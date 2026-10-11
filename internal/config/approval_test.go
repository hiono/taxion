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
