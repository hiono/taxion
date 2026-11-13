package copilot

import (
	"os"
	"path/filepath"
	"testing"

	sdk "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
	"github.com/hiono/taxion/internal/core"
)

func TestFileAccessLevelsConstrainRoleFilePermissions(t *testing.T) {
	worktree := t.TempDir()
	for name, body := range map[string]string{
		"tracked.go":        "package tracked",
		"untracked.go":      "package untracked",
		"nested/tracked.go": "package nested",
	} {
		path := filepath.Join(worktree, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	tracked := []string{"tracked.go", "nested/tracked.go"}
	tests := []struct {
		name      string
		policy    *core.FileAccessPolicy
		path      string
		directory bool
		allowed   bool
	}{
		{"exact listed file", &core.FileAccessPolicy{Level: core.FileAccessExact, Paths: []string{"untracked.go"}}, "untracked.go", false, true},
		{"exact excludes sibling", &core.FileAccessPolicy{Level: core.FileAccessExact, Paths: []string{"tracked.go"}}, "untracked.go", false, false},
		{"exact denies directory browsing", &core.FileAccessPolicy{Level: core.FileAccessExact, Paths: []string{"tracked.go"}}, ".", true, false},
		{"committed tracked file", &core.FileAccessPolicy{Level: core.FileAccessCommitted}, "tracked.go", false, true},
		{"committed denies untracked file", &core.FileAccessPolicy{Level: core.FileAccessCommitted}, "untracked.go", false, false},
		{"committed allows tracked directory listing", &core.FileAccessPolicy{Level: core.FileAccessCommitted}, "nested", true, true},
		{"all includes untracked file", &core.FileAccessPolicy{Level: core.FileAccessAll}, "untracked.go", false, true},
		{"all includes worktree root", &core.FileAccessPolicy{Level: core.FileAccessAll}, ".", true, true},
		{"all excludes git metadata", &core.FileAccessPolicy{Level: core.FileAccessAll}, ".git", false, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target := filepath.Join(worktree, filepath.FromSlash(test.path))
			allowed := fileAccessAllows(worktree, target, test.policy, tracked, test.directory)
			if allowed != test.allowed {
				t.Fatalf("file access to %q = %v, want %v", test.path, allowed, test.allowed)
			}
		})
	}
	outside := filepath.Join(t.TempDir(), "outside.go")
	if fileAccessAllows(worktree, outside, &core.FileAccessPolicy{Level: core.FileAccessAll}, tracked, false) {
		t.Fatal("all allowed access outside the assigned worktree")
	}
}

func TestCoderPermissionAppliesFileAccessAndToolAllowlistSeparately(t *testing.T) {
	worktree := t.TempDir()
	tracked := filepath.Join(worktree, "tracked.go")
	if err := os.WriteFile(tracked, []byte("package tracked"), 0600); err != nil {
		t.Fatal(err)
	}
	policy := &core.FileAccessPolicy{Level: core.FileAccessExact, Paths: []string{"tracked.go"}}
	write := coderPermissionDecision(worktree, nil, policy, nil, &sdk.PermissionRequestWrite{FileName: tracked})
	if _, ok := write.(*rpc.PermissionDecisionApproveOnce); !ok {
		t.Fatalf("explicit path write = %T, want one-time approval", write)
	}
	unlisted := coderPermissionDecision(worktree, nil, policy, nil,
		&sdk.PermissionRequestWrite{FileName: filepath.Join(worktree, "other.go")})
	if _, ok := unlisted.(*rpc.PermissionDecisionReject); !ok {
		t.Fatalf("unlisted file write = %T, want rejection", unlisted)
	}
	custom := coderPermissionDecision(worktree, []string{"taxion_test_tool"}, policy, nil,
		&sdk.PermissionRequestCustomTool{ToolName: "taxion_test_tool"})
	if _, ok := custom.(*rpc.PermissionDecisionApproveOnce); !ok {
		t.Fatalf("allowlisted SDK tool = %T, want one-time approval", custom)
	}
	unlistedTool := coderPermissionDecision(worktree, nil, policy, nil,
		&sdk.PermissionRequestCustomTool{ToolName: "taxion_test_tool"})
	if _, ok := unlistedTool.(*rpc.PermissionDecisionReject); !ok {
		t.Fatalf("unlisted SDK tool = %T, want rejection", unlistedTool)
	}
}

func TestReviewerUsesFileAccessButRemainsReadOnly(t *testing.T) {
	worktree := t.TempDir()
	tracked := filepath.Join(worktree, "tracked.go")
	if err := os.WriteFile(tracked, []byte("package tracked"), 0600); err != nil {
		t.Fatal(err)
	}
	policy := &core.FileAccessPolicy{Level: core.FileAccessCommitted}
	allowedRead := reviewerPermissionDecision(worktree, policy, []string{"tracked.go"},
		&sdk.PermissionRequestRead{Path: tracked})
	if _, ok := allowedRead.(*rpc.PermissionDecisionApproveOnce); !ok {
		t.Fatalf("committed Reviewer read = %T, want one-time approval", allowedRead)
	}
	untrackedRead := reviewerPermissionDecision(worktree, policy, []string{"tracked.go"},
		&sdk.PermissionRequestRead{Path: filepath.Join(worktree, "untracked.go")})
	if _, ok := untrackedRead.(*rpc.PermissionDecisionReject); !ok {
		t.Fatalf("untracked Reviewer read = %T, want rejection", untrackedRead)
	}
	write := reviewerPermissionDecision(worktree, &core.FileAccessPolicy{Level: core.FileAccessAll}, nil,
		&sdk.PermissionRequestWrite{FileName: tracked})
	if _, ok := write.(*rpc.PermissionDecisionReject); !ok {
		t.Fatalf("Reviewer write = %T, want rejection", write)
	}
	shell := reviewerPermissionDecision(worktree, &core.FileAccessPolicy{Level: core.FileAccessAll}, nil,
		&sdk.PermissionRequestShell{FullCommandText: "cat tracked.go"})
	if _, ok := shell.(*rpc.PermissionDecisionReject); !ok {
		t.Fatalf("Reviewer shell request = %T, want rejection", shell)
	}
}

func TestConfigHashBindsFileAccessPolicyAndTrackedSnapshot(t *testing.T) {
	adapter := Adapter{config: Config{
		Role: "CODER", FileAccess: &core.FileAccessPolicy{Level: core.FileAccessCommitted},
		TrackedFiles: []string{"src/app.go"},
	}}
	hash := adapter.ConfigHash()
	changedPolicy := Adapter{config: adapter.config}
	changedPolicy.config.FileAccess = &core.FileAccessPolicy{Level: core.FileAccessAll}
	if changedPolicy.ConfigHash() == hash {
		t.Fatal("file-access level was omitted from runtime session binding")
	}
	changedSnapshot := Adapter{config: adapter.config}
	changedSnapshot.config.TrackedFiles = []string{"src/other.go"}
	if changedSnapshot.ConfigHash() == hash {
		t.Fatal("committed path snapshot was omitted from runtime session binding")
	}
}
