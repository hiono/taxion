package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCommitMessageRequiresConventionalSubjectAndContextualBody(t *testing.T) {
	for _, message := range []string{
		"feat(runtime): execute approved work\n\nintent(runtime): retain the user's acceptance",
		"fix!: preserve authority\n\nconstraint(authority): execution requires a sourced authorization\n\nBREAKING CHANGE: previously implicit authority requires explicit approval",
		"refactor(memory)!: retain immutable sources\r\n\r\ndecision(memory): snapshot the issued version\r\n\r\nCo-authored-by: Copilot <copilot@example.com>\r\n",
		"test(core): check reasoning format\n\nintent(core): retain decisions after restart\nrejected(core): session-only persistence loses the audit trail\nlearned(core): source references survive SQLite reopen\n\nRefs: #53\nSigned-off-by: Dev <dev@example.com>",
		"fix(core): 判断の出典を保持する\n\nintent(core): 再開時に以前の判断を説明し直さずに済むようにする",
	} {
		if err := ValidateCommitMessage(message); err != nil {
			t.Fatalf("valid commit rejected: %q: %v", message, err)
		}

	}
	for _, message := range []string{
		"", "update application", "feat: application", "feat: application\n\n",
		"feat(core): implement\nintent(core): missing separator",
		"feat(core): implement\n\nplain body",
		"feat(core): implement\n\nCo-authored-by: Dev <dev@example.com>",
		"feat(core): implement\n\nintent(core): ",
		"feat(core): implement\n\nintent(): implement",
		"feat(core): implement\n\nintent(core): rationale\n\nunstructured summary",
		"feat(core): implement\n\nintent(core): rationale\n\nRefs: #53\nlearned(core): late action",
		"feat(core) implement\n\nintent(core): rationale",
		"feat(core): implement\n\nunknown(core): rationale",
	} {
		if err := ValidateCommitMessage(message); err == nil {
			t.Fatalf("invalid commit accepted: %q", message)
		}
	}
}

func TestCoderDispatchPersistsAndRendersCommitPolicy(t *testing.T) {
	g := trackedGraph(t, graphTask("task"))
	move(t, &g, "task", Ready, TransitionProof{})
	dispatch, err := NewGraphDispatch(g, "coder", "task", "owner/repo", RelationshipNone, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if dispatch.Instruction.CommitMessagePolicy != CommitMessagePolicy {
		t.Fatal("new Coder instruction omitted the commit policy")
	}
	rendered, err := RenderDispatchInstruction(dispatch)
	if err != nil || !strings.Contains(rendered, "intent(scope):") || !strings.Contains(rendered, CommitMessagePolicy) {
		t.Fatal("commit format missing from the actual instruction", err)
	}
	changed := dispatch
	changed.Instruction.CommitMessagePolicy = ""
	changed.InstructionHash, err = dispatchInstructionHash(changed.Instruction)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.IssueDispatch(changed, "controller", "issue without policy"); err == nil {
		t.Fatal("new Coder Dispatch bypassed mandatory commit policy")
	}
	if err := g.IssueDispatch(dispatch, "controller", "issue with commit policy"); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(DispatchInstruction{Version: 4})
	if err != nil || strings.Contains(string(encoded), "commitMessagePolicy") {
		t.Fatal("optional policy changed old instruction hashes", err)
	}
	previous := DispatchInstruction{Version: 4, Title: "first"}
	current := previous
	current.Title = "updated"
	_, preserved := compareDispatchInstructions(previous, current)
	for _, field := range preserved {
		if field == "commitMessagePolicy" {
			t.Fatal("absent new field changed historical amendment comparisons")
		}
	}
	reviewTask := graphTask("review")
	reviewTask.Owner = "REVIEWER"
	reviewGraph := trackedGraph(t, reviewTask)
	move(t, &reviewGraph, "review", Ready, TransitionProof{})
	reviewer, err := NewGraphDispatch(reviewGraph, "reviewer", "review", "owner/repo", RelationshipNone, "", "")
	if err != nil {
		t.Fatal(err)
	}
	rendered, err = RenderDispatchInstruction(reviewer)
	if err != nil || !strings.Contains(rendered, "COMMIT REASONING REVIEW:") || reviewer.Instruction.CommitMessagePolicy != "" {
		t.Fatal("Reviewer requires independent reasoning review with its own read-only contract", err)
	}
}
