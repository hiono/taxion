package controller

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func TestManagedCommitRespectsReviewFlowCeilings(t *testing.T) {
	for _, failure := range []string{"", "deadline", "stopped", "tokens", "unknown-usage"} {
		t.Run(failure, func(t *testing.T) {
			db, ledger, request := reviewFlowFixture(t)
			flow, err := StartReviewFlow(db, ledger, request)
			if err != nil {
				t.Fatal(err)
			}
			root, id := flow.Contract.Worktree, flow.DispatchIDs[0]
			control, err := New(db, &fakeRuntime{role: "CODER", hash: "config", worktree: root, sessionID: "session"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := control.Start(t.Context(), id); err != nil {
				t.Fatal(err)
			}
			if err := control.Send(t.Context(), id, "execute"); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(root, "internal"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "internal", "app.go"), []byte("package app"), 0600); err != nil {
				t.Fatal(err)
			}
			gitTest(t, root, "add", "internal/app.go")
			if failure == "tokens" || failure == "unknown-usage" {
				input, output := flow.MaxTokens, int64(0)
				usage := core.DispatchUsage{Model: "test", InputTokens: &input, OutputTokens: &output}
				if failure == "unknown-usage" {
					usage.InputTokens = nil
				}
				if err := control.RecordUsage(id, usage); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.UpdateWorkspace(func(state *store.Workspace) error {
				if failure == "deadline" {
					state.Graph.ReviewFlows[0].StartedAt = time.Now().Add(-2 * time.Minute)
					state.Graph.ReviewFlows[0].Deadline = time.Now().Add(-time.Minute)
				}
				if failure == "stopped" {
					state.Graph.ReviewFlows[0].Status = "STOPPED"
					state.Graph.ReviewFlows[0].Reason = "human stop"
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			scanner := filepath.Join(t.TempDir(), "scanner")
			if err := os.WriteFile(scanner, []byte("#!/bin/sh\nif [ \"$1\" = version ]; then printf '1.9.0\\n'; exit 0; fi\ncat >/dev/null\nprintf '[]'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			head := gitTest(t, root, "rev-parse", "HEAD")
			sha, err := control.CommitForSession(t.Context(), id, "session",
				"feat(app): implement approved change\n\nintent(app): satisfy the authorized criterion\n", scanner)
			if failure == "" {
				if err != nil || sha == "" {
					t.Fatal("active flow could not commit", err)
				}
			} else if err == nil || sha != "" || !strings.Contains(err.Error(), "review flow") ||
				gitTest(t, root, "rev-parse", "HEAD") != head {
				t.Fatal("flow ceiling failed to stop managed commit", failure, err)
			}
		})
	}
}
