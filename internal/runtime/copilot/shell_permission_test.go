package copilot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

func shellRequest(text string, paths ...string) *sdk.PermissionRequestShell {
	request := &sdk.PermissionRequestShell{FullCommandText: text, PossiblePaths: paths}
	for _, segment := range splitSegments(text) {
		request.Commands = append(request.Commands, sdk.PermissionRequestShellCommand{Identifier: segment[0]})
		request.CommandSegments = append(request.CommandSegments,
			sdk.PermissionRequestShellCommandSegment{Identifier: segment[0], FullCommandText: segment[1]})
	}
	return request
}

// splitSegments treats ":" as a segment separator in these fixtures.
func splitSegments(text string) [][2]string {
	var out [][2]string
	for _, part := range strings.Split(text, ":") {
		out = append(out, [2]string{strings.Fields(part)[0], part})
	}
	return out
}

func TestCoderShellPermissionAllowsScopedLocalCommands(t *testing.T) {
	worktree := t.TempDir()
	if err := os.Mkdir(filepath.Join(worktree, "build"), 0o755); err != nil {
		t.Fatal(err)
	}
	tools := []string{"view", "edit", "bash"}
	for name, request := range map[string]*sdk.PermissionRequestShell{
		"build":      shellRequest("cmake --build build", filepath.Join(worktree, "build")),
		"test":       shellRequest("ctest --test-dir build", "build"),
		"go test":    shellRequest("go test ./..."),
		"git status": shellRequest("git status --short"),
		"pipeline":   shellRequest("make test:grep FAIL"),
	} {
		t.Run(name, func(t *testing.T) {
			decision := coderPermissionDecision(worktree, tools, nil, nil, request)
			if _, ok := decision.(*rpc.PermissionDecisionApproveOnce); !ok {
				t.Fatalf("decision = %T, want one-time approval", decision)
			}
		})
	}
}

func TestCoderShellPermissionRejectsUnsafeRequests(t *testing.T) {
	worktree := t.TempDir()
	tools := []string{"view", "edit", "bash"}
	outside := t.TempDir()
	withURL := shellRequest("make fetch")
	withURL.PossibleURLs = []sdk.PermissionRequestShellPossibleURL{{URL: "https://example.invalid"}}
	bypass := shellRequest("make test")
	bypass.RequestSandboxBypass = boolPointer(true)
	managed := shellRequest("make test")
	managed.ManagedApprovalRequired = boolPointer(true)
	outsideDirectory := shellRequest("make test")
	outsideDirectory.ResolvedWorkingDirectory = &outside
	unparsed := &sdk.PermissionRequestShell{FullCommandText: "$(cat x)"}
	mismatched := shellRequest("make test")
	mismatched.Commands = append(mismatched.Commands, sdk.PermissionRequestShellCommand{Identifier: "git"})
	for name, request := range map[string]*sdk.PermissionRequestShell{
		"outside path":      shellRequest("cat /etc/passwd", "/etc/passwd"),
		"relative escape":   shellRequest("cat ../secret", "../secret"),
		"git metadata":      shellRequest("rm -rf .git", ".git"),
		"outside directory": outsideDirectory,
		"url":               withURL,
		"sandbox bypass":    bypass,
		"managed approval":  managed,
		"git commit":        shellRequest("git commit -m x"),
		"git push":          shellRequest("git push origin HEAD"),
		"git option first":  shellRequest("git -C . push"),
		"absolute git push": shellRequest("/usr/bin/git push"),
		"gh":                shellRequest("gh pr create"),
		"curl":              shellRequest("curl https://example.invalid"),
		"nested shell":      shellRequest("bash -c true"),
		"env wrapper":       shellRequest("env git push"),
		"sudo":              shellRequest("sudo make install"),
		"pipeline git push": shellRequest("make test:git push"),
		"unparsed":          unparsed,
		"command mismatch":  mismatched,
	} {
		t.Run(name, func(t *testing.T) {
			decision := coderPermissionDecision(worktree, tools, nil, nil, request)
			reject, ok := decision.(*rpc.PermissionDecisionReject)
			if !ok || reject.Feedback == nil || *reject.Feedback == "" {
				t.Fatalf("decision = %#v, want rejection with feedback", decision)
			}
		})
	}
	decision := coderPermissionDecision(worktree, []string{"view", "edit"}, nil, nil, shellRequest("make test"))
	if _, ok := decision.(*rpc.PermissionDecisionReject); !ok {
		t.Fatalf("shell without allowlisted tool = %T, want rejection", decision)
	}
	if _, ok := reviewerPermissionDecision(worktree, nil, nil, shellRequest("make test")).(*rpc.PermissionDecisionReject); !ok {
		t.Fatal("Reviewer shell must stay rejected")
	}
}
