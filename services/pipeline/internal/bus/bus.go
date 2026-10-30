// Package bus talks to Redpanda through its HTTP proxy (pandaproxy) rather
// than a Kafka client library.
//
// This matches services/gateway/publisher.go and keeps the whole pipeline free
// of a Kafka driver dependency, which is worth more here than throughput: the
// demo moves a handful of events, and the proxy is already running in compose.
// Swapping in franz-go later means replacing this file, not its callers.
package bus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const contentType = "application/vnd.kafka.json.v2+json"

// Topics carried by the pipeline. These are the names created by
// infra/redpanda/create-topics.sh.
const (
	TopicWebhooksRaw       = "pp.webhooks.raw"
	TopicDisputeEvents     = "cs.dispute.events"
	TopicEvidenceRequested = "cs.evidence.requested"
	TopicCaseReady         = "cs.case.ready"
	TopicDecisionPending   = "cs.decision.pending"
	TopicActionRequested   = "cs.action.requested"
	TopicOutcomes          = "cs.outcomes"
	TopicDLQ               = "cs.dlq"
)

// Publisher writes JSON records to a topic.
type Publisher struct {
	baseURL string
	client  *http.Client
}

func NewPublisher(baseURL string) *Publisher {
	return &Publisher{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

// Publish writes one record. value must already be valid JSON.
func (p *Publisher) Publish(ctx context.Context, topic, key string, value []byte) error {
	if !json.Valid(value) {
		return fmt.Errorf("publish to %s: value is not valid JSON", topic)
	}
	payload, err := json.Marshal(map[string]any{
		"records": []map[string]any{{"key": key, "value": json.RawMessage(value)}},
	})
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("%s/topics/%s", p.baseURL, url.PathEscape(topic))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("publish to %s: %w", topic, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("publish to %s: proxy returned %s: %s", topic, resp.Status, body)
	}
	return nil
}

// Record is one consumed message.
type Record struct {
	Topic     string          `json:"topic"`
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	Partition int             `json:"partition"`
	Offset    int64           `json:"offset"`
}

// Consumer is a pandaproxy consumer-group instance subscribed to one or more
// topics. It is not safe for concurrent use.
type Consumer struct {
	baseURL  string
	group    string
	instance string
	client   *http.Client
}

// NewConsumer creates a consumer instance in `group`, subscribed to `topics`.
//
// Offsets are committed explicitly, not automatically: a record must not count
// as handled until the handler has actually succeeded, or a crash mid-handler
// silently drops a dispute.
func NewConsumer(ctx context.Context, baseURL, group string, topics []string) (*Consumer, error) {
	if len(topics) == 0 {
		return nil, errors.New("a consumer needs at least one topic")
	}
	c := &Consumer{
		baseURL: strings.TrimRight(baseURL, "/"),
		group:   group,
		client:  &http.Client{Timeout: 30 * time.Second},
	}

	created := struct {
		InstanceID string `json:"instance_id"`
	}{}
	body := map[string]any{
		"format":             "json",
		"auto.offset.reset":  "earliest",
		"auto.commit.enable": "false",
	}
	endpoint := fmt.Sprintf("%s/consumers/%s", c.baseURL, url.PathEscape(group))
	if err := c.do(ctx, http.MethodPost, endpoint, body, &created); err != nil {
		return nil, fmt.Errorf("create consumer in group %s: %w", group, err)
	}
	if created.InstanceID == "" {
		return nil, errors.New("proxy returned no instance_id")
	}
	c.instance = created.InstanceID

	// The instance URI is built from the configured base rather than the
	// proxy's own base_uri: the proxy advertises a hostname that need not be
	// reachable from inside the compose network.
	if err := c.do(ctx, http.MethodPost, c.instanceURL()+"/subscription",
		map[string]any{"topics": topics}, nil); err != nil {
		return nil, fmt.Errorf("subscribe to %v: %w", topics, err)
	}
	return c, nil
}

func (c *Consumer) instanceURL() string {
	return fmt.Sprintf("%s/consumers/%s/instances/%s",
		c.baseURL, url.PathEscape(c.group), url.PathEscape(c.instance))
}

// Poll fetches the next batch, waiting up to `wait` for records to arrive. An
// empty slice with a nil error means the topic is simply idle.
func (c *Consumer) Poll(ctx context.Context, wait time.Duration) ([]Record, error) {
	endpoint := fmt.Sprintf("%s/records?timeout=%d", c.instanceURL(), wait.Milliseconds())
	var records []Record
	if err := c.do(ctx, http.MethodGet, endpoint, nil, &records); err != nil {
		return nil, err
	}
	return records, nil
}

// Commit records that everything up to and including `rec` has been handled.
func (c *Consumer) Commit(ctx context.Context, rec Record) error {
	body := map[string]any{
		"partitions": []map[string]any{
			{"topic": rec.Topic, "partition": rec.Partition, "offset": rec.Offset + 1},
		},
	}
	return c.do(ctx, http.MethodPost, c.instanceURL()+"/offsets", body, nil)
}

// Close removes the consumer instance so the group rebalances promptly instead
// of waiting for a session timeout.
func (c *Consumer) Close(ctx context.Context) error {
	if c.instance == "" {
		return nil
	}
	err := c.do(ctx, http.MethodDelete, c.instanceURL(), nil, nil)
	c.instance = ""
	return err
}

func (c *Consumer) do(ctx context.Context, method, endpoint string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", contentType)

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%s %s: proxy returned %s: %s", method, endpoint, resp.Status, raw)
	}
	if out == nil {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// Handler processes one record. Returning an error sends the record to the dead
// letter topic; the offset is committed either way, so one bad record cannot
// block the partition behind it.
type Handler func(ctx context.Context, rec Record) error

// DeadLetter is the envelope written to cs.dlq.
type DeadLetter struct {
	Topic     string          `json:"topic"`
	Partition int             `json:"partition"`
	Offset    int64           `json:"offset"`
	Key       string          `json:"key"`
	Reason    string          `json:"reason"`
	Consumer  string          `json:"consumer"`
	FailedAt  time.Time       `json:"failed_at"`
	Value     json.RawMessage `json:"value"`
}

// Run polls until ctx is cancelled, handing each record to h.
//
// A handler failure is dead-lettered rather than retried forever: a dispute
// that cannot be parsed will not parse on the tenth attempt either, and
// blocking the partition would stall every dispute behind it. If the record
// cannot even be dead-lettered, the offset is left uncommitted so the record is
// redelivered rather than lost.
func Run(ctx context.Context, c *Consumer, pub *Publisher, name string, h Handler, logf func(string, ...any)) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		records, err := c.Poll(ctx, time.Second)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			logf("%s: poll failed, retrying: %v", name, err)
			if !sleep(ctx, 2*time.Second) {
				return ctx.Err()
			}
			continue
		}

		for _, rec := range records {
			if handleErr := h(ctx, rec); handleErr != nil {
				logf("%s: %s[%d]@%d failed: %v", name, rec.Topic, rec.Partition, rec.Offset, handleErr)
				if dlqErr := deadLetter(ctx, pub, name, rec, handleErr); dlqErr != nil {
					// Leave the offset uncommitted: redelivery is better than
					// dropping the record with no record of why.
					logf("%s: could not dead-letter %s@%d: %v", name, rec.Topic, rec.Offset, dlqErr)
					continue
				}
			}
			// Commit on a context detached from shutdown. The record has
			// already been handled; letting cancellation skip the commit
			// would redeliver and reprocess it on the next boot.
			commitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			if err := c.Commit(commitCtx, rec); err != nil {
				logf("%s: commit %s@%d failed: %v", name, rec.Topic, rec.Offset, err)
			}
			cancel()
		}
	}
}

func deadLetter(ctx context.Context, pub *Publisher, name string, rec Record, cause error) error {
	if pub == nil {
		return errors.New("no publisher configured")
	}
	encoded, err := json.Marshal(DeadLetter{
		Topic:     rec.Topic,
		Partition: rec.Partition,
		Offset:    rec.Offset,
		Key:       rec.Key,
		Reason:    cause.Error(),
		Consumer:  name,
		FailedAt:  time.Now().UTC(),
		Value:     rec.Value,
	})
	if err != nil {
		return err
	}
	return pub.Publish(ctx, TopicDLQ, rec.Key, encoded)
}

// sleep waits for d, reporting false if ctx was cancelled first.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
