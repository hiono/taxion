package gitguard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitTest(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	result, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, result, err)
	}
	return string(result)
}

func writeTest(t *testing.T, root, name, content string) {
	t.Helper()
	filename := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestIndexProhibitedPaths(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{".taxion/config.yaml", true},
		{".taxion/cache/session", true},
		{"nested/.taxion/state", true},
		{".taxion", true},
		{".env", true},
		{".env.local", true},
		{".env.example", true},
		{"nested/.env.production", true},
		{".taxion/private\nname\tvalue", true},
		{"docs/environment.example", false},
		{"docs/taxion-config.example.yaml", false},
		{"mise.toml", false},
		{".environment", false},
		{".taxion-example/config.yaml", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			gitTest(t, root, "init", "-q")
			writeTest(t, root, ".gitignore", ".taxion\n.env*\n")
			writeTest(t, root, tc.name, "fake-private-payload")
			gitTest(t, root, "add", "-f", "--", tc.name)
			before := gitTest(t, root, "ls-files", "--stage", "-z")
			err := CheckIndex(context.Background(), root)
			if (err != nil) != tc.want {
				t.Fatal("incorrect index policy result", err)
			}
			if err != nil && (strings.Contains(err.Error(), "fake-private-payload") ||
				strings.Contains(err.Error(), "private\nname")) {
				t.Fatal("index error disclosed private data")
			}
			if before != gitTest(t, root, "ls-files", "--stage", "-z") {
				t.Fatal("check changed index state")
			}
		})
	}
}

func TestIndexUsesStagedTreeNotWorkingCopy(t *testing.T) {
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	writeTest(t, root, ".env", "fake-private-payload")
	gitTest(t, root, "add", ".env")
	if err := os.Remove(filepath.Join(root, ".env")); err != nil {
		t.Fatal(err)
	}
	if err := CheckIndex(context.Background(), root); err == nil {
		t.Fatal("missing working file concealed index entry")
	}
	gitTest(t, root, "config", "user.name", "Test")
	gitTest(t, root, "config", "user.email", "test@example.com")
	gitTest(t, root, "commit", "-qm", "unsafe test baseline")
	writeTest(t, root, "safe.txt", "safe change")
	gitTest(t, root, "add", "safe.txt")
	if err := CheckIndex(context.Background(), root); err == nil {
		t.Fatal("unchanged tracked dotenv concealed by staged diff")
	}
	gitTest(t, root, "rm", "--cached", ".env")
	if err := CheckIndex(context.Background(), root); err != nil {
		t.Fatal("staged removal of unsafe path rejected", err)
	}
	gitTest(t, root, "commit", "-qm", "safe tree")
	gitTest(t, root, "mv", "safe.txt", ".env.local")
	if err := CheckIndex(context.Background(), root); err == nil {
		t.Fatal("rename into prohibited path accepted")
	}
}

