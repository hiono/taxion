package gitguard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedStagePublishesOnlySelectedFiles(t *testing.T) {
	for _, kind := range []string{"normal", "split", "linked"} {
		t.Run(kind, func(t *testing.T) {
			root, dispatch := commitFixture(t)
			original := root
			if kind == "split" {
				gitTest(t, root, "update-index", "--split-index")
			}
			if kind == "linked" {
				root = filepath.Join(t.TempDir(), "linked")
				gitTest(t, original, "worktree", "add", "-b", "work/linked", root, dispatch.ExecutionStartSHA)
				dispatch.Instruction.Worktree, dispatch.Instruction.Branch = root, "work/linked"
			}
			writeTest(t, root, "internal/selected.txt", "approved staged content")
			writeTest(t, root, "internal/unselected.txt", "unstaged content")
			t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "unrelated"))
			t.Setenv("GIT_INDEX_FILE", filepath.Join(t.TempDir(), "other-index"))
			tree, err := Stage(context.Background(), dispatch, []string{"internal/selected.txt"}, fakeScanner(t, "clean"))
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("GIT_DIR", "")
			t.Setenv("GIT_INDEX_FILE", "")
			// Git interprets empty redirection variables; remove them for fixture reads.
			if err := os.Unsetenv("GIT_DIR"); err != nil {
				t.Fatal(err)
			}
			if err := os.Unsetenv("GIT_INDEX_FILE"); err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(gitTest(t, root, "write-tree")) != tree ||
				strings.TrimSpace(gitTest(t, root, "rev-parse", "HEAD")) != dispatch.ExecutionStartSHA {
				t.Fatal("stage changed HEAD or failed to publish verified tree")
			}
			entries := gitTest(t, root, "ls-files", "--cached")
			if !strings.Contains(entries, "internal/selected.txt") || strings.Contains(entries, "unselected.txt") {
				t.Fatal("stage selected more than explicit files")
			}
			sha, err := Commit(context.Background(), dispatch, managedMessage, fakeScanner(t, "clean"))
			if err != nil || strings.TrimSpace(gitTest(t, root, "rev-parse", sha+"^{tree}")) != tree {
				t.Fatal("stage and commit disagree on verified tree", err)
			}
		})
	}
}

func TestManagedStageRejectsUnsafeSelectionsWithoutChangingIndex(t *testing.T) {
	for _, kind := range []string{"empty", "directory", "absolute", "parent", "dot", "duplicate", "noncanonical",
		"git", "dotenv", "local", "scope", "missing", "ignored", "secret", "prior-secret", "scanner", "lock", "symlink-parent"} {
		t.Run(kind, func(t *testing.T) {
			root, dispatch := commitFixture(t)
			writeTest(t, root, "internal/new.txt", "approved")
			paths := []string{"internal/new.txt"}
			scanner := fakeScanner(t, "conditional")
			switch kind {
			case "empty":
				paths = nil
			case "directory":
				paths = []string{"internal"}
			case "absolute":
				paths = []string{filepath.Join(root, "internal/new.txt")}
			case "parent":
				paths = []string{"../outside"}
			case "dot":
				paths = []string{"."}
			case "duplicate":
				paths = append(paths, paths[0])
			case "noncanonical":
				paths = []string{"internal/../internal/new.txt"}
			case "git":
				paths = []string{".git/config"}
			case "dotenv":
				writeTest(t, root, "internal/.env", "safe-looking")
				paths = []string{"internal/.env"}
			case "local":
				writeTest(t, root, "internal/.taxion/config.yaml", "safe-looking")
				paths = []string{"internal/.taxion/config.yaml"}
			case "scope":
				writeTest(t, root, "outside.txt", "approved")
				paths = []string{"outside.txt"}
			case "missing":
				paths = []string{"internal/missing.txt"}
			case "ignored":
				writeTest(t, root, ".gitignore", "internal/new.txt\n")
			case "secret":
				writeTest(t, root, "internal/new.txt", "fake-private-payload")
			case "prior-secret":
				writeTest(t, root, "internal/source.txt", "fake-private-payload")
				gitTest(t, root, "add", "internal/source.txt")
			case "scanner":
				scanner = filepath.Join(t.TempDir(), "missing")
			case "lock":
				writeTest(t, root, ".git/index.lock", "existing lock")
			case "symlink-parent":
				outside := t.TempDir()
				writeTest(t, outside, "source.txt", "outside")
				if err := os.Symlink(outside, filepath.Join(root, "internal", "outside")); err != nil {
					t.Fatal(err)
				}
				paths = []string{"internal/outside/source.txt"}
			}
			before, err := os.ReadFile(filepath.Join(root, ".git", "index"))
			if err != nil {
				t.Fatal(err)
			}
			tree, err := Stage(context.Background(), dispatch, paths, scanner)
			if err == nil || tree != "" || strings.Contains(err.Error(), "fake-private-payload") {
				t.Fatal("unsafe stage accepted or exposed content", kind, err)
			}
			after, err := os.ReadFile(filepath.Join(root, ".git", "index"))
			if err != nil || string(before) != string(after) ||
				strings.TrimSpace(gitTest(t, root, "rev-parse", "HEAD")) != dispatch.ExecutionStartSHA {
				t.Fatal("rejected stage changed real index or HEAD", kind, err)
			}
			if kind != "lock" {
				if _, err := os.Stat(filepath.Join(root, ".git", "index.lock")); !os.IsNotExist(err) {
					t.Fatal("failed stage left its index lock", err)
				}
			}
		})
	}
}

