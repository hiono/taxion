package controller

import (
	"fmt"

	"github.com/hiono/taxion/internal/core"
)

// DispatchResultSchema describes model output; the controller independently
// verifies Git, validation commands, and references between evidence fields.
func DispatchResultSchema(dispatch core.GraphDispatch, strict bool) map[string]any {
	properties := map[string]any{
		"dispatchId":   map[string]any{"type": "string"},
		"taskId":       map[string]any{"type": "string"},
		"recipient":    map[string]any{"type": "string"},
		"repository":   map[string]any{"type": "string"},
		"status":       map[string]any{"type": "string", "enum": []string{"SUCCEEDED", "FAILED", "SUSPENDED"}},
		"startedSha":   map[string]any{"type": "string"},
		"outputSha":    map[string]any{"type": "string"},
		"changedFiles": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"validations": map[string]any{"type": "array", "items": map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"command":  map[string]any{"type": "string"},
				"exitCode": map[string]any{"type": "integer"},
				"worktree": map[string]any{"type": "string"},
				"sha":      map[string]any{"type": "string"},
			},
			"required": []string{"command", "exitCode", "worktree", "sha"},
		}},
		"evidence": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
			"description": fmt.Sprintf("Evidence identifiers, not narrative reports. For SUCCEEDED, include every required identifier as a separate exact array item: %q. Do not append descriptions to these identifiers; additional evidence may be separate items. Acceptance and finding evidence must reference exact strings present in this array.", dispatch.Instruction.RequiredEvidence)},
		"blockers":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"nextTransition":  map[string]any{"type": "string"},
		"resumeCondition": map[string]any{"type": "string"},
	}
	required := []string{
		"dispatchId", "taskId", "recipient", "repository", "status", "startedSha", "outputSha",
		"changedFiles", "validations", "evidence", "blockers", "nextTransition", "resumeCondition",
	}
	if dispatch.Recipient == "REVIEWER" {
		properties["changedFiles"] = map[string]any{
			"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 0,
			"description": "Reviewer is read-only: return []. The reproduced Coder diff is the review input, not a change made by Reviewer. Report any actual Reviewer mutation using worktreeChanged and blockers; the controller independently verifies the tree.",
		}
		references := map[string]any{"type": "array", "items": map[string]any{"type": "string"},
			"description": "Exact identifiers already present in result.evidence, including identical spelling and punctuation."}
		properties["reviewVerdict"] = map[string]any{"type": "string", "enum": []string{"APPROVE", "REQUEST_CHANGES", "BLOCKED"}}
		properties["acceptance"] = map[string]any{"type": "array", "items": map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"criterion": map[string]any{"type": "string", "enum": dispatch.Instruction.Acceptance},
				"status":    map[string]any{"type": "string", "enum": []string{"PASS", "FAIL", "BLOCKED"}},
				"evidence":  references,
			},
			"required": []string{"criterion", "status", "evidence"},
		}}
		properties["findings"] = map[string]any{"type": "array", "items": map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"severity": map[string]any{"type": "string", "enum": []string{"CRITICAL", "HIGH", "MEDIUM", "LOW", "INFO"}},
				"title":    map[string]any{"type": "string"},
				"file":     map[string]any{"type": "string"},
				"lines":    map[string]any{"type": "string"},
				"evidence": references,
			},
			"required": []string{"severity", "title", "file", "lines", "evidence"},
		}}
		properties["worktreeChanged"] = map[string]any{"type": "boolean"}
		required = append(required, "reviewVerdict", "acceptance", "findings", "worktreeChanged")
	}
	if strict {
		properties["nextTransition"] = map[string]any{"type": "string", "enum": []core.TaskStatus{
			core.Draft, core.Ready, core.Active, core.WaitingForEvidence, core.WaitingForReview,
			core.RequestChanges, core.Blocked, core.HumanDecisionRequired, core.Approved,
			core.Completed, core.Rejected, core.Cancelled, core.Superseded,
		}}
		for field, value := range map[string]string{
			"dispatchId": dispatch.ID, "taskId": dispatch.TaskID, "recipient": dispatch.Recipient,
			"repository": dispatch.Repository, "startedSha": dispatch.ExpectedStartSHA(),
		} {
			properties[field] = map[string]any{"type": "string", "enum": []string{value}}
		}
		if dispatch.Instruction.OutputMode == core.OutputModeWorktreeTreeSnapshot {
			properties["outputSha"] = map[string]any{"type": "string", "enum": []string{dispatch.ExpectedStartSHA()}}
		}
	}
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": properties, "required": required,
	}
}
