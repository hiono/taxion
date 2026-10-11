package config

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

const ApprovalKind = "taxion.execution-config.v1"

// Approval is stored as structured JSON in a sourced Task decision. Allowed
// entries are complete tuples so independent lists cannot widen authorization.
type Approval struct {
	Kind    string   `json:"kind"`
	TaskID  string   `json:"taskId"`
	Reason  string   `json:"reason"`
	Allowed []Config `json:"allowed"`
}

func CheckApproval(input string, taskID, decisionID string, candidate Config) error {
	if err := candidate.Validate(); err != nil {
		return err
	}
	if candidate.Execution.ApprovalRef != decisionID {
		return fmt.Errorf("configuration approval reference differs from the selected decision")
	}
	var approval Approval
	decoder := json.NewDecoder(strings.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&approval); err != nil {
		return fmt.Errorf("execution approval requires structured configuration authorization")
	}
	var uniqueKeys map[string]any
	if err := yaml.Unmarshal([]byte(input), &uniqueKeys); err != nil {
		return fmt.Errorf("execution approval requires unique JSON keys")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("execution approval requires exactly one JSON object")
	}
	if approval.Kind != ApprovalKind || taskID == "" || approval.TaskID != taskID ||
		strings.TrimSpace(approval.Reason) == "" || len(approval.Allowed) == 0 {
		return fmt.Errorf("execution approval requires matching kind, Task ID, reason and allowed configurations")
	}
	matched := false
	for _, allowed := range approval.Allowed {
		if err := allowed.Validate(); err != nil {
			return fmt.Errorf("execution approval contains an invalid configuration: %w", err)
		}
		if allowed.Execution.ApprovalRef != decisionID {
			return fmt.Errorf("execution approval contains a different decision reference")
		}
		matched = matched || allowed == candidate
	}
	if !matched {
		return fmt.Errorf("configuration is outside the recorded approval; obtain Human authorization for the selected combination")
	}
	return nil
}
