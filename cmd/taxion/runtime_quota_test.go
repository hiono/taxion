package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeQuotaArgumentsDoNotAuthorizeExecution(t *testing.T) {
	var output bytes.Buffer
	if err := runtimeQuotaCmd([]string{"--help"}, &output); err != nil ||
		!strings.Contains(output.String(), "approved account guidance") ||
		strings.Contains(output.String(), "system-prompt-file") {
		t.Fatal("quota help requires model inputs or omits approval", output.String(), err)
	}

	for _, args := range [][]string{
		{}, {"--dispatch", "d", "--timeout", "0s"},
		{"--dispatch", "d", "--timeout", "6s"}, {"--dispatch", "d", "extra"},
		{"--dispatch", "d", "--budget-guidance", "account"},
		{"--dispatch", "d", "--max-ai-credits", "30"},
		{"--dispatch", "d", "--system-prompt-file", "prompt"},
	} {
		output.Reset()
		if err := runtimeQuotaCmd(args, &output); err == nil {
			t.Fatal("invalid quota-only arguments accepted", args)
		}
	}
}

func TestRuntimeQuotaDoesNotInitializeWorkspace(t *testing.T) {
	oldStore := storePath
	storePath = filepath.Join(t.TempDir(), "ledger.json")
	defer func() { storePath = oldStore }()
	var output bytes.Buffer
	if err := runtimeQuotaCmd([]string{"--dispatch", "d"}, &output); err == nil {
		t.Fatal("quota probe accepted a missing workspace")
	}
	if _, err := os.Stat(workspaceDBPath()); !os.IsNotExist(err) || output.Len() != 0 {
		t.Fatal("quota probe created a database or emitted a quota", err, output.String())
	}
}
