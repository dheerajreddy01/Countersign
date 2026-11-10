package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeProxy is a minimal stand-in for Redpanda's HTTP proxy: enough of the
// Kafka REST v2 surface to drive a consumer through subscribe, poll, commit
// and close without a broker.
type fakeProxy struct {
	mu sync.Mutex

	batches   [][]Record // returned by successive polls
	published []struct {
		Topic string
		Key   string
		Value json.RawMessage
	}
	committed    []int64
	subscribed   []string
	deleted      bool
	pollFailures int // fail this many polls before serving records

	// Topic existence, as GET /topics reports it. nil means "every topic the
	// pipeline uses exists", which is what the older tests assume.
	topics []string
	// Answer GET /topics with no topics this many times first, the way a broker
	// that has just booted does before anything has created them.
	topicsMissingFor int
	topicChecks      int
	// Subscriptions made while a requested topic was missing. A real broker
	// accepts those and then never assigns the member anything.
	subscribedEarly int

	// Live consumer instances. restart() forgets them all, as a broker restart
	// does; polling a forgotten one is pandaproxy's 404 / 40403.
	instances map[string]bool
	created   int
}

// restart forgets every consumer instance, as a Redpanda restart does.
func (f *fakeProxy) restart() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.instances = map[string]bool{}
}

func (f *fakeProxy) topicExists(name string) bool {
	if f.topicChecks <= f.topicsMissingFor {
		return false
	}
	if f.topics == nil {
		return true
	}
	for _, t := range f.topics {
		if t == name {
			return true
		}
	}
	return false
}

func (f *fakeProxy) liveInstance(w http.ResponseWriter, r *http.Request) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.instances[r.PathValue("id")] {
		w.Header().Set("Content-Type", consumerContentType)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error_code":40403,"message":"Consumer instance not found"}`))
		return false
	}
	return true
}

// wrongMediaType mimics pandaproxy's 415: the consumer-management paths take
// the plain v2 type and the record paths take the embedded one. Without this
// check the fake accepts anything and a wrong Content-Type only shows up
// against a real broker.
func wrongMediaType(t *testing.T, w http.ResponseWriter, r *http.Request, header, want string) bool {
	t.Helper()
	if got := r.Header.Get(header); got != want {
		t.Errorf("%s %s: %s = %q, want %q", r.Method, r.URL.Path, header, got, want)
		http.Error(w, `{"error_code":415,"message":"unsupported_media_type"}`,
			http.StatusUnsupportedMediaType)
		return true
	}
	return false
}

