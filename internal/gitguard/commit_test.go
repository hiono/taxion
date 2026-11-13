package gitguard

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
)

const managedMessage = "feat(app): implement approved change\n\nintent(app): satisfy the authorized criterion\n"

func commitFixture(t *testing.T) (string, core.GraphDispatch) {
	t.Helper()
	root := t.TempDir()
	gitTest(t, root, "init", "-b", "work/task")
	gitTest(t, root, "config", "user.name", "Test")
	gitTest(t, root, "config", "user.email", "test@example.com")
	gitTest(t, root, "remote", "add", "origin", "https://github.com/owner/repo.git")
	writeTest(t, root, "baseline.txt", "baseline")
	gitTest(t, root, "add", "baseline.txt")
	gitTest(t, root, "commit", "-qm", "baseline")
	head := strings.TrimSpace(gitTest(t, root, "rev-parse", "HEAD"))
	writeTest(t, root, "internal/source.txt", "approved content")
	gitTest(t, root, "add", "internal/source.txt")
	return root, core.GraphDispatch{
		Recipient: "CODER", Repository: "owner/repo", ExecutionStartSHA: head,
		Instruction: core.DispatchInstruction{
			Worktree: root, Branch: "work/task", BaselineSHA: head,
			CommitMessagePolicy: core.CommitMessagePolicy, AuthorizedActions: []string{"internal"},
		},
	}
}

func TestManagedCommitPublishesExactStagedTree(t *testing.T) {
	for _, split := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "split"}[split], func(t *testing.T) {
			root, dispatch := commitFixture(t)
			if split {
				gitTest(t, root, "update-index", "--split-index")
			}
			expected := strings.TrimSpace(gitTest(t, root, "write-tree"))
			writeTest(t, root, "internal/source.txt", "unstaged content")
			before := gitTest(t, root, "ls-files", "--stage", "-z")
			indexPath := filepath.Join(root, ".git", "index")
			indexBytes, err := os.ReadFile(indexPath)
			if err != nil {
				t.Fatal(err)
			}
			// Controller protection is direct, not an installed hook dependency.
			hooks := t.TempDir()
			if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
				t.Fatal(err)
			}
			gitTest(t, root, "config", "core.hooksPath", hooks)
			t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "missing"))
			t.Setenv("GIT_INDEX_FILE", filepath.Join(t.TempDir(), "unrelated"))
			sha, err := Commit(context.Background(), dispatch, managedMessage, fakeScanner(t, "clean"))
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("GIT_DIR", filepath.Join(root, ".git"))
			t.Setenv("GIT_INDEX_FILE", filepath.Join(root, ".git", "index"))
			if strings.TrimSpace(gitTest(t, root, "rev-parse", "HEAD")) != sha ||
				strings.TrimSpace(gitTest(t, root, "rev-parse", sha+"^{tree}")) != expected {
				t.Fatal("published tree differs from scanned snapshot")
			}
			if before != gitTest(t, root, "ls-files", "--stage", "-z") {
				t.Fatal("managed commit changed staged entries")
			}
			afterBytes, err := os.ReadFile(indexPath)
			if err != nil || string(indexBytes) != string(afterBytes) {
				t.Fatal("managed commit changed real index bytes", err)
			}
			if strings.TrimSpace(gitTest(t, root, "rev-parse", sha+"^")) != dispatch.ExecutionStartSHA {
				t.Fatal("managed commit has an unexpected parent")
			}
			if strings.TrimSpace(gitTest(t, root, "show", sha+":internal/source.txt")) != "approved content" {
				t.Fatal("unstaged content entered commit")
			}
		})
	}
}

func TestManagedCommitRefusesUnsafeStates(t *testing.T) {
	for _, kind := range []string{"path", "secret", "message-secret", "message", "scope", "forbidden",
		"role", "branch", "origin", "start", "scanner", "index-lock", "merge", "empty", "prior-message", "signing"} {
		t.Run(kind, func(t *testing.T) {
			root, dispatch := commitFixture(t)
			message, scanner := managedMessage, fakeScanner(t, "conditional")
			switch kind {
			case "path":
				writeTest(t, root, ".env", "fake-private-payload")
				gitTest(t, root, "add", ".env")
			case "secret":
				writeTest(t, root, "internal/source.txt", "fake-private-payload")
				gitTest(t, root, "add", "internal/source.txt")
			case "message-secret":
				message += "constraint(app): fake-private-payload\n"
			case "message":
				message = "invalid"
			case "scope":
				writeTest(t, root, "outside.txt", "outside")
				gitTest(t, root, "add", "outside.txt")
			case "forbidden":
				dispatch.Instruction.ForbiddenActions = []string{"internal/source.txt"}
			case "role":
				dispatch.Recipient = "REVIEWER"
			case "branch":
				dispatch.Instruction.Branch = "different"
			case "origin":
				dispatch.Repository = "other/repo"
			case "start":
				dispatch.ExecutionStartSHA = strings.Repeat("f", 40)
			case "scanner":
				scanner = filepath.Join(t.TempDir(), "missing")
			case "index-lock":
				writeTest(t, root, ".git/index.lock", "existing lock")
			case "merge":
				writeTest(t, root, ".git/MERGE_HEAD", dispatch.ExecutionStartSHA)
			case "empty":
				gitTest(t, root, "reset", "-q", "HEAD", "--", "internal/source.txt")
			case "prior-message":
				gitTest(t, root, "commit", "-qm", "invalid prior authored commit")
			case "signing":
				gitTest(t, root, "config", "commit.gpgsign", "true")
				gitTest(t, root, "config", "gpg.program", filepath.Join(t.TempDir(), "missing"))
			}
			head := gitTest(t, root, "rev-parse", "HEAD")
			index := gitTest(t, root, "ls-files", "--stage", "-z")
			sha, err := Commit(context.Background(), dispatch, message, scanner)
			if err == nil || sha != "" || strings.Contains(err.Error(), "fake-private-payload") {
				t.Fatal("unsafe managed commit accepted or leaked content", kind, err)
			}
			if head != gitTest(t, root, "rev-parse", "HEAD") || index != gitTest(t, root, "ls-files", "--stage", "-z") {
				t.Fatal("rejected commit changed Git state")
			}
			if kind != "index-lock" {
				if _, err := os.Stat(filepath.Join(root, ".git", "index.lock")); !os.IsNotExist(err) {
					t.Fatal("index lock not released", err)
				}
			}

		})
	}
}

