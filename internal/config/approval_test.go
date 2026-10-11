package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestApprovalChecksCompleteTuples(t *testing.T) {
	candidate, err := Load([]byte(validConfig))
	if err != nil {
		t.Fatal(err)
	}
	second := candidate
	second.Runtime.Account = "second-account"
	second.Runtime.ContractRef = "second-contract"
	approval := Approval{Kind: ApprovalKind, TaskID: "task", Reason: "Human approved these combinations", Allowed: []Config{candidate, second}}
	encode := func(a Approval) string {
		t.Helper()
		data, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	input := encode(approval)
	for _, selected := range []Config{candidate, second} {
		if err := CheckApproval(input, "task", candidate.Execution.ApprovalRef, selected); err != nil {
			t.Fatal(err)
		}
	}
	mixed := candidate
	mixed.Runtime.Account = second.Runtime.Account
	if err := CheckApproval(input, "task", candidate.Execution.ApprovalRef, mixed); err == nil {
		t.Fatal("independent account and contract lists widened approval")
	}
	for _, text := range []string{
		"approved", input + "\n{}", strings.Replace(input, `"kind":`, `"unknown":`, 1),
		strings.Replace(input, `"kind":`, `"kind":"duplicate","kind":`, 1),
		strings.Replace(input, `"taskId":"task"`, `"taskId":"other"`, 1),
	} {
		if err := CheckApproval(text, "task", candidate.Execution.ApprovalRef, candidate); err == nil {
			t.Fatal("invalid or unrelated approval accepted")
		}
	}
	if err := CheckApproval(input, "task", "different-decision", candidate); err == nil {
		t.Fatal("different approval reference accepted")
	}
	approval.Allowed = append(approval.Allowed, Config{})
	if err := CheckApproval(encode(approval), "task", candidate.Execution.ApprovalRef, candidate); err == nil {
		t.Fatal("matched tuple hid an invalid allowed tuple")
	}
}

func TestApprovalNamesAdditionalTasksWithoutWideningTuples(t *testing.T) {
	candidate, err := Load([]byte(validConfig))
	if err != nil {
		t.Fatal(err)
	}
	approval := Approval{
		Kind: ApprovalKind, TaskID: "implementation", Reason: "authorize named reviews",
		AdditionalTaskIDs: []string{"flow-review-0", "flow-review-1"}, Allowed: []Config{candidate},
	}
	check := func(taskID string, selected Config) error {
		t.Helper()
		input, err := json.Marshal(approval)
		if err != nil {
			t.Fatal(err)
		}
		return CheckApproval(string(input), taskID, candidate.Execution.ApprovalRef, selected)
	}
	for _, id := range []string{"implementation", "flow-review-0", "flow-review-1"} {
		if err := check(id, candidate); err != nil {
			t.Fatal("explicitly named Task rejected", err)
		}
	}
	for _, id := range []string{"", "flow-review-2", "other-flow-review-0", "flow-review-01", "flow-review-0-extra"} {
		if err := check(id, candidate); err == nil {
			t.Fatal("unnamed Task accepted")
		}
	}
	changed := candidate
	changed.Runtime.ContractRef = "different-contract"
	if err := check("flow-review-0", changed); err == nil {
		t.Fatal("additional Task widened the tuple")
	}
	for _, ids := range [][]string{
		{""}, {" "}, {"flow-review-*"}, {"flow-review-?"}, {"flow-review-[01]"},
		{"implementation"}, {"flow-review-0", "flow-review-0"}, {"flow-review-0", "invalid task"},
	} {
		approval.AdditionalTaskIDs = ids
		if err := check("implementation", candidate); err == nil {
			t.Fatal("invalid additional IDs hidden by a matching primary Task")
		}
	}
	approval.AdditionalTaskIDs = nil
	if err := check("implementation", candidate); err != nil {
		t.Fatal("legacy single-Task approval rejected", err)
	}
	if err := check("flow-review-0", candidate); err == nil {
		t.Fatal("parent approval implicitly inherited")
	}
}
