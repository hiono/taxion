package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestSavedReviewAcceptanceRejectsWithoutCreatingWorkspace(t *testing.T) {
	previous := storePath
	dir := t.TempDir()
	storePath = filepath.Join(dir, "absent", "ledger.json")
	t.Cleanup(func() { storePath = previous })
	request := filepath.Join(dir, "acceptance.json")
	if err := os.WriteFile(request, []byte(`{"flowId":"stopped","actor":"user","source":"user:decision","reason":"accept saved result","allowExpired":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"--help"},
		{"--file", request, "--preview"},
		{"--file", request},
		{"--file", request, "--preview", "--reviewer-tools", "view"},
	} {
		var output bytes.Buffer
		if err := workflowCommand(t.Context(), append([]string{"accept-saved-review"}, args...), &output); err == nil {
			t.Fatal("unexpected successful acceptance", args)
		}
		if _, err := os.Stat(filepath.Dir(storePath)); !os.IsNotExist(err) {
			t.Fatal("acceptance created a workspace", args, err)
		}
	}
}
