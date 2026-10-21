package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGateCommandValidatesInputs(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
	}{
		{args: nil, want: "usage:"},
		{args: []string{"unknown"}, want: "unknown gate"},
		{args: []string{"contract"}, want: "--task"},
		{args: []string{"execution"}, want: "--flow"},
		{args: []string{"publication"}, want: "--flow"},
		{args: []string{"contract", "--task", "t", "extra"}, want: "positional"},
	} {
		var output bytes.Buffer
		err := gateCommand(test.args, &output)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("gate %v returned %v, want error containing %q", test.args, err, test.want)
		}
	}
}

func TestGateCommandDoesNotCreateWorkspace(t *testing.T) {
	previous := storePath
	storePath = filepath.Join(t.TempDir(), "absent", "ledger.json")
	t.Cleanup(func() { storePath = previous })
	var output bytes.Buffer
	if err := gateCommand([]string{"contract", "--task", "missing"}, &output); err == nil {
		t.Fatal("gate report accepted an absent workspace")
	}
	if _, err := os.Stat(filepath.Dir(storePath)); !os.IsNotExist(err) {
		t.Fatal("gate report created state", err)
	}
}