func TestManagedStageLiteralNamesAndTrackedDeletion(t *testing.T) {
	root, dispatch := commitFixture(t)
	scanner := fakeScanner(t, "clean")
	writeTest(t, root, "internal/*.txt", "literal wildcard name")
	writeTest(t, root, "internal/other.txt", "not selected")
	if _, err := Stage(t.Context(), dispatch, []string{"internal/*.txt"}, scanner); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(gitTest(t, root, "ls-files"), "internal/other.txt") {
		t.Fatal("literal selection expanded a wildcard")
	}
	sha, err := Commit(t.Context(), dispatch, managedMessage, scanner)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"internal/source.txt", "internal/*.txt", "internal/other.txt"} {
		if err := os.Remove(filepath.Join(root, path)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(root, "internal")); err != nil {
		t.Fatal(err)
	}
	tree, err := Stage(t.Context(), dispatch, []string{"internal/source.txt", "internal/*.txt"}, scanner)
	if err != nil || tree == "" || strings.Contains(gitTest(t, root, "ls-files"), "internal/") {
		t.Fatal("tracked deletion with removed parents failed", err)
	}
	if strings.TrimSpace(gitTest(t, root, "rev-parse", "HEAD")) != sha {
		t.Fatal("tracked deletion created a commit")
	}
}

func TestManagedStageStopsConcurrentBranchMovement(t *testing.T) {
	root, dispatch := commitFixture(t)
	writeTest(t, root, "internal/new.txt", "approved")
	indexPath := filepath.Join(root, ".git", "index")
	before, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	gitTest(t, root, "branch", "work/other")
	scanner := filepath.Join(t.TempDir(), "scanner")
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = version ]; then git -C %q symbolic-ref HEAD refs/heads/work/other; printf '1.9.0\\n'; exit 0; fi\ncat >/dev/null\nprintf '[]'\n", root)
	if err := os.WriteFile(scanner, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	tree, err := Stage(t.Context(), dispatch, []string{"internal/new.txt"}, scanner)
	if err == nil || tree != "" || !strings.Contains(err.Error(), "branch changed") {
		t.Fatal("stage published after branch movement", err)
	}
	after, err := os.ReadFile(indexPath)
	if err != nil || string(before) != string(after) {
		t.Fatal("stage changed real index after branch movement", err)
	}
}

func TestManagedStageChecksGitFilteredContent(t *testing.T) {
	root, dispatch := commitFixture(t)
	writeTest(t, root, ".gitattributes", "internal/new.txt filter=fixture\n")
	writeTest(t, root, "internal/new.txt", "apparently safe working content")
	filter := filepath.Join(t.TempDir(), "filter")
	if err := os.WriteFile(filter, []byte("#!/bin/sh\ncat >/dev/null\nprintf 'fake-private-payload'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	gitTest(t, root, "config", "filter.fixture.clean", filter)
	gitTest(t, root, "config", "filter.fixture.required", "true")
	if _, err := Stage(t.Context(), dispatch, []string{"internal/new.txt"}, fakeScanner(t, "conditional")); err == nil {
		t.Fatal("stage inspected working content instead of Git-filtered blob")
	}
	if strings.Contains(gitTest(t, root, "ls-files"), "internal/new.txt") {
		t.Fatal("failed filtered scan changed real index")
	}
}

func TestManagedStageWithRealScanner(t *testing.T) {
	scanner := os.Getenv("TAXION_TEST_BETTERLEAKS")
	if scanner == "" {
		t.Skip("set TAXION_TEST_BETTERLEAKS to test the pinned scanner")
	}
	root, dispatch := commitFixture(t)
	writeTest(t, root, "internal/new.txt", "safe staged content")
	tree, err := Stage(t.Context(), dispatch, []string{"internal/new.txt"}, scanner)
	if err != nil || tree != strings.TrimSpace(gitTest(t, root, "write-tree")) {
		t.Fatal("real scanner did not publish expected stage tree", err)
	}
	sha, err := Commit(t.Context(), dispatch, managedMessage, scanner)
	if err != nil || strings.TrimSpace(gitTest(t, root, "rev-parse", sha+"^{tree}")) != tree {
		t.Fatal("real scanner stage→commit mismatch", err)
	}
}

func TestManagedStageAllowsAuthorizedProhibitedPathRemoval(t *testing.T) {
	root, dispatch := commitFixture(t)
	writeTest(t, root, "internal/.env", "local setting reference")
	gitTest(t, root, "add", "internal/.env")
	gitTest(t, root, "commit", "-qm", "legacy local settings")
	head := strings.TrimSpace(gitTest(t, root, "rev-parse", "HEAD"))
	dispatch.ExecutionStartSHA, dispatch.Instruction.BaselineSHA = head, head
	if err := os.Remove(filepath.Join(root, "internal/.env")); err != nil {
		t.Fatal(err)
	}
	if _, err := Stage(t.Context(), dispatch, []string{"internal/.env"}, fakeScanner(t, "clean")); err != nil {
		t.Fatal("authorized deletion of prohibited local file rejected", err)
	}
	if strings.Contains(gitTest(t, root, "ls-files"), ".env") {
		t.Fatal("prohibited path remains staged after deletion")
	}
}
