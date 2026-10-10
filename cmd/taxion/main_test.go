package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/memory"
)

func TestUsageDescribesCurrentCommands(t *testing.T) {
	for _, text := range []string{
		"memory search", "--query required", "memory export", "no transmission",
		"Bind coder-output evidence", "local stub",
		"task create, dispatch and review require an explicit --repo",
		"memory configure", "memory approve", "memory checks",
		"memory conflict",
	} {
		if !strings.Contains(usage, text) {
			t.Errorf("usage missing %q", text)
		}

	}
}

func TestMemoryConflictCLI(t *testing.T) {
	previous := storePath
	storePath = filepath.Join(t.TempDir(), "ledger.json")
	t.Cleanup(func() { storePath = previous })
	if err := initCmd(); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"Use SQLite", "Use JSON"} {
		if err := memoryCmd([]string{"put", "--kind", "decision", "--title", title}); err != nil {
			t.Fatal(err)
		}
	}
	s, err := openMemory()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := s.List(memory.KindDecision, "")
	if err != nil || len(entries) != 2 {
		t.Fatalf("Memory entries = %+v, %v", entries, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	left, right := fmt.Sprintf("decision:%d", entries[0].ID), fmt.Sprintf("decision:%d", entries[1].ID)
	if err := memoryCmd([]string{
		"conflict", "add", "--left", left, "--right", right,
		"--actor", "human", "--reason", "contradictory storage choices",
	}); err != nil {
		t.Fatal(err)
	}
	s, err = openMemory()
	if err != nil {
		t.Fatal(err)
	}
	conflicts, err := s.Conflicts(left)
	if err != nil || len(conflicts) != 1 {
		t.Fatalf("conflicts = %+v, %v", conflicts, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := memoryCmd([]string{
		"conflict", "resolve", "--id", fmt.Sprint(conflicts[0].ID),
		"--actor", "human", "--reason", "SQLite applies; JSON is temporary input",
	}); err != nil {
		t.Fatal(err)
	}
	if err := memoryCmd([]string{"conflict", "list", "--ref", left}); err != nil {
		t.Fatal(err)
	}
}

func TestCheckServeMode(t *testing.T) {
	cases := []struct {
		name    string
		mode    string
		addr    string
		token   string
		wantErr bool
	}{
		{"loopback default addr", "loopback", "127.0.0.1:3938", "", false},
		{"loopback tokened local", "loopback", "127.0.0.1:3938", "tok", false},
		{"loopback refuses wildcard untokened", "loopback", "0.0.0.0:3938", "", true},
		{"loopback refuses wildcard even tokened", "loopback", "0.0.0.0:3938", "tok", true},
		{"remote requires token", "remote", "0.0.0.0:3938", "", true},
		{"remote tokened wildcard", "remote", "0.0.0.0:3938", "tok", false},
		{"unknown mode", "dmz", "127.0.0.1:3938", "tok", true},
	}
	for _, tc := range cases {
		err := checkServeMode(tc.mode, tc.addr, tc.token)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, wantErr = %v", tc.name, err, tc.wantErr)
		}
	}
}

func TestMemoryPromotionCLIAndDispatch(t *testing.T) {
	previous := storePath
	storePath = filepath.Join(t.TempDir(), "ledger.json")
	t.Cleanup(func() { storePath = previous })
	if err := initCmd(); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"0", "-1", "bad"} {
		if err := memoryCmd([]string{"configure", "--promote-after", n}); err == nil {
			t.Fatalf("invalid threshold accepted: %s", n)
		}
	}
	if err := memoryCmd([]string{"configure", "--promote-after", "1"}); err != nil {
		t.Fatal(err)
	}
	if err := memoryCmd([]string{"warn", "--body", "use target", "--scope", "global"}); err != nil {
		t.Fatal(err)
	}
	s, err := openMemory()
	if err != nil {
		t.Fatal(err)
	}
	lessons, err := s.List(memory.KindLesson, memory.ScopeGlobal)
	if err != nil || len(lessons) != 1 {
		t.Fatalf("CLI candidate: %+v %v", lessons, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	args := []string{"approve", "--id", fmt.Sprint(lessons[0].ID),
		"--check", memory.CheckRepoEquals, "--value", "target"}
	if err := memoryCmd(args); err == nil {
		t.Fatal("global approval must require explicit scope")
	}
	if err := memoryCmd(append(args, "--scope", "global")); err != nil {
		t.Fatal(err)
	}
	if err := memoryCmd([]string{"checks"}); err != nil {
		t.Fatal(err)
	}
	l := core.Ledger{Tasks: []core.Task{{ID: "t", Title: "fix: target", Repo: "target"}}}
	if err := saveLedger(l); err != nil {
		t.Fatal(err)
	}
	if err := dispatchCmd([]string{"--task", "t", "--repo", "other"}); !errors.Is(err, memory.ErrGuardrailRejected) {
		t.Fatalf("CLI dispatch must enforce approved policy: %v", err)
	}
	got, err := loadStore()
	if err != nil || len(got.Dispatches) != 0 {
		t.Fatalf("rejected CLI dispatch changed ledger: %+v %v", got, err)
	}
	if err := dispatchCmd([]string{"--task", "t", "--repo", "target"}); err != nil {
		t.Fatalf("matching CLI dispatch failed: %v", err)
	}
}
