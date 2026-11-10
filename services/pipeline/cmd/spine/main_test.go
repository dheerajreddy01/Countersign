package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// script writes an executable shell script and returns its path.
func script(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// marker returns a path a child can create when it receives SIGTERM, so a test
// can tell a child was stopped rather than merely outlived.
func marker(t *testing.T) string { return filepath.Join(t.TempDir(), "stopped") }

// lockedBuffer is safe for run's writer goroutines and the test to share.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func sleeperThatRecords(t *testing.T, name, mark string) string {
	return script(t, name, "trap 'touch "+mark+"; exit 0' TERM\nwhile :; do sleep 0.1; done")
}

func TestOneChildExitingStopsTheRest(t *testing.T) {
	mark := marker(t)
	sleeper := sleeperThatRecords(t, "worker", mark)
	dier := script(t, "caseservice", "echo going down\nexit 3")

	var out lockedBuffer
	err := run(context.Background(), []string{sleeper, dier}, os.Environ(), &out, 5*time.Second)

	if err == nil || !strings.Contains(err.Error(), "caseservice exited") {
		t.Fatalf("want an error naming caseservice, got %v", err)
	}
	if _, statErr := os.Stat(mark); statErr != nil {
		t.Fatal("the surviving child was not sent SIGTERM")
	}
	if !strings.Contains(out.String(), "[caseservice] going down") {
		t.Fatalf("child output not prefixed and forwarded:\n%s", out.String())
	}
}

// A child that exits cleanly is still a failure: nothing in the pipeline is
// meant to finish, so status 0 means it stopped doing its job.
func TestCleanChildExitIsStillAFailure(t *testing.T) {
	sleeper := sleeperThatRecords(t, "worker", marker(t))
	quitter := script(t, "normalizer", "exit 0")

	err := run(context.Background(), []string{sleeper, quitter}, os.Environ(), &lockedBuffer{}, 5*time.Second)
	if err == nil {
		t.Fatal("a child exiting with status 0 must still fail the container")
	}
}

func TestCancelShutsDownCleanly(t *testing.T) {
	markA, markB := marker(t), marker(t)
	a := sleeperThatRecords(t, "a", markA)
	b := sleeperThatRecords(t, "b", markB)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, []string{a, b}, os.Environ(), &lockedBuffer{}, 5*time.Second) }()

	time.Sleep(300 * time.Millisecond) // let both install their traps
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("clean shutdown returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after cancel")
	}
	for _, m := range []string{markA, markB} {
		if _, err := os.Stat(m); err != nil {
			t.Fatalf("child was not sent SIGTERM (%s missing)", m)
		}
	}
}

func TestChildIgnoringSIGTERMIsKilledAfterGrace(t *testing.T) {
	stubborn := script(t, "stubborn", "trap '' TERM\nwhile :; do sleep 0.1; done")
	dier := script(t, "dier", "exit 1")

	start := time.Now()
	err := run(context.Background(), []string{stubborn, dier}, os.Environ(), &lockedBuffer{}, 500*time.Millisecond)
	if err == nil {
		t.Fatal("want an error")
	}
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Fatalf("took %s; the stubborn child was not killed after the grace period", elapsed)
	}
}

func TestMissingBinaryFailsAtStart(t *testing.T) {
	err := run(context.Background(), []string{"/nonexistent/caseservice"}, os.Environ(), &lockedBuffer{}, time.Second)
	if err == nil || !strings.Contains(err.Error(), "start /nonexistent/caseservice") {
		t.Fatalf("want a start error, got %v", err)
	}
}

func TestLoopbackDefaultsDoNotOverrideExplicitValues(t *testing.T) {
	env := withDefaults([]string{"NORMALIZER_ADDR=:9999", "EXECUTOR_ADDR="}, loopbackDefaults)
	got := map[string][]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = append(got[k], v)
	}
	if v := got["NORMALIZER_ADDR"]; len(v) != 1 || v[0] != ":9999" {
		t.Errorf("explicit NORMALIZER_ADDR overridden: %v", v)
	}
	if v := got["EXECUTOR_ADDR"]; len(v) != 1 || v[0] != "" {
		t.Errorf("explicitly empty EXECUTOR_ADDR overridden: %v", v)
	}
	if v := got["EVIDENCE_BUILDER_ADDR"]; len(v) != 1 || v[0] != "127.0.0.1:8085" {
		t.Errorf("unset EVIDENCE_BUILDER_ADDR not defaulted to loopback: %v", v)
	}
}