func (f *fakeProxy) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("GET /topics", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.topicChecks++
		var names []string
		for _, t := range []string{TopicWebhooksRaw, TopicDisputeEvents, TopicEvidenceRequested,
			TopicCaseReady, TopicDecisionPending, TopicActionRequested, TopicOutcomes, TopicDLQ} {
			if f.topicExists(t) {
				names = append(names, t)
			}
		}
		f.mu.Unlock()
		if names == nil {
			names = []string{}
		}
		w.Header().Set("Content-Type", consumerContentType)
		_ = json.NewEncoder(w).Encode(names)
	})

	mux.HandleFunc("POST /consumers/{group}", func(w http.ResponseWriter, r *http.Request) {
		if wrongMediaType(t, w, r, "Content-Type", consumerContentType) {
			return
		}
		f.mu.Lock()
		f.created++
		id := fmt.Sprintf("inst-%d", f.created)
		if f.instances == nil {
			f.instances = map[string]bool{}
		}
		f.instances[id] = true
		f.mu.Unlock()
		w.Header().Set("Content-Type", consumerContentType)
		_, _ = fmt.Fprintf(w, `{"instance_id":%q,"base_uri":"http://unreachable.invalid/x"}`, id)
	})

	mux.HandleFunc("POST /consumers/{group}/instances/{id}/subscription",
		func(w http.ResponseWriter, r *http.Request) {
			if wrongMediaType(t, w, r, "Content-Type", consumerContentType) {
				return
			}
			if !f.liveInstance(w, r) {
				return
			}
			var body struct{ Topics []string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.subscribed = body.Topics
			for _, topic := range body.Topics {
				if !f.topicExists(topic) {
					f.subscribedEarly++
				}
			}
			f.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		})

	mux.HandleFunc("GET /consumers/{group}/instances/{id}/records",
		func(w http.ResponseWriter, r *http.Request) {
			if wrongMediaType(t, w, r, "Accept", recordsContentType) {
				return
			}
			if !f.liveInstance(w, r) {
				return
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.pollFailures > 0 {
				f.pollFailures--
				http.Error(w, "proxy unavailable", http.StatusServiceUnavailable)
				return
			}
			var batch []Record
			if len(f.batches) > 0 {
				batch, f.batches = f.batches[0], f.batches[1:]
			} else {
				batch = []Record{}
			}
			w.Header().Set("Content-Type", recordsContentType)
			_ = json.NewEncoder(w).Encode(batch)
		})

	mux.HandleFunc("POST /consumers/{group}/instances/{id}/offsets",
		func(w http.ResponseWriter, r *http.Request) {
			if wrongMediaType(t, w, r, "Content-Type", consumerContentType) {
				return
			}
			var body struct {
				Partitions []struct{ Offset int64 } `json:"partitions"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			for _, p := range body.Partitions {
				f.committed = append(f.committed, p.Offset)
			}
			f.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		})

	// DELETE is checked like every other consumer-management path. It was the
	// one route the fake waved through, and the real proxy refuses it with 415
	// when the type is missing, so Close failed against every real broker while
	// passing here.
	mux.HandleFunc("DELETE /consumers/{group}/instances/{id}",
		func(w http.ResponseWriter, r *http.Request) {
			if wrongMediaType(t, w, r, "Content-Type", consumerContentType) {
				return
			}
			f.mu.Lock()
			f.deleted = true
			delete(f.instances, r.PathValue("id"))
			f.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		})

	mux.HandleFunc("POST /topics/{topic}", func(w http.ResponseWriter, r *http.Request) {
		if wrongMediaType(t, w, r, "Content-Type", recordsContentType) {
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Records []struct {
				Key   string          `json:"key"`
				Value json.RawMessage `json:"value"`
			} `json:"records"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		topic := r.PathValue("topic")
		f.mu.Lock()
		for _, rec := range body.Records {
			f.published = append(f.published, struct {
				Topic string
				Key   string
				Value json.RawMessage
			}{topic, rec.Key, rec.Value})
		}
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestPublishSendsTheRecordToTheTopic(t *testing.T) {
	f := &fakeProxy{}
	srv := f.server(t)

	pub := NewPublisher(srv.URL + "/")
	if err := pub.Publish(context.Background(), TopicDisputeEvents, "PP-D-1", []byte(`{"a":1}`)); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.published) != 1 {
		t.Fatalf("published %d records, want 1", len(f.published))
	}
	got := f.published[0]
	if got.Topic != TopicDisputeEvents || got.Key != "PP-D-1" {
		t.Errorf("published to %s key %s", got.Topic, got.Key)
	}
	if string(got.Value) != `{"a":1}` {
		t.Errorf("value = %s", got.Value)
	}
}

func TestPublishRejectsNonJSONValues(t *testing.T) {
	f := &fakeProxy{}
	srv := f.server(t)
	pub := NewPublisher(srv.URL)
	// Catching this here keeps a malformed record out of the topic, where it
	// would only fail on the consuming side.
	if err := pub.Publish(context.Background(), TopicDisputeEvents, "k", []byte("not json")); err == nil {
		t.Fatal("want an error for a non-JSON value")
	}
}

func TestPublishSurfacesProxyErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "topic missing", http.StatusNotFound)
	}))
	defer srv.Close()

	err := NewPublisher(srv.URL).Publish(context.Background(), "nope", "k", []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v, want it to mention the proxy status", err)
	}
}

