// Command normalizer is the first durable step in the pipeline.
//
// It consumes the raw PayPal webhooks the gateway publishes, drops repeat
// deliveries, converts each dispute into Countersign's own vocabulary, and
// publishes the result on cs.dispute.events.
//
// The gateway already dedupes by event id, but in memory, which forgets
// everything on restart. This one claims each event id in Postgres, so a
// webhook PayPal retries after a redeploy is not processed a second time.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dheerajreddy01/countersign/services/pipeline/internal/bus"
	"github.com/dheerajreddy01/countersign/services/pipeline/internal/events"
	"github.com/dheerajreddy01/countersign/services/pipeline/internal/store"
)

const (
	consumerName = "normalizer"
	closeTimeout = 5 * time.Second
)

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	proxyURL := getenv("REDPANDA_PROXY_URL", "http://redpanda:8082")
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	db, err := store.Open(ctx, databaseURL)
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	defer db.Close()

	publisher := bus.NewPublisher(proxyURL)
	consumer, err := bus.NewConsumer(ctx, proxyURL, consumerName+"-group", []string{bus.TopicWebhooksRaw})
	if err != nil {
		log.Fatalf("consumer: %v", err)
	}
	defer func() {
		// Close on a fresh context: ctx is already cancelled during shutdown.
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
		defer cancel()
		if err := consumer.Close(closeCtx); err != nil {
			log.Printf("closing consumer: %v", err)
		}
	}()

	log.Printf("%s consuming %s", consumerName, bus.TopicWebhooksRaw)
	err = bus.Run(ctx, consumer, publisher, consumerName, handler(db, publisher), log.Printf)
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("%s stopped: %v", consumerName, err)
	}
	log.Printf("%s stopped", consumerName)
}

func handler(db *store.Store, publisher *bus.Publisher) bus.Handler {
	return func(ctx context.Context, rec bus.Record) error {
		dispute, err := events.Normalize(rec.Value)
		if err != nil {
			if errors.Is(err, events.ErrNotADispute) {
				// Another subscriber's event sharing the topic. Acknowledge it
				// rather than dead-lettering: it is not a failure.
				return nil
			}
			return err
		}

		fresh, err := db.RecordWebhookEvent(ctx, dispute.EventID, dispute.EventType, consumerName)
		if err != nil {
			return err
		}
		if !fresh {
			log.Printf("%s: event %s already handled, skipping", consumerName, dispute.EventID)
			return nil
		}

		encoded, err := json.Marshal(dispute)
		if err != nil {
			return err
		}
		if err := publisher.Publish(ctx, bus.TopicDisputeEvents, dispute.DisputeID, encoded); err != nil {
			return err
		}

		if !dispute.Supported {
			// Recorded, not hidden: a reason Countersign does not model still
			// reaches the merchant's board, it just is not scored.
			log.Printf("%s: dispute %s has unmodelled reason %q",
				consumerName, dispute.DisputeID, dispute.RawReason)
		}
		log.Printf("%s: dispute %s (%s, %s) normalised",
			consumerName, dispute.DisputeID, dispute.Reason, dispute.Stage)
		return nil
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
