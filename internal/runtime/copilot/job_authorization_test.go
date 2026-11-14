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
	effect, ok := jobEffectForPermission(worktree, request)
	if !ok || effect.Effect != core.JobEffectValidationExecution || effect.Target != request.FullCommandText {
		t.Fatalf("jobEffectForPermission(shell) = (%#v, %t)", effect, ok)
	}
	read := &sdk.PermissionRequestRead{Path: "src/main.go"}
	effect, ok = jobEffectForPermission(worktree, read)
	if !ok || effect.Effect != core.JobEffectRepositoryRead || effect.Target != "src/main.go" {
		t.Fatalf("jobEffectForPermission(read) = (%#v, %t)", effect, ok)
	}
	unknown := &sdk.PermissionRequestCustomTool{ToolName: "unknown"}
	if _, ok := jobEffectForPermission(worktree, unknown); ok {
		t.Fatal("unknown custom tool must not receive an inferred effect")
	}
}