func TestIndexEnvironmentAndSubdirectory(t *testing.T) {
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	writeTest(t, root, ".env", "fake-private-payload")
	writeTest(t, root, "nested/safe.txt", "safe data")
	gitTest(t, root, "add", "nested/safe.txt")
	if err := CheckIndex(context.Background(), root); err != nil {
		t.Fatal("untracked dotenv rejected by index check", err)
	}
	gitTest(t, root, "add", ".env")
	for _, name := range []string{"GIT_LITERAL_PATHSPECS", "GIT_GLOB_PATHSPECS", "GIT_NOGLOB_PATHSPECS", "GIT_ICASE_PATHSPECS"} {
		t.Setenv(name, "1")
		if err := CheckIndex(context.Background(), filepath.Join(root, "nested")); err == nil {
			t.Fatal("pathspec environment concealed root index entries", name)
		}
		t.Setenv(name, "0")
	}
	gitTest(t, root, "rm", "--cached", ".env")
	alternate := filepath.Join(t.TempDir(), "commit-index")
	t.Setenv("GIT_INDEX_FILE", alternate)
	gitTest(t, root, "read-tree", "--empty")
	gitTest(t, root, "add", ".env")
	before, err := os.ReadFile(alternate)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckIndex(context.Background(), filepath.Join(root, "nested")); err == nil {
		t.Fatal("alternate index or root paths ignored")
	}
	after, err := os.ReadFile(alternate)
	if err != nil || string(before) != string(after) {
		t.Fatal("alternate index changed", err)
	}
	t.Setenv("GIT_INDEX_FILE", filepath.Join(t.TempDir(), "corrupt-index"))
	if err := os.WriteFile(os.Getenv("GIT_INDEX_FILE"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := CheckIndex(context.Background(), root); err == nil {
		t.Fatal("unreadable index treated as safe")
	}
}

func TestIndexLinkedWorktreeAndSymlink(t *testing.T) {
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	gitTest(t, root, "config", "user.name", "Test")
	gitTest(t, root, "config", "user.email", "test@example.com")
	gitTest(t, root, "commit", "--allow-empty", "-qm", "baseline")
	linked := filepath.Join(t.TempDir(), "linked")
	gitTest(t, root, "worktree", "add", "--detach", "-q", linked)
	if err := os.Symlink("external-private-file", filepath.Join(linked, ".env")); err != nil {
		t.Fatal(err)
	}
	gitTest(t, linked, "add", ".env")
	if err := CheckIndex(context.Background(), linked); err == nil {
		t.Fatal("linked worktree or dotenv symlink bypassed gate")
	}
	if err := CheckIndex(context.Background(), root); err != nil {
		t.Fatal("linked index affected original worktree", err)
	}
}

func TestIndexFailsOnUnmergedNonrepositoryAndCancellation(t *testing.T) {
	root := t.TempDir()
	if err := CheckIndex(context.Background(), root); err == nil {
		t.Fatal("nonrepository accepted")
	}
	gitTest(t, root, "init", "-q")
	writeTest(t, root, "safe.txt", "safe")
	sha := strings.TrimSpace(gitTest(t, root, "hash-object", "-w", "safe.txt"))
	command := exec.Command("git", "-C", root, "update-index", "--index-info")
	command.Stdin = strings.NewReader("100644 " + sha + " 1\tsafe.txt\n100644 " + sha + " 2\tsafe.txt\n")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create unmerged index: %s: %v", output, err)
	}
	if err := CheckIndex(context.Background(), root); err == nil || !strings.Contains(err.Error(), "unresolved") {
		t.Fatal("unmerged index accepted", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := CheckIndex(ctx, root); err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatal("cancelled inspection accepted", err)
	}
}

func TestIndexHookHelper(t *testing.T) {
	if os.Getenv("TAXION_INDEX_HOOK_TEST") != "1" {
		return
	}
	directory, err := os.Getwd()
	if err != nil {
		os.Exit(2)
	}
	if err := CheckIndex(context.Background(), directory); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestIndexChecksGitPartialCommitIndex(t *testing.T) {
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	gitTest(t, root, "config", "user.name", "Test")
	gitTest(t, root, "config", "user.email", "test@example.com")
	writeTest(t, root, "safe.txt", "baseline")
	gitTest(t, root, "add", "safe.txt")
	gitTest(t, root, "commit", "-qm", "baseline")
	hooks := t.TempDir()
	script := "#!/bin/sh\nexec \"$TAXION_TEST_EXECUTABLE\" -test.run='^TestIndexHookHelper$'\n"
	if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	gitTest(t, root, "config", "core.hooksPath", hooks)
	writeTest(t, root, "safe.txt", "safe change")
	writeTest(t, root, ".env", "fake-private-payload")
	gitTest(t, root, "add", ".env")
	commit := func(args ...string) ([]byte, error) {
		command := exec.Command("git", append([]string{"-C", root, "commit"}, args...)...)
		command.Env = append(os.Environ(), "TAXION_INDEX_HOOK_TEST=1", "TAXION_TEST_EXECUTABLE="+os.Args[0])
		return command.CombinedOutput()
	}
	// Git constructs a temporary index excluding the separately staged .env.
	if output, err := commit("--only", "safe.txt", "-m", "safe partial commit"); err != nil {
		t.Fatalf("safe partial commit rejected: %s: %v", output, err)
	}
	head := gitTest(t, root, "rev-parse", "HEAD")
	before := gitTest(t, root, "ls-files", "--stage", "-z")
	if _, err := commit("-m", "unsafe staged commit"); err == nil {
		t.Fatal("hook accepted staged dotenv")
	}
	if _, err := commit("--only", ".env", "-m", "unsafe partial commit"); err == nil {
		t.Fatal("hook accepted dotenv in temporary partial index")
	}
	if head != gitTest(t, root, "rev-parse", "HEAD") || before != gitTest(t, root, "ls-files", "--stage", "-z") {
		t.Fatal("rejected commits changed HEAD or staged tree")
	}
}
