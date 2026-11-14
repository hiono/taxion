package openacp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestOpenACPJobOperation(t *testing.T) {
	tests := []struct {
		kind      string
		name      string
		available []string
		want      string
		ok        bool
	}{
		{"read", "read", []string{"view", "edit"}, "repository.read", true},
		{"search", "grep", []string{"view", "edit"}, "repository.read", true},
		{"edit", "edit", []string{"view", "edit"}, "repository.write", true},
		{"execute", "bash", []string{"view", "edit"}, "", false},
		{"fetch", "web", []string{"view", "edit"}, "", false},
		{"edit", "delete", []string{"view", "edit"}, "", false},
		{"read", "read", []string{"edit"}, "", false},
	}
	for _, test := range tests {
		got, ok := openACPJobOperation(test.kind, test.name, test.available)
		if got != test.want || ok != test.ok {
			t.Errorf("openACPJobOperation(%q, %q) = (%q, %t), want (%q, %t)", test.kind, test.name, got, ok, test.want, test.ok)
		}
	}
}

func TestPermissionOutcomeUsesJobAuthorizationAndAllowOnce(t *testing.T) {
	called := false
	adapter := &Adapter{
		config: Config{AvailableTools: []string{"read"}},
		authorizeJobOperation: func(_ context.Context, sessionID, operation, detail string) error {
			called = true
			if sessionID != "session-1" || operation != "repository.read" || detail != "" {
				t.Fatalf("unexpected authorization request: %q %q %q", sessionID, operation, detail)
			}
			return nil
		},
	}
	params := json.RawMessage(`{"sessionId":"session-1","toolCall":{"kind":"read","name":"read"},"options":[{"optionId":"once","kind":"allow_once"},{"optionId":"always","kind":"allow_always"}]}`)
	outcome := adapter.permissionOutcome(params)
	if !called {
		t.Fatal("Job authorization callback was not invoked")
	}
	if outcome["outcome"].(map[string]any)["optionId"] != "once" {
		t.Fatalf("permission outcome = %#v, want allow-once", outcome)
	}
}

func TestPermissionOutcomeRejectsWhenJobAuthorizationFails(t *testing.T) {
	adapter := &Adapter{
		config: Config{AvailableTools: []string{"edit"}},
		authorizeJobOperation: func(context.Context, string, string, string) error {
			return errors.New("role operation denied")
		},
	}
	params := json.RawMessage(`{"sessionId":"session-1","toolCall":{"kind":"edit","name":"edit"},"options":[{"optionId":"reject","kind":"reject_once"},{"optionId":"allow","kind":"allow_once"}]}`)
	outcome := adapter.permissionOutcome(params)
	if outcome["outcome"].(map[string]any)["optionId"] != "reject" {
		t.Fatalf("permission outcome = %#v, want reject-once", outcome)
	}
}
