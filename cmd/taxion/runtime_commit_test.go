package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime/copilot"
)

func TestRuntimeCommitArgumentContract(t *testing.T) {
	for _, tc := range []struct {
		action string
		flags  []string
		want   string
	}{
		{"commit", nil, "--message-file is required"},
		{"send", []string{"--prompt-file", "prompt", "--message-file", "message"}, "--message-file applies only to commit"},
		{"stop", []string{"--scanner", "betterleaks"}, "--scanner applies only to commit"},
		{"commit", []string{"--message-file", "message", "--timeout", "0s"}, "--timeout must be positive"},
		{"start", []string{"--managed-stage"}, "--managed-stage requires --managed-commit"},
	} {
		t.Run(tc.action+" "+tc.want, func(t *testing.T) {
			args := append([]string{tc.action, "--dispatch", "dispatch", "--system-prompt-file", "system"}, tc.flags...)
			err := runtimeCmd(args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("runtime argument error = %v, want %s", err, tc.want)
			}

		})
	}
	if !strings.Contains(usage, "runtime commit") {
		t.Fatal("runtime commit is absent from help")
	}
}

func TestDispatchWorkerManagedCommitIsExplicitAndRoleBound(t *testing.T) {
	dispatch := core.GraphDispatch{
		ID: "dispatch", Recipient: "CODER",
		Instruction: core.DispatchInstruction{CommitMessagePolicy: core.CommitMessagePolicy},
	}
	config := copilot.Config{
		Role: "CODER", SystemPrompt: "approved role", CLIPath: "/bin/true",
		WorkingDirectory: t.TempDir(), BaseDirectory: t.TempDir(),
	}
	disabled, err := newDispatchWorker(context.Background(), nil, dispatch, config, false, false, "missing")
	if err != nil {
		t.Fatal("disabled managed commit unexpectedly requires a scanner", err)
	}
	defer disabled.Close()
	enabled, err := newDispatchWorker(context.Background(), nil, dispatch, config, true, false, "/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	defer enabled.Close()
	stageEnabled, err := newDispatchWorker(context.Background(), nil, dispatch, config, true, true, "/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	defer stageEnabled.Close()
	if stageEnabled.ConfigHash() == enabled.ConfigHash() {
		t.Fatal("stage permission did not change session identity")
	}
	if _, err := newDispatchWorker(context.Background(), nil, dispatch, config, false, true, "/bin/true"); err == nil {
		t.Fatal("stage tool enabled without managed commit")
	}
	if disabled.ConfigHash() == enabled.ConfigHash() {
		t.Fatal("managed commit opt-in did not change session identity")
	}
	for _, role := range []string{"REVIEWER", "PLANNER"} {
		dispatch.Recipient = role
		if _, err := newDispatchWorker(context.Background(), nil, dispatch, config, true, false, "/bin/true"); err == nil {
			t.Fatal("non-Coder gained managed commit")
		}
	}
	dispatch.Recipient = "CODER"
	dispatch.Instruction.CommitMessagePolicy = ""
	if _, err := newDispatchWorker(context.Background(), nil, dispatch, config, true, false, "/bin/true"); err == nil {
		t.Fatal("legacy Dispatch gained managed commit")
	}
	dispatch.Instruction.CommitMessagePolicy = core.CommitMessagePolicy
	if _, err := newDispatchWorker(context.Background(), nil, dispatch, config, true, false, "taxion-missing-betterleaks-test"); err == nil {
		t.Fatal("missing scanner accepted for managed commit")
	}
	path := filepath.Join(t.TempDir(), "scanner")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	before, err := newDispatchWorker(context.Background(), nil, dispatch, config, true, false, path)
	if err != nil {
		t.Fatal(err)
	}
	defer before.Close()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	after, err := newDispatchWorker(context.Background(), nil, dispatch, config, true, false, path)
	if err != nil || before.ConfigHash() != after.ConfigHash() {
		t.Fatal("missing scanner prevents attaching with the saved identity for safe stop", err)
	}
	defer after.Close()
	if err := workflowCmd([]string{"run", "--id", "flow", "--scanner", "/bin/true"}); err == nil ||
		!strings.Contains(err.Error(), "--scanner requires --managed-commit") {
		t.Fatal("workflow silently ignored scanner without managed commit", err)
	}
}
