package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestStoppedDispatchReconciliationDoesNotCreateWorkspace(t *testing.T) {
	previous := storePath
	dir := t.TempDir()
	storePath = filepath.Join(dir, "absent", "ledger.json")
	t.Cleanup(func() { storePath = previous })
	request := filepath.Join(dir, "reconciliation.json")
	if err := os.WriteFile(request, []byte(`{"flowId":"stopped","dispatchId":"first","actor":"human","source":"human:decision","reason":"retire","evidence":["human-confirmed quiescence"],"noActiveExecutionConfirmed":true,"acceptDeliveryUncertainty":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"--help"}, {"--file", request, "--preview"}, {"--file", request},
		{"--file", request, "--preview", "--reviewer-tools", "view"},
	} {
		var output bytes.Buffer
		if err := workflowCommand(t.Context(), append([]string{"reconcile-stopped"}, args...), &output); err == nil {
			t.Fatal("unexpected successful reconciliation", args)
		}
		if _, err := os.Stat(filepath.Dir(storePath)); !os.IsNotExist(err) {
			t.Fatal("reconciliation created a workspace", args, err)
		}
	}
}
