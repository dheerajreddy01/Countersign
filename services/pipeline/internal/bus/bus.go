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
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// pandaproxy wants the embedded format (…json.v2+json) only on the record
// paths: producing and fetching. Consumer management — creating an instance,
// subscribing, committing offsets, deleting — takes the plain …v2+json type,
// and the embedded form there is rejected with 415 Unsupported Media Type.
const (
	recordsContentType  = "application/vnd.kafka.json.v2+json"
	consumerContentType = "application/vnd.kafka.v2+json"
)

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
	req.Header.Set("Content-Type", recordsContentType)

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
//
// The instance itself lives in the broker's memory, not in the group: when
// Redpanda restarts, every instance id it handed out stops existing and the
// proxy answers 404 / 40403 for it. Consumer keeps what it needs to make a new
// one (group, topics) and does so the first time a poll meets that answer.
type Consumer struct {
	baseURL  string
	group    string
	topics   []string
	instance string
	client   *http.Client

	topicPoll time.Duration
}

// topicPollInterval is how often a consumer re-checks for topics that do not
// exist yet. A variable so tests need not wait out the production interval.
var topicPollInterval = 2 * time.Second

// ProxyError is a non-2xx answer from pandaproxy.
type ProxyError struct {
	Method, Endpoint, Status string
	StatusCode, Code         int
	Body                     string
}

func (e *ProxyError) Error() string {
	return fmt.Sprintf("%s %s: proxy returned %s: %s", e.Method, e.Endpoint, e.Status, e.Body)
}

// errInstanceGone is pandaproxy's "consumer instance not found".
const errInstanceGone = 40403

