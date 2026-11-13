package copilot

import (
	"os"
	"path/filepath"
	"testing"

	sdk "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
	"github.com/hiono/taxion/internal/core"
)

func TestWorkerProductReadsDoNotRequireManagementAccessOrShell(t *testing.T) {
	root, control := t.TempDir(), t.TempDir()
	path := filepath.Join(root, "product.go")
	if err := os.WriteFile(path, []byte("package product"), 0600); err != nil {
		t.Fatal(err)
	}
	policy := &core.FileAccessPolicy{Level: core.FileAccessCommitted}
	for _, role := range []string{"CODER", "REVIEWER"} {
		t.Run(role, func(t *testing.T) {
			decide := func(request sdk.PermissionRequest) rpc.PermissionDecision {
				if role == "CODER" {
					return coderPermissionDecision(root, []string{"view"}, policy, []string{"product.go"}, request)
				}
				return reviewerPermissionDecision(root, policy, []string{"product.go"}, request)
			}
			if _, ok := decide(&sdk.PermissionRequestRead{Path: path}).(*rpc.PermissionDecisionApproveOnce); !ok {
				t.Fatal("approved product source cannot be read")
			}
			for _, request := range []sdk.PermissionRequest{
				&sdk.PermissionRequestRead{Path: control},
				&sdk.PermissionRequestRead{Path: filepath.Join(control, "session.json")},
				&sdk.PermissionRequestShell{FullCommandText: "taxion ready"},
				&sdk.PermissionRequestWrite{FileName: filepath.Join(root, "unapproved.go")},
			} {
				if _, ok := decide(request).(*rpc.PermissionDecisionReject); !ok {
					t.Fatal("role separation expanded access to management state, shell or unapproved files", request)
				}
			}
		})
	}
}

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
				decision := reviewerPermissionDecision(root, nil, nil, read)
				if role == "CODER" {
					decision = coderPermissionDecision(root, nil, nil, nil, read)
				}
				_, allowed := decision.(*rpc.PermissionDecisionApproveOnce)
				if allowed != tc.allow {
					t.Fatalf("read %q: permission %T, want allowed=%v", tc.path, decision, tc.allow)
				}
			})
		}
	}
}
