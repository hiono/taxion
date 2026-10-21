package config

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/hiono/taxion/internal/core"
	"gopkg.in/yaml.v3"
)

const ApprovalKind = "taxion.execution-config.v1"

// Approval is stored as structured JSON in a sourced Task decision. Allowed
// entries are complete tuples so independent lists cannot widen authorization.
type Approval struct {
	Kind              string   `json:"kind"`
	TaskID            string   `json:"taskId"`
	AdditionalTaskIDs []string `json:"additionalTaskIds,omitempty"`
	Reason            string   `json:"reason"`
	DecisionID        string   `json:"decisionId,omitempty"`
	AuthorizationRef  string   `json:"authorizationReference,omitempty"`
	ApprovalRef       string   `json:"approvalRef,omitempty"`
	Allowed           []Config `json:"allowed"`
}

func CheckApproval(input string, taskID, decisionID string, candidate Config) error {
	if err := candidate.Validate(); err != nil {
		return err
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
	taskAllowed := approval.TaskID == taskID
	seen := map[string]bool{approval.TaskID: true}
	for _, id := range approval.AdditionalTaskIDs {
		if id == "" || strings.ContainsAny(id, " \t\r\n*?[]") || seen[id] {
			return fmt.Errorf("execution approval requires unique exact additional Task IDs without whitespace or patterns")
		}
		seen[id] = true
		taskAllowed = taskAllowed || id == taskID
	}
	if approval.Kind != ApprovalKind || taskID == "" || approval.TaskID == "" || !taskAllowed ||
		strings.TrimSpace(approval.Reason) == "" || len(approval.Allowed) == 0 {
		return fmt.Errorf("execution approval requires matching kind, Task ID, reason and allowed configurations")
	}
	if approval.DecisionID != "" || approval.AuthorizationRef != "" || approval.ApprovalRef != "" {
		if approval.DecisionID != decisionID || strings.TrimSpace(approval.AuthorizationRef) == "" ||
			approval.ApprovalRef != candidate.Execution.ApprovalRef ||
			decisionID != core.ConfigurationReauthorizationID(approval.ApprovalRef, approval.TaskID, approval.AuthorizationRef) {
			return fmt.Errorf("configuration approval reference differs from the selected decision")
		}
	} else if decisionID != candidate.Execution.ApprovalRef &&
		decisionID != core.ConfigurationApprovalID(candidate.Execution.ApprovalRef, approval.TaskID) {
		return fmt.Errorf("configuration approval reference differs from the selected decision")
	}
	matched := false
	for _, allowed := range approval.Allowed {
		if err := allowed.Validate(); err != nil {
			return fmt.Errorf("execution approval contains an invalid configuration: %w", err)
		}
		if allowed.Execution.ApprovalRef != candidate.Execution.ApprovalRef {
			return fmt.Errorf("execution approval contains a different decision reference")
		}
		matched = matched || allowed == candidate
	}
	if !matched {
		return fmt.Errorf("configuration is outside the recorded approval; obtain Human authorization for the selected combination")
	}
	return nil
}