func TestManagedCommitBranchCompareAndSwap(t *testing.T) {
	root, dispatch := commitFixture(t)
	tree := strings.TrimSpace(gitTest(t, root, "write-tree"))
	command := exec.Command("git", "-C", root, "commit-tree", tree, "-p", dispatch.ExecutionStartSHA)
	command.Stdin = strings.NewReader(managedMessage)
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	other := strings.TrimSpace(string(output))
	scanner := filepath.Join(t.TempDir(), "scanner")
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = version ]; then git -C %q update-ref refs/heads/work/task %q; printf '1.9.0\\n'; exit 0; fi\ncat >/dev/null\nprintf '[]'\n", root, other)
	if err := os.WriteFile(scanner, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	sha, err := Commit(context.Background(), dispatch, managedMessage, scanner)
	if err == nil || sha != "" || !strings.Contains(err.Error(), "branch update failed") {
		t.Fatal("concurrent branch update was replaced", err)
	}
	if !strings.Contains(err.Error(), "candidate") {
		t.Fatal("failed publication did not report the candidate for reconciliation", err)
	}
	if strings.TrimSpace(gitTest(t, root, "rev-parse", "HEAD")) != other {
		t.Fatal("managed commit lost external branch update")
	}
}

func TestManagedCommitHoldsIndexLockWhileScanning(t *testing.T) {
	root, dispatch := commitFixture(t)
	expected := strings.TrimSpace(gitTest(t, root, "write-tree"))
	writeTest(t, root, "internal/source.txt", "different unstaged version")
	scanner := filepath.Join(t.TempDir(), "scanner")
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = version ]; then if git -C %q add internal/source.txt 2>/dev/null; then exit 2; fi; printf '1.9.0\\n'; exit 0; fi\ncat >/dev/null\nprintf '[]'\n", root)
	if err := os.WriteFile(scanner, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	sha, err := Commit(context.Background(), dispatch, managedMessage, scanner)
	if err != nil {
		t.Fatal("index was not protected during scan", err)
	}
	if strings.TrimSpace(gitTest(t, root, "rev-parse", sha+"^{tree}")) != expected {
		t.Fatal("concurrent staging entered scanned commit")
	}
}

func TestManagedCommitLinkedWorktree(t *testing.T) {
	root, dispatch := commitFixture(t)
	worktree := filepath.Join(t.TempDir(), "linked")
	gitTest(t, root, "worktree", "add", "-b", "work/linked", worktree, dispatch.ExecutionStartSHA)
	dispatch.Instruction.Worktree, dispatch.Instruction.Branch = worktree, "work/linked"
	writeTest(t, worktree, "internal/source.txt", "linked approved content")
	gitTest(t, worktree, "add", "internal/source.txt")
	sha, err := Commit(context.Background(), dispatch, managedMessage, fakeScanner(t, "clean"))
	if err != nil || strings.TrimSpace(gitTest(t, worktree, "rev-parse", "HEAD")) != sha {
		t.Fatal("linked worktree commit failed", err)
	}
	if strings.TrimSpace(gitTest(t, root, "rev-parse", "HEAD")) != dispatch.ExecutionStartSHA {
		t.Fatal("linked commit changed main worktree branch")
	}
}

func TestManagedCommitRealScanner(t *testing.T) {
	scanner := os.Getenv("TAXION_TEST_BETTERLEAKS")
	if scanner == "" {
		t.Skip("set TAXION_TEST_BETTERLEAKS to test the pinned scanner")
	}
	root, dispatch := commitFixture(t)
	sha, err := Commit(context.Background(), dispatch, managedMessage, scanner)
	if err != nil || sha == "" {
		t.Fatal("real scanner rejected safe managed commit", err)
	}
	writeTest(t, root, "internal/source.txt", "another approved change")
	gitTest(t, root, "add", "internal/source.txt")
	if _, err := Commit(context.Background(), dispatch, managedMessage, scanner); err != nil {
		t.Fatal("second managed commit rejected prior valid policy", err)
	}
}
