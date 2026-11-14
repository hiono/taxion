package copilot

import (
	"os"
	"path/filepath"
	"testing"

	sdk "github.com/github/copilot-sdk/go"
	"github.com/hiono/taxion/internal/core"
)

func TestJobEffectForPermissionPreservesObservedEffect(t *testing.T) {
	request := &sdk.PermissionRequestShell{FullCommandText: "go test ./..."}
	worktree := t.TempDir()
	if err := os.MkdirAll(filepath.Join(worktree, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	effect, ok := jobEffectForPermission(worktree, nil, request)
	if !ok || effect.Effect != core.JobEffectValidationExecution || effect.Target != request.FullCommandText ||
		!effect.Inspectable || !effect.SandboxEnforced || effect.Capability != "shell" {
		t.Fatalf("jobEffectForPermission(shell) = (%#v, %t)", effect, ok)
	}
	read := &sdk.PermissionRequestRead{Path: "src/main.go"}
	effect, ok = jobEffectForPermission(worktree, []string{"src/main.go"}, read)
	if !ok || effect.Effect != core.JobEffectRepositoryRead || effect.Target != "src/main.go" || !effect.Tracked ||
		!effect.Inspectable || !effect.SandboxEnforced || effect.Capability != "file.read" {
		t.Fatalf("jobEffectForPermission(read) = (%#v, %t)", effect, ok)
	}
	search := &sdk.PermissionRequestCustomTool{ToolName: ManagedSearchToolName}
	effect, ok = jobEffectForPermission(worktree, nil, search)
	if !ok || effect.Effect != core.JobEffectNetworkAccess || effect.Resource != "external:managed-search" ||
		effect.Capability != "managed-search" || effect.Action != "search" || !effect.Inspectable {
		t.Fatalf("jobEffectForPermission(managed search) = (%#v, %t)", effect, ok)
	}
	unknown := &sdk.PermissionRequestCustomTool{ToolName: "unknown"}
	if _, ok := jobEffectForPermission(worktree, nil, unknown); ok {
		t.Fatal("unknown custom tool must not receive an inferred effect")
	}
}