func TestConsumerSubscribesPollsAndCommits(t *testing.T) {
	f := &fakeProxy{batches: [][]Record{{
		{Topic: TopicWebhooksRaw, Key: "WH-1", Value: json.RawMessage(`{"n":1}`), Partition: 2, Offset: 41},
	}}}
	srv := f.server(t)
	ctx := context.Background()

	c, err := NewConsumer(ctx, srv.URL, "test-group", []string{TopicWebhooksRaw})
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}

	f.mu.Lock()
	subscribed := append([]string(nil), f.subscribed...)
	f.mu.Unlock()
	if len(subscribed) != 1 || subscribed[0] != TopicWebhooksRaw {
		t.Fatalf("subscribed = %v", subscribed)
	}

	records, err := c.Poll(ctx, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(records) != 1 || records[0].Key != "WH-1" {
		t.Fatalf("records = %+v", records)
	}

	if err := c.Commit(ctx, records[0]); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	f.mu.Lock()
	committed := append([]int64(nil), f.committed...)
	f.mu.Unlock()
	// Kafka commits the next offset to read, not the one just handled.
	if len(committed) != 1 || committed[0] != 42 {
		t.Errorf("committed = %v, want [42]", committed)
	}

	if err := c.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	f.mu.Lock()
	deleted := f.deleted
	f.mu.Unlock()
	if deleted {
		t.Error("Close must not delete the instance: on Redpanda v24.2 that hangs the broker")
	}
}

func TestConsumerNeedsAtLeastOneTopic(t *testing.T) {
	if _, err := NewConsumer(context.Background(), "http://x", "g", nil); err == nil {
		t.Fatal("want an error with no topics")
	}
}

func TestPollReturnsEmptyOnAnIdleTopic(t *testing.T) {
	f := &fakeProxy{}
	srv := f.server(t)
	ctx := context.Background()
	c, err := NewConsumer(ctx, srv.URL, "g", []string{TopicWebhooksRaw})
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	records, err := c.Poll(ctx, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("want no records, got %d", len(records))
	}
}

func TestRunHandlesRecordsAndCommitsThem(t *testing.T) {
	f := &fakeProxy{batches: [][]Record{{
		{Topic: TopicWebhooksRaw, Key: "WH-1", Value: json.RawMessage(`{"n":1}`), Offset: 0},
		{Topic: TopicWebhooksRaw, Key: "WH-2", Value: json.RawMessage(`{"n":2}`), Offset: 1},
	}}}
	srv := f.server(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c, err := NewConsumer(ctx, srv.URL, "g", []string{TopicWebhooksRaw})
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}

	var seen []string
	var mu sync.Mutex
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = Run(ctx, c, NewPublisher(srv.URL), "test", func(_ context.Context, rec Record) error {
			mu.Lock()
			seen = append(seen, rec.Key)
			if len(seen) == 2 {
				cancel()
			}
			mu.Unlock()
			return nil
		}, func(string, ...any) {})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop after the context was cancelled")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 || seen[0] != "WH-1" || seen[1] != "WH-2" {
		t.Fatalf("handled %v, want [WH-1 WH-2]", seen)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.committed) != 2 {
		t.Errorf("committed %v, want both records", f.committed)
	}
}

