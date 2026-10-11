package copilot

import (
	"os"
	"path/filepath"
	"testing"

	sdk "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

func TestRoleReadsResolveRelativeAndMissingLeavesWithoutSymlinkEscapes(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "existing.go"), []byte("package probe"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "absent.go"), filepath.Join(root, "dangling.go")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path  string
		allow bool
	}{
		{filepath.Join(root, "absent.go"), true},
		{"existing.go", true},
		{"absent.go", true},
		{"", false},
		{filepath.Join(outside, "absent.go"), false},
		{filepath.Join(root, "escape", "absent.go"), false},
		{filepath.Join(root, "dangling.go"), false},
		{"missing-parent/absent.go", false},
		{"../absent.go", false},
	} {
		for _, role := range []string{"CODER", "REVIEWER"} {
			t.Run(role+"/"+tc.path, func(t *testing.T) {
				read := &sdk.PermissionRequestRead{Path: tc.path}
				decision := reviewerPermissionDecision(root, read)
				if role == "CODER" {
					decision = coderPermissionDecision(root, nil, read)
				}
				_, allowed := decision.(*rpc.PermissionDecisionApproveOnce)
				if allowed != tc.allow {
					t.Fatalf("read %q: permission %T, want allowed=%v", tc.path, decision, tc.allow)
				}
			})
		}
	}
}
