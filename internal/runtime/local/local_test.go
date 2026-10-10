package local

import (
	"context"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
)

func TestOrderedExecution(t *testing.T) {
	ctx := context.Background()
	a := New("coder", "plan", "execute", "verify")
	sid, err := a.Start(ctx, "t1")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := a.CompleteStep(ctx, sid, "execute"); err != core.ErrOutOfOrder {
		t.Fatalf("expected CHK-002 out-of-order, got %v", err)
	}
	for _, step := range []string{"plan", "execute", "verify"} {
		if err := a.CompleteStep(ctx, sid, step); err != nil {
			t.Fatalf("step %s: %v", step, err)
		}
	}
	var types []string
	for ev := range mustEvents(t, ctx, a, sid) {
		types = append(types, ev.Type)
	}
	if len(types) != 4 || types[3] != "done" {
		t.Fatalf("expected 3 checkpoint + done events, got %v", types)
	}
}

func TestSendRecordsInputWithoutClaimingWorkerOutput(t *testing.T) {
	ctx := context.Background()
	a := New("coder", "verify")
	sid, err := a.Start(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Send(ctx, sid, "inspect the change"); err != nil {
		t.Fatal(err)
	}
	events := mustEvents(t, ctx, a, sid)
	event := <-events
	if event.Type != runtime.EventInput || event.Payload != "inspect the change" {
		t.Fatalf("unexpected input event: %+v", event)
	}
	if err := a.Send(ctx, sid, " "); err == nil {
		t.Fatal("whitespace-only prompt must be rejected")
	}
}

func TestFailClosed(t *testing.T) {
	ctx := context.Background()
	a := New("reviewer", "plan", "verify")
	if _, err := a.Start(ctx, ""); err == nil {
		t.Fatal("empty task id must fail")
	}
	sid, _ := a.Start(ctx, "t1")
	bogus := "reviewer-0-0"
	for _, err := range []error{
		a.Send(ctx, bogus, "prompt"),
		a.CompleteStep(ctx, bogus, "plan"),
		a.Resume(ctx, bogus),
		a.Stop(ctx, bogus),
	} {
		if err != ErrUnknownSession {
			t.Fatalf("expected unknown-session error, got %v", err)
		}
	}
	if _, err := a.Events(ctx, bogus); err != ErrUnknownSession {
		t.Fatalf("expected unknown-session error, got %v", err)
	}
	if err := a.Stop(ctx, sid); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := a.CompleteStep(ctx, sid, "plan"); err != ErrStoppedSession {
		t.Fatalf("expected stopped-session error, got %v", err)
	}
	if err := a.Resume(ctx, sid); err != ErrStoppedSession {
		t.Fatalf("expected stopped-session error, got %v", err)
	}
	if err := a.Send(ctx, sid, "prompt"); err != ErrStoppedSession {
		t.Fatalf("expected stopped-session error, got %v", err)
	}
}

func TestRoleIsolation(t *testing.T) {
	ctx := context.Background()
	coder := New("coder", "plan")
	reviewer := New("reviewer", "plan")
	csid, _ := coder.Start(ctx, "t1")
	if err := reviewer.Resume(ctx, csid); err != ErrUnknownSession {
		t.Fatalf("coder session must be unknown to reviewer adapter: %v", err)
	}
}

func mustEvents(t *testing.T, ctx context.Context, a *Adapter, sid string) <-chan runtime.Event {
	t.Helper()
	ch, err := a.Events(ctx, sid)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	return ch
}
