package core

import (
	"encoding/json"
	"testing"
	"time"
)

func TestConfigurationBindingRequiresExactNamedTaskScope(t *testing.T) {
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
		binding := DispatchConfiguration{
			DecisionID: "approval", Selection: selection, RecordedAt: time.Now().UTC(),
			Sources: map[string]string{"version": "user", "repository.path": "user", "execution.approvalRef": "user"},
		}
		dispatch := GraphDispatch{
			TaskID: "flow-review-0", Repository: "owner/repo",
			Instruction: DispatchInstruction{Decisions: []TaskDecision{{ID: "approval", Source: "human:scope", Text: string(approval)}}},
		}
		err = binding.validate(dispatch)
		if (err == nil) != (len(ids) == 2 && ids[1] == "flow-review-1") {
			t.Fatal("core configuration binding widened exact Task scope", err)
		}
	}
}
