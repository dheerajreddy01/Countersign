package bus

import (
	"context"
	"encoding/json"
	"errors"
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
}

func (f *fakeProxy) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("POST /consumers/{group}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write([]byte(`{"instance_id":"inst-1","base_uri":"http://unreachable.invalid/x"}`))
	})

	mux.HandleFunc("POST /consumers/{group}/instances/{id}/subscription",
		func(w http.ResponseWriter, r *http.Request) {
			var body struct{ Topics []string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.subscribed = body.Topics
			f.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		})

	mux.HandleFunc("GET /consumers/{group}/instances/{id}/records",
		func(w http.ResponseWriter, r *http.Request) {
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
			w.Header().Set("Content-Type", contentType)
			_ = json.NewEncoder(w).Encode(batch)
		})

	mux.HandleFunc("POST /consumers/{group}/instances/{id}/offsets",
		func(w http.ResponseWriter, r *http.Request) {
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

	mux.HandleFunc("DELETE /consumers/{group}/instances/{id}",
		func(w http.ResponseWriter, r *http.Request) {
			f.mu.Lock()
			f.deleted = true
			f.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		})

	mux.HandleFunc("POST /topics/{topic}", func(w http.ResponseWriter, r *http.Request) {
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
	if !deleted {
		t.Error("Close should delete the consumer instance")
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
