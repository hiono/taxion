package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateAuditOutputAndFailClosedUnconfiguredProject(t *testing.T) {
	inside, err := filepath.Abs("private-audit.json")
	if err != nil {
		t.Fatal(err)
	}
	if privateAuditOutput(inside) == nil {
		t.Fatal("private report allowed in public repository")
	}
	outside := filepath.Join(t.TempDir(), "audit.json")
	if err := privateAuditOutput(outside); err != nil {
		t.Fatal(err)
	}
	request := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(request, []byte(`{"provider":"gitlab","host":"forge.example","repository":"example/unconfigured","output":"unused"}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TAXION_COMMUNICATION_PROFILES", "")
	for _, mode := range []string{"-audit-gitlab", "-audit-file", "-guidance"} {
		var output bytes.Buffer
		if err := run([]string{mode, request}, &output, &output); err == nil || output.Len() != 0 {
			t.Fatal("unconfigured profile emitted content", mode, err)
		}
	}
}
