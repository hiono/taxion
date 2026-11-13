package core

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestConfigurationBindingRequiresExactNamedTaskScope(t *testing.T) {
	for _, scoped := range []bool{false, true} {
		for _, ids := range [][]string{
			{"flow-review-0", "flow-review-1"}, nil, {"other-flow-review-0"},
			{"flow-review-*"}, {"flow-review-0", "flow-review-0"}, {"parent"},
			{"flow-review-0", ""}, {"flow-review-0", "invalid task"},
		} {
			selection := json.RawMessage(`{"version":1,"repository":{"path":"owner/repo"},"execution":{"approvalRef":"approval"}}`)
			approval, err := json.Marshal(struct {
				Kind              string            `json:"kind"`
				TaskID            string            `json:"taskId"`
				AdditionalTaskIDs []string          `json:"additionalTaskIds,omitempty"`
				Reason            string            `json:"reason"`
				Allowed           []json.RawMessage `json:"allowed"`
			}{"taxion.execution-config.v1", "parent", ids, "authorize named review", []json.RawMessage{selection}})
			if err != nil {
				t.Fatal(err)
			}

			decisionID := "approval"
			if scoped {
				decisionID = ConfigurationApprovalID("approval", "parent")
			}
			binding := DispatchConfiguration{
				DecisionID: decisionID, Selection: selection, RecordedAt: time.Now().UTC(),
				Sources: map[string]string{"version": "user", "repository.path": "user", "execution.approvalRef": "user"},
			}
			dispatch := GraphDispatch{
				TaskID: "flow-review-0", Repository: "owner/repo",
				Instruction: DispatchInstruction{Decisions: []TaskDecision{{ID: decisionID, Source: "human:scope", Text: string(approval)}}},
			}
			err = binding.validate(dispatch)
			if (err == nil) != (len(ids) == 2 && ids[1] == "flow-review-1") {
				t.Fatal("core configuration binding widened exact Task scope", err)
			}
		}
	}
}

func TestConfigurationBindingAcceptsExplicitReauthorizationDecision(t *testing.T) {
	selection := json.RawMessage(`{"version":1,"repository":{"path":"owner/repo"},"execution":{"approvalRef":"approval"}}`)
	decisionID := ConfigurationReauthorizationID("approval", "task", "unique-auth")
	approval, err := json.Marshal(struct {
		Kind                   string            `json:"kind"`
		TaskID                 string            `json:"taskId"`
		Reason                 string            `json:"reason"`
		DecisionID             string            `json:"decisionId"`
		AuthorizationReference string            `json:"authorizationReference"`
		ApprovalRef            string            `json:"approvalRef"`
		Allowed                []json.RawMessage `json:"allowed"`
	}{
		"taxion.execution-config.v1", "task", "confirm exact configuration",
		decisionID, "unique-auth", "approval", []json.RawMessage{selection},
	})
	if err != nil {
		t.Fatal(err)
	}
	binding := DispatchConfiguration{
		DecisionID: decisionID, Selection: selection, RecordedAt: time.Now().UTC(),
		Sources: map[string]string{"version": "user", "repository.path": "user", "execution.approvalRef": "user"},
	}
	dispatch := GraphDispatch{
		TaskID: "task", Repository: "owner/repo",
		Instruction: DispatchInstruction{Decisions: []TaskDecision{{ID: decisionID, Source: "human:reauthorization", Text: string(approval)}}},
	}
	if err := binding.validate(dispatch); err != nil {
		t.Fatalf("explicit reauthorization decision rejected: %v", err)
	}
	dispatch.Instruction.Decisions[0].Text = strings.Replace(string(approval), `"approvalRef":"approval"`, `"approvalRef":"other"`, 1)
	if err := binding.validate(dispatch); err == nil {
		t.Fatal("configuration binding accepted a different approval reference")
	}
}

func TestExecutionAccessObservationValidationAndCloning(t *testing.T) {
	selection := json.RawMessage(`{"version":1,"repository":{"path":"owner/repo"},"execution":{"approvalRef":"approval"}}`)
	approval := `{"kind":"taxion.execution-config.v1","taskId":"task","reason":"authorize task","allowed":[` + string(selection) + `]}`
	now := time.Now().UTC()
	binding := DispatchConfiguration{
		DecisionID: "approval", Selection: selection, RecordedAt: now,
		Sources: map[string]string{"version": "user", "repository.path": "user", "execution.approvalRef": "user"},
		AccessChecks: []ExecutionAccessCheck{{
			Operation: "START", RuntimeConfigHash: "runtime-hash", StartedAt: now, CompletedAt: now,
			RepositoryPermission: "READ_WRITE", ContractEligibility: "UNVERIFIED",
		}},
	}
	dispatch := GraphDispatch{
		TaskID: "task", Repository: "owner/repo", Recipient: "CODER", RuntimeConfigHash: "runtime-hash",
		Instruction: DispatchInstruction{Decisions: []TaskDecision{{ID: "approval", Source: "human:scope", Text: approval}}},
	}
	if err := binding.validate(dispatch); err != nil {
		t.Fatal(err)
	}
	for _, failure := range []string{"operation", "hash", "missing-hash", "missing-time", "reversed-time", "permission", "contract", "mixed-unbound-hashes"} {
		t.Run(failure, func(t *testing.T) {
			copy := cloneDispatchConfiguration(&binding)
			check := &copy.AccessChecks[0]
			candidate := dispatch
			switch failure {
			case "operation":
				check.Operation = "STOP"
			case "hash":
				check.RuntimeConfigHash = "other-hash"
			case "missing-hash":
				check.RuntimeConfigHash = ""
			case "missing-time":
				check.StartedAt = time.Time{}
			case "reversed-time":
				check.CompletedAt = now.Add(-time.Second)
			case "permission":
				check.RepositoryPermission = "READ"
			case "contract":
				check.ContractEligibility = "VERIFIED"
			case "mixed-unbound-hashes":
				candidate.RuntimeConfigHash = ""
				other := *check
				other.RuntimeConfigHash = "other-hash"
				copy.AccessChecks = append(copy.AccessChecks, other)
			}
			if err := copy.validate(candidate); err == nil {
				t.Fatal("invalid access observation accepted", failure)
			}
			if !reflect.DeepEqual(binding.AccessChecks, []ExecutionAccessCheck{{
				Operation: "START", RuntimeConfigHash: "runtime-hash", StartedAt: now, CompletedAt: now,
				RepositoryPermission: "READ_WRITE", ContractEligibility: "UNVERIFIED",
			}}) {
				t.Fatal("cloned observation changed the original")
			}
		})
	}
	dispatch.Recipient = "REVIEWER"
	binding.AccessChecks[0].RepositoryPermission = "READ"
	if err := binding.validate(dispatch); err != nil {
		t.Fatal("read-only role observation rejected", err)
	}
	binding.AccessChecks = nil
	if err := binding.validate(dispatch); err != nil {
		t.Fatal("legacy configuration without observations rejected", err)
	}
}
