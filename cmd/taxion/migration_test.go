package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/memory"
)

func TestMemoryMigrationCLIAndSpecificationHash(t *testing.T) {
	previous := storePath
	root := t.TempDir()
	storePath = filepath.Join(root, "ledger.json")
	t.Cleanup(func() { storePath = previous })
	s, err := openMemory()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(memory.KindDecision, memory.Entry{
		Title: "decision", Body: "approved local scope", Scope: memory.ScopeGlobal, SourceRefs: []string{"human:1"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(root, "snapshot.db")
	if err := memoryMigrationCmd("snapshot", []string{"--output", snapshot}); err != nil {
		t.Fatal(err)
	}
	if err := memoryMigrationCmd("inspect-snapshot", []string{"--file", snapshot}); err != nil {
		t.Fatal(err)
	}
	report, err := memory.InspectSnapshot(t.Context(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "restored.db")
	args := []string{"--file", snapshot, "--output", target, "--expected-hash", report.Hash, "--actor", "human", "--source", "human:cli-approval"}
	if err := memoryMigrationCmd("restore", args); err != nil {
		t.Fatal(err)
	}
	if err := memoryMigrationCmd("restore", args); err == nil {
		t.Fatal("CLI replaced an existing destination")
	}
	if err := memoryMigrationCmd("inspect-snapshot", []string{"--file", snapshot, "unexpected"}); err == nil {
		t.Fatal("CLI ignored unexpected arguments")
	}
	spec := core.Specification{
		ID: "synthetic", Title: "Synthetic source", Source: "https://example.org/spec",
		Location: "section 1", Version: "v1", RetrievedAt: time.Now().UTC(),
		Body: "Return 202.", Usage: "Implement and independently review.",
	}
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "spec.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := specificationCmd([]string{"hash", "--file", path}); err != nil {
		t.Fatal(err)
	}
	spec.Location = ""
	data, err = json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := specificationCmd([]string{"hash", "--file", path}); err == nil {
		t.Fatal("CLI hashed an uncited source")
	}
}
