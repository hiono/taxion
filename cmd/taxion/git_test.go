package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestGitCheckIndex(t *testing.T) {
	t.Chdir(t.TempDir())
	var output bytes.Buffer
	if err := checkGitIndex([]string{"check-index"}, &output); err == nil || output.Len() != 0 {
		t.Fatal("nonrepository check succeeded")
	}
	git := func(args ...string) {
		t.Helper()
		result, err := exec.Command("git", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s: %v", args, result, err)
		}
	}
	git("init", "-q")
	if err := checkGitIndex([]string{"check-index"}, &output); err != nil ||
		!strings.Contains(output.String(), "Secret content scanning has not been performed") {
		t.Fatal("empty index check failed or overstated protection", err)
	}
	if err := os.WriteFile(".env", []byte("fake-private-payload"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", ".env")
	output.Reset()
	err := checkGitIndex([]string{"check-index"}, &output)
	if err == nil || output.Len() != 0 || strings.Contains(err.Error(), "fake-private-payload") {
		t.Fatal("unsafe index accepted or private data disclosed", err)
	}
	for _, args := range [][]string{nil, {"show"}, {"check-index", "--file", "index"}, {"check-index", "extra"}} {
		output.Reset()
		if err := checkGitIndex(args, &output); err == nil || output.Len() != 0 {
			t.Fatal("invalid invocation succeeded")
		}
	}
}
