package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/memory"
	"github.com/hiono/taxion/internal/runtime/controller"
)

func TestUsageDescribesCurrentCommands(t *testing.T) {
	for _, text := range []string{
		"memory search", "--query required", "memory export", "no transmission",
		"Bind coder-output evidence", "local stub",
		"conversation watch", "--capabilities", "--dispatch",
		"task create, dispatch and review require an explicit --repo",
		"memory configure", "memory approve", "memory checks",
		"memory conflict",
		"conversation search", "workflow step", "workflow interrupt",
		"workflow planning-context", "workflow planner-draft",
		"workflow planner-action", "workflow question-draft",
	} {
		if !strings.Contains(usage, text) {
			t.Errorf("usage missing %q", text)
		}

	}
	if got := strings.Count(usage, "conversation watch"); got != 1 {
		t.Errorf("conversation watch appears %d times in usage, want 1", got)
	}
	if got := strings.Count(usage, "conversation search"); got != 1 {
		t.Errorf("conversation search appears %d times in usage, want 1", got)
	}
}

func TestUsageGroupsCommandsWithoutChangingSyntax(t *testing.T) {
	_, commands, ok := strings.Cut(usage, "Commands:\n")
	if !ok {
		t.Fatal("missing command index")
	}
	commands, _, _ = strings.Cut(commands, "\nIdentity gate")
	groups := map[string]int{}
	group := ""
	seen := map[string]bool{}
	for _, line := range strings.Split(commands, "\n") {
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "  ") {
			if _, exists := groups[line]; !strings.HasSuffix(line, ":") || exists {
				t.Fatalf("invalid or repeated category: %s", line)
			}
			group = line
			groups[group] = 0
			continue
		}
		if group == "" || seen[line] {
			t.Fatalf("uncategorized or repeated command: %s", line)
		}
		seen[line] = true
		groups[group]++
	}
	if len(groups) != 12 || len(seen) != 97 {
		t.Fatalf("%d commands in categories: %v", len(seen), groups)
	}
	for name, count := range groups {
		if count < 2 || count > 24 {
			t.Errorf("category %s contains %d commands; keep discovery bounded", name, count)
		}
	}
	t.Logf("%d command entries in %d categories: %v", len(seen), len(groups), groups)
}

func TestTaskGraphRejectsIgnoredFileOption(t *testing.T) {
	if err := taskGraphCmd("graph", []string{"--file", "ignored.json"}); err == nil {
		t.Fatal("task graph accepted an unused --file option")
	}
}

func TestRecordsRejectOptionsNotSupportedByAction(t *testing.T) {
	err := recordsCmd([]string{"show", "--file", "ignored.json"})
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined: -file") {
		t.Fatalf("records show error = %v, want unsupported --file error", err)
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

func TestRuntimeDispatchResultSchema(t *testing.T) {
	dispatch := core.GraphDispatch{
		ID: "dispatch-1", TaskID: "task-1", Recipient: "CODER", Repository: "owner/repo",
	}
	schema := dispatchResultSchema(dispatch)
	if schema["additionalProperties"] != false {
		t.Fatal("result schema must reject undocumented fields")
	}
	properties := schema["properties"].(map[string]any)
	validations := properties["validations"].(map[string]any)
	validationItems := validations["items"].(map[string]any)
	validationProperties := validationItems["properties"].(map[string]any)
	for _, field := range []string{"worktree", "sha"} {
		if _, ok := validationProperties[field]; !ok {
			t.Fatalf("validation schema omits Git provenance field %s", field)
		}
	}
}

func TestRuntimeResultSchemaNamesExactRequiredEvidence(t *testing.T) {
	for _, recipient := range []string{"CODER", "REVIEWER"} {
		dispatch := core.GraphDispatch{
			Recipient: recipient,
			Instruction: core.DispatchInstruction{
				RequiredEvidence: []string{"work-report", "skill-check"},
			},
		}
		for _, strict := range []bool{false, true} {
			schema := controller.DispatchResultSchema(dispatch, strict)
			properties := schema["properties"].(map[string]any)
			description := properties["evidence"].(map[string]any)["description"].(string)
			for _, identifier := range dispatch.Instruction.RequiredEvidence {
				if !strings.Contains(description, `"`+identifier+`"`) {
					t.Fatalf("%s strict=%t schema omits exact identifier %q: %s", recipient, strict, identifier, description)
				}
			}
			files := properties["changedFiles"].(map[string]any)
			if recipient == "REVIEWER" && files["maxItems"] != 0 {
				t.Fatal("Reviewer schema permits reporting source Coder paths as its own edits")
			}
			if recipient == "CODER" {
				if _, restricted := files["maxItems"]; restricted {
					t.Fatal("Coder changed paths were constrained by Reviewer policy")
				}
			}
		}
	}
	for _, submission := range []bool{false, true} {
		prompt := runtimeSubmissionPrompt("role", "contract", submission)
		if !strings.Contains(prompt, "separate array item") || !strings.Contains(prompt, `not "work-report: changes made"`) {
			t.Fatalf("submission=%t does not distinguish an evidence identifier from its description", submission)
		}
	}
}

func TestReviewerResponseSchemaContainsAcceptanceAndFindings(t *testing.T) {
	dispatch := core.GraphDispatch{
		Recipient: "REVIEWER",
		Instruction: core.DispatchInstruction{
			Acceptance: []string{"tests pass", "scope preserved"},
		},
	}
	schema := dispatchResultSchema(dispatch)
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("result schema properties = %T", schema["properties"])
	}
	for _, field := range []string{"reviewVerdict", "acceptance", "findings", "worktreeChanged"} {
		if _, ok := properties[field]; !ok {
			t.Fatalf("Reviewer response schema omits %s", field)
		}
	}
}