func TestRunDeadLettersAFailingRecordAndKeepsGoing(t *testing.T) {
	// A record that cannot be handled must not block the ones behind it.
	f := &fakeProxy{batches: [][]Record{{
		{Topic: TopicWebhooksRaw, Key: "bad", Value: json.RawMessage(`{"n":1}`), Offset: 7},
		{Topic: TopicWebhooksRaw, Key: "good", Value: json.RawMessage(`{"n":2}`), Offset: 8},
	}}}
	srv := f.server(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c, err := NewConsumer(ctx, srv.URL, "g", []string{TopicWebhooksRaw})
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = Run(ctx, c, NewPublisher(srv.URL), "test", func(_ context.Context, rec Record) error {
			if rec.Key == "bad" {
				return errors.New("cannot parse")
			}
			cancel()
			return nil
		}, func(string, ...any) {})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	var dlq []json.RawMessage
	for _, p := range f.published {
		if p.Topic == TopicDLQ {
			dlq = append(dlq, p.Value)
		}
	}
	if len(dlq) != 1 {
		t.Fatalf("dead-lettered %d records, want 1", len(dlq))
	}
	var letter DeadLetter
	if err := json.Unmarshal(dlq[0], &letter); err != nil {
		t.Fatalf("dead letter is not decodable: %v", err)
	}
	if letter.Reason != "cannot parse" || letter.Offset != 7 || letter.Key != "bad" {
		t.Errorf("dead letter = %+v", letter)
	}
	if string(letter.Value) != `{"n":1}` {
		t.Errorf("dead letter lost the original value: %s", letter.Value)
	}
	// Both offsets commit: the bad one is recorded in the DLQ, so replaying it
	// forever would only stall the partition.
	if len(f.committed) != 2 {
		t.Errorf("committed = %v, want both offsets", f.committed)
	}
}

func TestRunRetriesAfterAPollFailure(t *testing.T) {
	f := &fakeProxy{
		pollFailures: 1,
		batches: [][]Record{{
			{Topic: TopicWebhooksRaw, Key: "WH-1", Value: json.RawMessage(`{"n":1}`), Offset: 0},
		}},
	}
	srv := f.server(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c, err := NewConsumer(ctx, srv.URL, "g", []string{TopicWebhooksRaw})
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}

	handled := make(chan string, 1)
	go func() {
		_ = Run(ctx, c, NewPublisher(srv.URL), "test", func(_ context.Context, rec Record) error {
			handled <- rec.Key
			cancel()
			return nil
		}, func(string, ...any) {})
	}()

	select {
	case key := <-handled:
		if key != "WH-1" {
			t.Fatalf("handled %q", key)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("Run did not recover from a transient poll failure")
	}
}

// fastTopicPoll shortens the topic re-check interval for one test.
func fastTopicPoll(t *testing.T) {
	t.Helper()
	old := topicPollInterval
	topicPollInterval = 5 * time.Millisecond
	t.Cleanup(func() { topicPollInterval = old })
}

// On Render every service boots in parallel with the broker, so a consumer
// routinely arrives before its topics exist. A real pandaproxy accepts that
// subscription and assigns the member nothing, forever: no error, a passing
// health check, and not one record consumed. The consumer must not subscribe
// until the topics are there.
func TestConsumerWaitsForItsTopicsBeforeSubscribing(t *testing.T) {
	fastTopicPoll(t)
	f := &fakeProxy{topicsMissingFor: 3}
	srv := f.server(t)

	if _, err := NewConsumer(context.Background(), srv.URL, "g", []string{TopicWebhooksRaw}); err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.subscribedEarly != 0 {
		t.Errorf("subscribed %d time(s) to a topic that did not exist yet", f.subscribedEarly)
	}
	if f.topicChecks < 4 {
		t.Errorf("checked for topics %d times, want it to keep checking until they appear", f.topicChecks)
	}
	if len(f.subscribed) != 1 {
		t.Errorf("never subscribed once the topic existed: %v", f.subscribed)
	}
}

func TestConsumerWaitsOnlyForTheTopicsItNeeds(t *testing.T) {
	fastTopicPoll(t)
	f := &fakeProxy{topics: []string{TopicOutcomes}}
	srv := f.server(t)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := NewConsumer(ctx, srv.URL, "g", []string{TopicOutcomes, TopicDisputeEvents})
	if err == nil {
		t.Fatal("NewConsumer returned while cs.dispute.events was still missing")
	}
	if !strings.Contains(err.Error(), TopicDisputeEvents) || strings.Contains(err.Error(), TopicOutcomes+" ") {
		t.Errorf("error should name only the missing topic: %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error should wrap the context's: %v", err)
	}
}

// A broker that is not up yet is the other half of a parallel boot. Exiting on
// the first refused connection only works on a platform that restarts the
// process; waiting works everywhere.
func TestConsumerWaitsForAProxyThatIsNotUpYet(t *testing.T) {
	fastTopicPoll(t)
	f := &fakeProxy{}
	real := f.server(t)

	var up sync.WaitGroup
	up.Add(1)
	refusals := 0
	var mu sync.Mutex
	gate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if refusals < 3 {
			refusals++
			mu.Unlock()
			http.Error(w, "starting", http.StatusServiceUnavailable)
			return
		}
		mu.Unlock()
		proxy, _ := http.NewRequestWithContext(r.Context(), r.Method, real.URL+r.URL.RequestURI(), r.Body)
		proxy.Header = r.Header
		resp, err := http.DefaultClient.Do(proxy)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for k, v := range resp.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	t.Cleanup(gate.Close)

	if _, err := NewConsumer(context.Background(), gate.URL, "g", []string{TopicWebhooksRaw}); err != nil {
		t.Fatalf("NewConsumer gave up on a proxy that came up: %v", err)
	}
}

// After a Redpanda restart every consumer instance id is gone and the proxy
// answers 404 / 40403 for it. The loop used to retry that same id forever, so
// one broker restart stopped the whole pipeline behind green health checks.
func TestPollRecreatesAnInstanceTheBrokerForgot(t *testing.T) {
	f := &fakeProxy{batches: [][]Record{{
		{Topic: TopicWebhooksRaw, Key: "WH-after-restart", Value: json.RawMessage(`{}`), Offset: 7},
	}}}
	srv := f.server(t)
	ctx := context.Background()

	c, err := NewConsumer(ctx, srv.URL, "g", []string{TopicWebhooksRaw})
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	f.restart()

	_, err = c.Poll(ctx, 10*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "re-created") {
		t.Fatalf("first poll after the restart should report the re-creation, got %v", err)
	}
	records, err := c.Poll(ctx, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("poll on the re-created instance: %v", err)
	}
	if len(records) != 1 || records[0].Key != "WH-after-restart" {
		t.Fatalf("records = %+v", records)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.created != 2 {
		t.Errorf("instances created = %d, want 2", f.created)
	}
	if len(f.subscribed) != 1 || f.subscribed[0] != TopicWebhooksRaw {
		t.Errorf("the new instance was not re-subscribed: %v", f.subscribed)
	}
}

func TestRunKeepsConsumingAcrossABrokerRestart(t *testing.T) {
	f := &fakeProxy{batches: [][]Record{{
		{Topic: TopicWebhooksRaw, Key: "WH-1", Value: json.RawMessage(`{}`)},
	}}}
	srv := f.server(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c, err := NewConsumer(ctx, srv.URL, "g", []string{TopicWebhooksRaw})
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	f.restart()

	handled := make(chan string, 1)
	go func() {
		_ = Run(ctx, c, NewPublisher(srv.URL), "test", func(_ context.Context, rec Record) error {
			handled <- rec.Key
			cancel()
			return nil
		}, func(string, ...any) {})
	}()
	select {
	case key := <-handled:
		if key != "WH-1" {
			t.Fatalf("handled %q", key)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("Run never recovered from the broker forgetting its consumer instance")
	}
}

// Only "instance not found" warrants a new instance. A transient failure must
// keep the existing one, or every blip would churn the group.
func TestOnlyALostInstanceIsRecreated(t *testing.T) {
	f := &fakeProxy{pollFailures: 2}
	srv := f.server(t)
	ctx := context.Background()
	c, err := NewConsumer(ctx, srv.URL, "g", []string{TopicWebhooksRaw})
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := c.Poll(ctx, time.Millisecond); err == nil || IsInstanceGone(err) {
			t.Fatalf("poll %d: want a transient error, got %v", i, err)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.created != 1 {
		t.Errorf("instances created = %d after transient failures, want 1", f.created)
	}
}

// Close leaves the instance for the broker to expire. Deleting it hangs Redpanda
// v24.2 at 100% CPU once the consumer has been running a while (reproduced in the
// compose stack on .7 and .27); expiry was tested the same way and is safe.
func TestCloseLeavesTheInstanceForTheBrokerToExpire(t *testing.T) {
	f := &fakeProxy{}
	srv := f.server(t)
	ctx := context.Background()
	c, err := NewConsumer(ctx, srv.URL, "g", []string{TopicWebhooksRaw})
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	if err := c.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleted {
		t.Error("Close sent a DELETE")
	}
	if c.instance != "" {
		t.Errorf("Close kept using instance %q", c.instance)
	}
}
