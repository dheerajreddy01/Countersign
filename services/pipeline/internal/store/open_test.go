package store

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestPingRetriesUntilTheServerComesUp(t *testing.T) {
	calls := 0
	ping := func(context.Context) error {
		calls++
		if calls < 3 {
			return errors.New("dial tcp 10.0.0.1:5432: connect: connection refused")
		}
		return nil
	}
	if err := pingUntilReady(context.Background(), ping, time.Second, time.Millisecond); err != nil {
		t.Fatalf("want success once the server answers, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("want 3 attempts, got %d", calls)
	}
}

func TestPingRetriesWhileTheServerIsStarting(t *testing.T) {
	calls := 0
	ping := func(context.Context) error {
		calls++
		if calls == 1 {
			return &pgconn.PgError{Code: "57P03", Message: "the database system is starting up"}
		}
		return nil
	}
	if err := pingUntilReady(context.Background(), ping, time.Second, time.Millisecond); err != nil {
		t.Fatalf("57P03 should be retried, got %v", err)
	}
}

// A server that answers with a real error is misconfiguration, and waiting
// two minutes to report it would only hide it.
func TestPingFailsAtOnceOnARealServerError(t *testing.T) {
	calls := 0
	authErr := &pgconn.PgError{Code: "28P01", Message: "password authentication failed"}
	ping := func(context.Context) error {
		calls++
		return fmt.Errorf("failed to connect: %w", authErr)
	}
	err := pingUntilReady(context.Background(), ping, time.Minute, time.Millisecond)
	if !errors.As(err, new(*pgconn.PgError)) {
		t.Fatalf("want the auth error back, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("a real server error must not be retried; got %d attempts", calls)
	}
}

func TestPingGivesUpAfterTheTimeout(t *testing.T) {
	ping := func(context.Context) error { return errors.New("connection refused") }
	start := time.Now()
	err := pingUntilReady(context.Background(), ping, 50*time.Millisecond, 10*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "still unavailable") {
		t.Fatalf("want a timeout error, got %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("did not respect the timeout")
	}
}

func TestPingStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ping := func(context.Context) error { cancel(); return errors.New("connection refused") }
	err := pingUntilReady(ctx, ping, time.Minute, time.Minute)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

// End to end through Open and pgx: a port with nothing listening is retried
// (not failed on the first refused dial) and then reported.
func TestOpenRetriesARefusedConnection(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close() // nothing listens here now: every dial is refused

	defer func(t, i time.Duration) { OpenTimeout, openRetryInterval = t, i }(OpenTimeout, openRetryInterval)
	OpenTimeout, openRetryInterval = 300*time.Millisecond, 50*time.Millisecond

	_, err = Open(context.Background(), "postgres://u:p@"+addr+"/db?sslmode=disable&connect_timeout=1")
	if err == nil || !strings.Contains(err.Error(), "still unavailable") {
		t.Fatalf("want a retried-then-unavailable error, got %v", err)
	}
}
