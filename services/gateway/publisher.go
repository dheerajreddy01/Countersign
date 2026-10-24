package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// proxyPublisher publishes through Redpanda's HTTP proxy, which keeps the
// scaffold free of third-party dependencies. Swap for a Kafka client later.
type proxyPublisher struct {
	baseURL string
	client  *http.Client
}

func newProxyPublisher(baseURL string) *proxyPublisher {
	return &proxyPublisher{baseURL: baseURL, client: &http.Client{Timeout: 5 * time.Second}}
}

func (p *proxyPublisher) Publish(topic, key string, value []byte) error {
	payload, err := json.Marshal(map[string]any{
		"records": []map[string]any{{"key": key, "value": json.RawMessage(value)}},
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/topics/%s", p.baseURL, topic), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/vnd.kafka.json.v2+json")

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("proxy returned %s", resp.Status)
	}
	return nil
}
