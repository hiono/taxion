//go:build unix

package worktree

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestOpenRegularWorktreeFileDoesNotBlockOnFIFOReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "candidate")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatalf("write original file: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove original file: %v", err)
	}
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("replace with FIFO: %v", err)
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		t.Fatalf("open Worktree root: %v", err)
	}
	defer root.Close()
	file, _, err := openRegularWorktreeFile(root, filepath.Dir(path), filepath.Base(path))
	if err == nil {
		_ = file.Close()
		t.Fatal("FIFO replacement was accepted as a regular file")
	}
}