// IsInstanceGone reports whether err means the consumer instance no longer
// exists on the broker, which is what every poll returns after Redpanda
// restarts. Retrying the same instance can never succeed.
func IsInstanceGone(err error) bool {
	var pe *ProxyError
	return errors.As(err, &pe) && pe.StatusCode == http.StatusNotFound && pe.Code == errInstanceGone
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
		baseURL:   strings.TrimRight(baseURL, "/"),
		group:     group,
		topics:    append([]string(nil), topics...),
		client:    &http.Client{Timeout: 30 * time.Second},
		topicPoll: topicPollInterval,
	}
	if err := c.connect(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

// connect waits for the topics to exist, then creates a consumer instance and
// subscribes it.
//
// The wait is not politeness. A pandaproxy subscription to a topic that does not
// exist yet joins the group and is assigned nothing, and creating the topic
// later does not trigger a rebalance: the member stays Stable with zero
// partitions, its health check passes, and it never consumes a record. On a
// platform that boots every service in parallel with the broker (Render), that
// is the default outcome unless the subscription waits.
func (c *Consumer) connect(ctx context.Context) error {
	if err := c.waitForTopics(ctx); err != nil {
		return err
	}

	created := struct {
		InstanceID string `json:"instance_id"`
	}{}
	body := map[string]any{
		"format":             "json",
		"auto.offset.reset":  "earliest",
		"auto.commit.enable": "false",
	}
	endpoint := fmt.Sprintf("%s/consumers/%s", c.baseURL, url.PathEscape(c.group))
	if err := c.do(ctx, http.MethodPost, endpoint, body, &created, consumerContentType); err != nil {
		return fmt.Errorf("create consumer in group %s: %w", c.group, err)
	}
	if created.InstanceID == "" {
		return errors.New("proxy returned no instance_id")
	}
	c.instance = created.InstanceID

	// The instance URI is built from the configured base rather than the
	// proxy's own base_uri: the proxy advertises a hostname that need not be
	// reachable from inside the compose network (on Render it advertises
	// 127.0.0.1, which from any other service is that service itself).
	if err := c.do(ctx, http.MethodPost, c.instanceURL()+"/subscription",
		map[string]any{"topics": c.topics}, nil, consumerContentType); err != nil {
		return fmt.Errorf("subscribe to %v: %w", c.topics, err)
	}
	// Logged with the address so a deploy shows at a glance which proxy every
	// worker actually resolved: a wrong *_HOSTPORT is otherwise silent.
	log.Printf("bus: group %s subscribed to %v via %s", c.group, c.topics, c.baseURL)
	return nil
}

// waitForTopics blocks until every topic the consumer needs exists, retrying
// through an unreachable proxy as well: at boot the broker may simply not be up
// yet. It logs what it is waiting for, so a missing topic reads as a missing
// topic in the logs rather than as a worker that quietly does nothing.
func (c *Consumer) waitForTopics(ctx context.Context) error {
	// What the last answered check found missing. Kept apart from the current
	// attempt so a check cut short by the deadline does not blame every topic.
	missing := c.topics
	for attempt := 0; ; attempt++ {
		var existing []string
		err := c.do(ctx, http.MethodGet, c.baseURL+"/topics", nil, &existing, consumerContentType)
		if err == nil {
			have := make(map[string]bool, len(existing))
			for _, t := range existing {
				have[t] = true
			}
			missing = nil
			for _, t := range c.topics {
				if !have[t] {
					missing = append(missing, t)
				}
			}
			if len(missing) == 0 {
				return nil
			}
		}
		if attempt%15 == 0 {
			if err != nil {
				log.Printf("bus: group %s waiting for the proxy at %s: %v", c.group, c.baseURL, err)
			} else {
				log.Printf("bus: group %s waiting for topics %v to exist", c.group, missing)
			}
		}
		if !sleep(ctx, c.topicPoll) {
			return fmt.Errorf("waiting for topics %v: %w", missing, ctx.Err())
		}
	}
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
	if err := c.do(ctx, http.MethodGet, endpoint, nil, &records, recordsContentType); err != nil {
		if IsInstanceGone(err) {
			// The broker restarted and forgot this instance. Polling it again can
			// only 404 again, forever, behind a passing health check, so make a
			// new one. The group's committed offsets survive the restart, so
			// consumption resumes after the last handled record; anything handled
			// but not yet committed is redelivered, which every handler tolerates.
			lost := c.instance
			c.instance = ""
			if rerr := c.connect(ctx); rerr != nil {
				return nil, fmt.Errorf("consumer instance %s was lost and could not be re-created: %w", lost, rerr)
			}
			return nil, fmt.Errorf("consumer instance %s was lost (broker restart?); re-created as %s", lost, c.instance)
		}
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
	return c.do(ctx, http.MethodPost, c.instanceURL()+"/offsets", body, nil, consumerContentType)
}

// Close forgets the consumer instance without deleting it on the broker.
//
// Deliberately not a DELETE. On Redpanda v24.2 (seen on .7 and .27) deleting a
// pandaproxy consumer that has been consuming for a while sends the broker to
// 100% CPU for good: every later produce times out and the pipeline stops while
// every health check still passes, and a private service on Render is not
// restarted for that. Letting the proxy expire the instance instead
// (pandaproxy.consumer_instance_timeout_ms, set to 60s in infra/redpanda) was
// tested the same way and is safe. The cost: a restarted worker shares its
// group with the dead instance until it expires, so consumption can pause for
// up to that long after a deploy. Nothing is lost — offsets are committed only
// after a record is handled.
func (c *Consumer) Close(context.Context) error {
	c.instance = ""
	return nil
}

func (c *Consumer) do(ctx context.Context, method, endpoint string, body, out any, mediaType string) error {
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
	// pandaproxy insists on the type for DELETE too, even with no body.
	if body != nil || method == http.MethodDelete {
		req.Header.Set("Content-Type", mediaType)
	}
	req.Header.Set("Accept", mediaType)

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		pe := &ProxyError{Method: method, Endpoint: endpoint, Status: resp.Status,
			StatusCode: resp.StatusCode, Body: string(raw)}
		var coded struct {
			ErrorCode int `json:"error_code"`
		}
		if json.Unmarshal(raw, &coded) == nil {
			pe.Code = coded.ErrorCode
		}
		return pe
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

// ProxyURLFromEnv resolves the pandaproxy base URL.
//
// REDPANDA_PROXY_URL wins when set. Otherwise REDPANDA_PROXY_HOSTPORT is
// accepted, because a platform that assigns internal hostnames at deploy time
// can inject "host:port" from its own service registry but cannot interpolate
// it into a URL (Render's fromService gives hostport; its Blueprints have no
// string interpolation). Without this the deployment has to be hand-edited
// after every create.
func ProxyURLFromEnv(fallback string) string {
	if url := strings.TrimSpace(os.Getenv("REDPANDA_PROXY_URL")); url != "" {
		return url
	}
	if hostport := strings.TrimSpace(os.Getenv("REDPANDA_PROXY_HOSTPORT")); hostport != "" {
		return "http://" + hostport
	}
	return fallback
}
