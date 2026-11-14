package openacp

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
)

func openACPTestWorktree(t *testing.T) string {
	t.Helper()
	worktree := t.TempDir()
	if err := os.MkdirAll(filepath.Join(worktree, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "src", "main.go"), []byte("package main"), 0600); err != nil {
		t.Fatal(err)
	}
	return worktree
}

func TestOpenACPJobEffectRequiresTarget(t *testing.T) {
	worktree := t.TempDir()
	if err := os.MkdirAll(filepath.Join(worktree, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "src", "main.go"), []byte("package main"), 0600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		kind, name, input string
		want              core.JobEffect
		ok                bool
	}{
		{"read", "read", `{"path":"src/main.go"}`, core.JobEffectRepositoryRead, true},
		{"edit", "edit", `{"filePath":"src/main.go"}`, core.JobEffectRepositoryWrite, true},
		{"search", "grep", `{"query":"secret"}`, "", false},
		{"read", "read", `{}`, "", false},
		{"read", "read", `{"uri":"file:///etc/passwd"}`, "", false},
		{"read", "read", `{"path":"src/../src/main.go"}`, "", false},
		{"read", "read", `{"path":"src/main.go","uri":"file:///etc/passwd"}`, "", false},
		{"read", "read", `{"path":42}`, "", false},
		{"read", "read", `{"path":"src/main.go","path":"file:///etc/passwd"}`, "", false},
		{"execute", "bash", `{}`, "", false},
	}
	for _, test := range tests {
		got, ok := openACPJobEffect(worktree, test.kind, test.name, json.RawMessage(test.input), []string{"read", "edit", "grep"}, []string{"src/main.go"}, false)
		if ok != test.ok || got.Effect != test.want {
			t.Errorf("openACPJobEffect(%q,%q)=(%#v,%t), want (%q,%t)", test.kind, test.name, got, ok, test.want, test.ok)
		}
	}
	localURI := (&url.URL{Scheme: "file", Path: filepath.Join(worktree, "src", "main.go")}).String()
	if _, ok := openACPJobEffect(worktree, "read", "read", json.RawMessage(`{"uri":"`+localURI+`"}`), []string{"read"}, []string{"src/main.go"}, false); !ok {
		t.Fatal("local file URI inside the assigned worktree must be accepted")
	}
	allAccess, ok := openACPJobEffect(worktree, "edit", "edit", json.RawMessage(`{"path":"new.go"}`), []string{"edit"}, nil, true)
	if !ok || allAccess.Target != "new.go" {
		t.Fatalf("FileAccessAll must permit a new in-worktree target: %#v %t", allAccess, ok)
	}
	if _, ok := openACPJobEffect(worktree, "edit", "edit", json.RawMessage(`{"path":".git/config"}`), []string{"edit"}, nil, true); ok {
		t.Fatal("FileAccessAll must not expose Git metadata")
	}
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(worktree, "src", "escape.go")); err != nil {
		t.Fatal(err)
	}
	if _, ok := openACPJobEffect(worktree, "read", "read", json.RawMessage(`{"path":"src/escape.go"}`), []string{"read"}, nil, true); ok {
		t.Fatal("FileAccessAll must not follow a symlink outside the assigned worktree")
	}
}

func TestPermissionOutcomeUsesJobEffectAndAllowOnce(t *testing.T) {
	called := false
	adapter := &Adapter{
		config: Config{WorkingDirectory: openACPTestWorktree(t), AvailableTools: []string{"read"}, FileAccess: &core.FileAccessPolicy{Level: core.FileAccessExact, Paths: []string{"src/main.go"}}},
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
		config: Config{WorkingDirectory: openACPTestWorktree(t), AvailableTools: []string{"edit"}, FileAccess: &core.FileAccessPolicy{Level: core.FileAccessExact, Paths: []string{"src/main.go"}}},
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
