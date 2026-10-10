package proc

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/runtime"
)

func collect(t *testing.T, ch <-chan runtime.Event) []runtime.Event {
	t.Helper()
	var out []runtime.Event
	timeout := time.After(15 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-timeout:
			t.Fatal("timed out waiting for session events")
		}
	}
}

func TestEchoReportsOutputThenDone(t *testing.T) {
	p := New("coder", "sh", []string{"-c", "echo hello"}, "")
	sid, err := p.Start(context.Background(), "t-1")
	if err != nil {
		t.Fatal(err)
	}
	ch, err := p.Events(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	evs := collect(t, ch)
	var sawOutput, sawDone bool
	for _, ev := range evs {
		if ev.Type == "output" && ev.Payload == "hello" {
			sawOutput = true
		}
		if ev.Type == "done" {
			sawDone = true
		}
		if ev.Type == "failed" {
			t.Fatalf("unexpected failed: %s", ev.Payload)
		}
	}
	if !sawOutput || !sawDone {
		t.Fatalf("want output+done, got %+v", evs)
	}
}

func TestSendWritesInstructionToWorker(t *testing.T) {
	p := New("coder", "sh", []string{"-c", "IFS= read -r value; printf 'received:%s\\n' \"$value\""}, "")
	sid, err := p.Start(context.Background(), "t-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Send(context.Background(), sid, "inspect the change"); err != nil {
		t.Fatalf("send: %v", err)
	}
	ch, err := p.Events(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	var sawInput bool
	for _, ev := range collect(t, ch) {
		if ev.Type == runtime.EventOutput && ev.Payload == "received:inspect the change" {
			sawInput = true
		}
	}
	if !sawInput {
		t.Fatal("worker did not receive the instruction")
	}
}

func TestNonZeroExitFailsClosed(t *testing.T) {
	p := New("coder", "sh", []string{"-c", "exit 3"}, "")
	sid, err := p.Start(context.Background(), "t-1")
	if err != nil {
		t.Fatal(err)
	}
	ch, err := p.Events(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	var sawFailed, sawDone bool
	for _, ev := range collect(t, ch) {
		if ev.Type == "failed" {
			sawFailed = true
			if !strings.Contains(ev.Payload, "3") {
				t.Fatalf("failed payload should name the exit code, got %q", ev.Payload)
			}
		}
		if ev.Type == "done" {
			sawDone = true
		}
	}
	if !sawFailed || sawDone {
		t.Fatal("non-zero exit must report failed, never done")
	}
}

func TestStopKillsAndNeverDones(t *testing.T) {
	p := New("coder", "sh", []string{"-c", "sleep 30"}, "")
	ctx := context.Background()
	sid, err := p.Start(ctx, "t-1")
	if err != nil {
		t.Fatal(err)
	}
	ch, err := p.Events(ctx, sid)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Stop(ctx, sid); err != nil {
		t.Fatal(err)
	}
	for _, ev := range collect(t, ch) {
		if ev.Type == "done" {
			t.Fatal("stopped session must never become done")
		}
	}
	if err := p.Resume(ctx, sid); err != ErrStoppedSession {
		t.Fatalf("Resume after stop = %v, want ErrStoppedSession", err)
	}
}

func TestGuards(t *testing.T) {
	ctx := context.Background()
	p := New("coder", "sh", []string{"-c", "true"}, "")
	if _, err := p.Start(ctx, ""); err == nil {
		t.Fatal("empty task id must fail")
	}
	if _, err := p.Events(ctx, "nope"); err != ErrUnknownSession {
		t.Fatalf("Events unknown = %v", err)
	}
	if err := p.Send(ctx, "nope", "prompt"); err != ErrUnknownSession {
		t.Fatalf("Send unknown = %v", err)
	}
	if err := p.Stop(ctx, "nope"); err != ErrUnknownSession {
		t.Fatalf("Stop unknown = %v", err)
	}
	if err := p.Resume(ctx, "nope"); err != ErrUnknownSession {
		t.Fatalf("Resume unknown = %v", err)
	}
	unconfigured := New("coder", "", nil, "")
	if _, err := unconfigured.Start(ctx, "t-1"); err == nil {
		t.Fatal("unconfigured worker command must fail closed")
	}
	missing := New("coder", "taxion-no-such-binary", nil, "")
	if _, err := missing.Start(ctx, "t-1"); err == nil {
		t.Fatal("missing worker binary must fail closed")
	}
}
