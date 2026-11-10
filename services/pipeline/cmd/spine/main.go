// Command spine runs the case service and the three pipeline workers as child
// processes of one container.
//
// It exists for cost, not architecture. On Render each worker would otherwise
// be its own paid instance, and the three of them do little enough between
// disputes to share the case service's. The binaries are unchanged and still
// run separately under compose; this only decides where they live.
//
// The contract is all-or-nothing. If any child exits, for any reason and with
// any status, the rest are stopped and spine exits non-zero, so the platform
// restarts the whole container. A worker that died silently while the case
// service kept the health check green is exactly the failure this must not
// allow: the board would look fine and stop receiving disputes.
//
// The workers' own health servers are bound to loopback unless their address
// is set explicitly. Render resolves a private service's `hostport` to one of
// the ports it sees open, and with four open the console could be handed a
// worker's health port instead of the case service's API.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// defaultChildren is what runs when spine is started with no arguments, which
// is how render.yaml starts it.
var defaultChildren = []string{"/caseservice", "/normalizer", "/evidencebuilder", "/executor"}

// loopbackDefaults keeps the workers' health servers off the network interface
// unless the deployment says otherwise. The ports are each binary's own default.
var loopbackDefaults = map[string]string{
	"NORMALIZER_ADDR":       "127.0.0.1:8084",
	"EVIDENCE_BUILDER_ADDR": "127.0.0.1:8085",
	"EXECUTOR_ADDR":         "127.0.0.1:8086",
}

// shutdownGrace is how long children get to finish after SIGTERM. Render
// allows 30 seconds before it kills the container, so this leaves a margin.
const shutdownGrace = 25 * time.Second

func main() {
	log.SetFlags(0)
	log.SetPrefix("spine: ")

	children := os.Args[1:]
	if len(children) == 0 {
		children = defaultChildren
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	env := withDefaults(os.Environ(), loopbackDefaults)
	if err := run(ctx, children, env, os.Stdout, shutdownGrace); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

// withDefaults returns env with each key in defaults added, unless env sets it
// already (to anything, including empty).
func withDefaults(env []string, defaults map[string]string) []string {
	set := make(map[string]bool, len(env))
	for _, kv := range env {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				set[kv[:i]] = true
				break
			}
		}
	}
	out := append([]string(nil), env...)
	for k, v := range defaults {
		if !set[k] {
			out = append(out, k+"="+v)
		}
	}
	return out
}

// run starts every child and blocks until either ctx is cancelled (a clean
// shutdown, returning nil) or one child exits (returning an error naming it).
// Either way every other child is sent SIGTERM and, after grace, SIGKILL.
func run(ctx context.Context, children []string, env []string, out io.Writer, grace time.Duration) error {
	type exit struct {
		name string
		err  error
	}

	var (
		mu    sync.Mutex // serialises writes to out, so lines never interleave
		procs []*exec.Cmd
		exits = make(chan exit, len(children))
		pipes sync.WaitGroup
	)

	stopAll := func() {
		for _, p := range procs {
			_ = p.Process.Signal(syscall.SIGTERM)
		}
	}

	for _, path := range children {
		cmd := exec.Command(path)
		cmd.Env = env
		r, w := io.Pipe()
		cmd.Stdout = w
		cmd.Stderr = w
		if err := cmd.Start(); err != nil {
			stopAll()
			return fmt.Errorf("start %s: %w", path, err)
		}
		procs = append(procs, cmd)
		log.Printf("started %s (pid %d)", path, cmd.Process.Pid)

		pipes.Add(1)
		go func(name string, r io.Reader) {
			defer pipes.Done()
			sc := bufio.NewScanner(r)
			sc.Buffer(make([]byte, 64*1024), 1024*1024)
			for sc.Scan() {
				mu.Lock()
				fmt.Fprintf(out, "[%s] %s\n", name, sc.Text())
				mu.Unlock()
			}
			// Scan stops on a line longer than the buffer. Keep draining, or
			// the child blocks on its next write and hangs with the pipe full.
			if err := sc.Err(); err != nil {
				mu.Lock()
				fmt.Fprintf(out, "[%s] (spine: output dropped from here: %v)\n", name, err)
				mu.Unlock()
				_, _ = io.Copy(io.Discard, r)
			}
		}(baseName(path), r)

		go func(name string, cmd *exec.Cmd, w *io.PipeWriter) {
			err := cmd.Wait()
			w.Close()
			exits <- exit{name, err}
		}(baseName(path), cmd, w)
	}

	var failure error
	select {
	case <-ctx.Done():
		log.Print("shutting down")
	case e := <-exits:
		failure = fmt.Errorf("%s exited (%v); stopping the rest so the container restarts", e.name, describe(e.err))
		log.Print(failure)
		exits <- e // put it back so the drain below counts it
	}

	stopAll()
	deadline := time.After(grace)
	for remaining := len(procs); remaining > 0; {
		select {
		case <-exits:
			remaining--
		case <-deadline:
			log.Printf("%d child(ren) still running after %s; killing", remaining, grace)
			for _, p := range procs {
				_ = p.Process.Kill()
			}
			deadline = nil // block on exits only; Kill always ends Wait
		}
	}
	pipes.Wait()
	return failure
}

func describe(err error) string {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ProcessState.String()
	}
	if err == nil {
		return "status 0"
	}
	return err.Error()
}

func baseName(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}
