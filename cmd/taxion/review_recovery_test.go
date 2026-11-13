package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRecoverReviewRejectsMissingApprovalAndNeverCreatesWorkspace(t *testing.T) {
	before := storePath
	storePath = filepath.Join(t.TempDir(), "missing", "ledger.json")
	t.Cleanup(func() { storePath = before })
	file := filepath.Join(t.TempDir(), "recovery.json")
	for _, contents := range []string{`{}`, `{"unexpected":"field"}`} {
		if err := os.WriteFile(file, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"--file", file}, {"--file", file, "--preview"}} {
			var output bytes.Buffer
			if err := recoverArtifactReviewCommand(args, &output); err == nil {
				t.Fatal("invalid recovery accepted")
			}
			if _, err := os.Stat(filepath.Dir(storePath)); !os.IsNotExist(err) {
				t.Fatal("recovery created workspace", err)
			}
		}
	}
}
