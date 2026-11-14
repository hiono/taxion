package openacp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
)

func TestOpenACPJobEffectRequiresTarget(t *testing.T) {
	tests := []struct {
		kind, name, input string
		want             core.JobEffect
		ok bool
	}{
		{"read", "read", `{"path":"src/main.go"}`, core.JobEffectRepositoryRead, true},
		{"edit", "edit", `{"filePath":"src/main.go"}`, core.JobEffectRepositoryWrite, true},
		{"search", "grep", `{"query":"secret"}`, "", false},
		{"read", "read", `{}`, "", false},
		{"execute", "bash", `{}`, "", false},
	}
	for _, test := range tests {
		got, ok := openACPJobEffect(test.kind, test.name, json.RawMessage(test.input), []string{"read", "edit", "grep"}, []string{"src/main.go"}, false)
		if ok != test.ok || got.Effect != test.want {
			t.Errorf("openACPJobEffect(%q,%q)=(%#v,%t), want (%q,%t)", test.kind, test.name, got, ok, test.want, test.ok)
		}
	}
	allAccess, ok := openACPJobEffect("edit", "edit", json.RawMessage(`{"path":"new.go"}`), []string{"edit"}, nil, true)
	if !ok || allAccess.Target != "new.go" {
		t.Fatalf("FileAccessAll must permit a new in-worktree target: %#v %t", allAccess, ok)
	}
	if _, ok := openACPJobEffect("edit", "edit", json.RawMessage(`{"path":".git/config"}`), []string{"edit"}, nil, true); ok {
		t.Fatal("FileAccessAll must not expose Git metadata")
	}
}

func TestPermissionOutcomeUsesJobEffectAndAllowOnce(t *testing.T) {
	called := false
	adapter := &Adapter{
		config: Config{AvailableTools: []string{"read"}},
		authorizeJobEffect: func(_ context.Context, sessionID string, request runtime.JobEffectRequest) error {
			called = true
			if sessionID != "session-1" || request.Effect != core.JobEffectRepositoryRead || request.Target != "src/main.go" {
				t.Fatalf("unexpected authorization request: %q %#v", sessionID, request)
			}
			return nil
		},
	}
	params := json.RawMessage(`{"sessionId":"session-1","toolCall":{"kind":"read","name":"read","rawInput":{"path":"src/main.go"}},"options":[{"optionId":"once","kind":"allow_once"},{"optionId":"always","kind":"allow_always"}]}`)
	outcome := adapter.permissionOutcome(params)
	if !called {
		t.Fatal("Job effect authorization callback was not invoked")
	}
	if outcome["optionId"] != "once" {
		t.Fatalf("permission outcome = %#v, want allow-once", outcome)
	}
}

func TestPermissionOutcomeRejectsWhenJobEffectAuthorizationFails(t *testing.T) {
	adapter := &Adapter{
		config: Config{AvailableTools: []string{"edit"}},
		authorizeJobEffect: func(context.Context, string, runtime.JobEffectRequest) error {
			return errors.New("effect denied")
		},
	}
	params := json.RawMessage(`{"sessionId":"session-1","toolCall":{"kind":"edit","name":"edit","rawInput":{"path":"src/main.go"}},"options":[{"optionId":"reject","kind":"reject_once"},{"optionId":"allow","kind":"allow_once"}]}`)
	outcome := adapter.permissionOutcome(params)
	if outcome["optionId"] != "reject" {
		t.Fatalf("permission outcome = %#v, want reject-once", outcome)
	}
}
