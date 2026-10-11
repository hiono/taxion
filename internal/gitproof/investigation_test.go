package gitproof

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCommittedInvestigationIsBoundedScopedAndNeverReadsMutableFiles(t *testing.T) {
	root := t.TempDir()
	gitCommand(t, root, "init", "-b", "work/task")
	gitCommand(t, root, "config", "user.name", "Taxion Test")
	gitCommand(t, root, "config", "user.email", "taxion-test@example.com")
	gitCommand(t, root, "remote", "add", "origin", "https://github.com/owner/repo.git")
	sha := commitFile(t, root, "src/file.txt", "recorded\n")
	if err := os.WriteFile(filepath.Join(root, "src/file.txt"), []byte("mutable secret"), 0600); err != nil {
		t.Fatal(err)
	}
	read := func(paths []string, ceiling int) ([]InvestigatedFile, error) {
		return ReadCommittedFiles(context.Background(), root, "owner/repo", sha, []string{"src"}, []string{"src/private"}, paths, ceiling)
	}
	files, err := read([]string{"src/file.txt"}, len("recorded\n"))
	if err != nil || !reflect.DeepEqual(files, []InvestigatedFile{{Path: "src/file.txt", Content: "recorded\n"}}) {
		t.Fatal("investigation did not return exact committed bytes at its inclusive ceiling", files, err)
	}
	for _, test := range []struct {
		paths []string
		limit int
	}{
		{[]string{"src/file.txt"}, len("recorded\n") - 1},
		{[]string{"src/file.txt", "src/file.txt"}, 100},
		{[]string{"src/../README.md"}, 100},
		{[]string{"/src/file.txt"}, 100},
		{[]string{"src/private/key"}, 100},
		{[]string{"README.md"}, 100},
		{[]string{"src/missing"}, 100},
		{[]string{"src"}, 100},
		{[]string{"src/file.txt"}, 0},
		{[]string{"src/file.txt"}, 65537},
	} {
		if files, err := read(test.paths, test.limit); err == nil || files != nil {
			t.Fatal("invalid investigation returned source content", test, files, err)
		}
	}
	if _, err := ReadCommittedFiles(context.Background(), root, "owner/other", sha, []string{"src"}, nil, []string{"src/file.txt"}, 100); err == nil {
		t.Fatal("investigation accepted another repository")
	}
	if _, err := ReadCommittedFiles(context.Background(), root, "owner/repo", "HEAD", []string{"src"}, nil, []string{"src/file.txt"}, 100); err == nil {
		t.Fatal("investigation accepted a mutable revision")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReadCommittedFiles(ctx, root, "owner/repo", sha, []string{"src"}, nil, []string{"src/file.txt"}, 100); err == nil {
		t.Fatal("investigation ignored interruption")
	}
	status := gitCommand(t, root, "status", "--porcelain")
	if !strings.Contains(status, "src/file.txt") {
		t.Fatal("investigation reverted the mutable worktree")
	}
	t.Setenv("GIT_DIR", t.TempDir())
	t.Setenv("GIT_WORK_TREE", t.TempDir())
	if files, err := read([]string{"src/file.txt"}, 100); err != nil || files[0].Content != "recorded\n" {
		t.Fatal("ambient Git routing replaced the approved worktree", err)
	}
}

func TestCommittedInvestigationRejectsBinaryAndAggregateOverflow(t *testing.T) {
	root := t.TempDir()
	gitCommand(t, root, "init", "-b", "work/task")
	gitCommand(t, root, "config", "user.name", "Taxion Test")
	gitCommand(t, root, "config", "user.email", "taxion-test@example.com")
	gitCommand(t, root, "remote", "add", "origin", "https://github.com/owner/repo.git")
	commitFile(t, root, "src/a", "12345")
	commitFile(t, root, "src/b", "67890")
	sha := commitFile(t, root, "src/binary", "\x00binary")
	for _, paths := range [][]string{{"src/a", "src/b"}, {"src/binary"}} {
		if files, err := ReadCommittedFiles(context.Background(), root, "owner/repo", sha, []string{"src"}, nil, paths, 8); err == nil || files != nil {
			t.Fatal("binary or aggregate byte overflow produced partial evidence", paths, err)
		}
	}
}
