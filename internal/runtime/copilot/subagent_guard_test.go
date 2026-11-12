package copilot

import (
	"testing"

	sdk "github.com/github/copilot-sdk/go"
)

func TestSubagentGuardRequiresBothTrackingStreamsToFinish(t *testing.T) {
	guard := newSubagentGuard(true)
	invocation := sdk.HookInvocation{SessionID: "root"}
	handle := &managedSession{subagents: guard}
	resumable := false

	if _, err := handle.onSubagentStart(sdk.SubagentStartHookInput{
		SessionID: "root",
		AgentID:   "child",
	}, invocation); err != nil {
		t.Fatal(err)
	}
	guard.observeEvent(sdk.SessionEvent{
		ID:   "start",
		Data: &sdk.SubagentStartedData{ToolCallID: "call", Resumable: &resumable},
	})
	if err := guard.check(); err == nil {
		t.Fatal("active child was not blocked")
	}

	if _, err := handle.onSubagentStop(sdk.SubagentStopHookInput{
		SessionID: "root",
		AgentID:   "child",
	}, invocation); err != nil {
		t.Fatal(err)
	}
	if err := guard.check(); err == nil {
		t.Fatal("Hook completion concealed active Lifecycle child")
	}

	guard.observeEvent(sdk.SessionEvent{
		ID:   "complete",
		Data: &sdk.SubagentCompletedData{ToolCallID: "call"},
	})
	if err := guard.check(); err != nil {
		t.Fatalf("both tracking streams clear: %v", err)
	}
}

func TestSubagentGuardRejectsBrokenCorrelations(t *testing.T) {
	t.Run("missing agent id", func(t *testing.T) {
		guard := newSubagentGuard(true)
		guard.hookStart(
			sdk.SubagentStartHookInput{SessionID: "root"},
			sdk.HookInvocation{SessionID: "root"},
		)
		if guard.check() == nil {
			t.Fatal("missing AgentID accepted")
		}
	})
	t.Run("unknown hook stop", func(t *testing.T) {
		guard := newSubagentGuard(true)
		guard.hookStop(
			sdk.SubagentStopHookInput{SessionID: "root", AgentID: "unknown"},
			sdk.HookInvocation{SessionID: "root"},
		)
		if guard.check() == nil {
			t.Fatal("unknown Hook stop accepted")
		}
	})
	t.Run("missing resumable", func(t *testing.T) {
		guard := newSubagentGuard(true)
		guard.observeEvent(sdk.SessionEvent{
			ID:   "start",
			Data: &sdk.SubagentStartedData{ToolCallID: "call"},
		})
		if guard.check() == nil {
			t.Fatal("unknown Resumable accepted")
		}
	})
	t.Run("completion without start", func(t *testing.T) {
		guard := newSubagentGuard(true)
		guard.observeEvent(sdk.SessionEvent{
			ID:   "complete",
			Data: &sdk.SubagentCompletedData{ToolCallID: "missing"},
		})
		if guard.check() == nil {
			t.Fatal("unmatched completion accepted")
		}
	})
	t.Run("duplicate start", func(t *testing.T) {
		guard := newSubagentGuard(true)
		resumable := false
		for _, id := range []string{"one", "two"} {
			guard.observeEvent(sdk.SessionEvent{
				ID:   id,
				Data: &sdk.SubagentStartedData{ToolCallID: "same", Resumable: &resumable},
			})
		}
		if guard.check() == nil {
			t.Fatal("duplicate start accepted")
		}
	})
}

func TestSubagentGuardLifecycleEventIDsAreContentBound(t *testing.T) {
	t.Run("identical replay is idempotent", func(t *testing.T) {
		guard := newSubagentGuard(true)
		resumable := false
		event := sdk.SessionEvent{
			ID: "same",
			Data: &sdk.SubagentStartedData{ToolCallID: "call", Resumable: &resumable},
		}
		guard.observeEvent(event)
		guard.observeEvent(event)
		guard.observeEvent(sdk.SessionEvent{
			ID: "finish",
			Data: &sdk.SubagentCompletedData{ToolCallID: "call"},
		})
		if err := guard.check(); err != nil {
			t.Fatalf("identical event replay was not idempotent: %v", err)
		}
	})

	t.Run("conflicting replay fails closed", func(t *testing.T) {
		guard := newSubagentGuard(true)
		resumable := false
		guard.observeEvent(sdk.SessionEvent{
			ID: "same",
			Data: &sdk.SubagentStartedData{ToolCallID: "first", Resumable: &resumable},
		})
		guard.observeEvent(sdk.SessionEvent{
			ID: "same",
			Data: &sdk.SubagentStartedData{ToolCallID: "different", Resumable: &resumable},
		})
		guard.observeEvent(sdk.SessionEvent{
			ID: "finish",
			Data: &sdk.SubagentCompletedData{ToolCallID: "first"},
		})
		if guard.check() == nil {
			t.Fatal("conflicting event ID replay was accepted")
		}
	})
}

func TestSubagentGuardRebuildExcludesOnlyConfirmedNonresumableChildren(t *testing.T) {
	tests := []struct {
		name      string
		resumable *bool
		blocked   bool
	}{
		{name: "nonresumable", resumable: boolPointer(false)},
		{name: "resumable", resumable: boolPointer(true), blocked: true},
		{name: "unknown", resumable: nil, blocked: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			guard := newSubagentGuard(false)
			guard.beginRebuild()
			guard.rebuild([]sdk.SessionEvent{{
				ID:   "start",
				Data: &sdk.SubagentStartedData{ToolCallID: "call", Resumable: test.resumable},
			}})
			err := guard.check()
			if test.blocked && err == nil {
				t.Fatal("resumable or unknown child accepted")
			}
			if !test.blocked && err != nil {
				t.Fatalf("confirmed nonresumable child blocked: %v", err)
			}
		})
	}
}
