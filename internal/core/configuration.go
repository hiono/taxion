package core

import (
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strings"
	"time"
)

// DispatchConfiguration records a selected tuple, not external authentication
// or permission to execute. Selection contains identifiers and protected refs.
type DispatchConfiguration struct {
	DecisionID string            `json:"decisionId"`
	Selection  json.RawMessage   `json:"selection"`
	Sources    map[string]string `json:"sources"`
	RecordedAt time.Time         `json:"recordedAt"`
}

func (binding DispatchConfiguration) validate(dispatch GraphDispatch) error {
	if !nonempty(binding.DecisionID) || binding.RecordedAt.IsZero() {
		return graphError("configuration binding requires a decision and recording time")
	}
	var selection map[string]any
	if err := json.Unmarshal(binding.Selection, &selection); err != nil || len(selection) == 0 {
		return graphError("configuration binding requires a selected JSON object")
	}
	repository, repoOK := selection["repository"].(map[string]any)
	execution, executionOK := selection["execution"].(map[string]any)
	if !repoOK || !executionOK || repository["path"] != dispatch.Repository ||
		execution["approvalRef"] != binding.DecisionID {
		return graphError("configuration binding identity differs from the Dispatch")
	}
	paths := map[string]bool{}
	var collect func(map[string]any, string) error
	collect = func(fields map[string]any, prefix string) error {
		for key, value := range fields {
			path := prefix + key
			if children, ok := value.(map[string]any); ok {
				if len(children) == 0 {
					return graphError("configuration binding has an empty section")
				}
				if err := collect(children, path+"."); err != nil {
					return err
				}
				continue
			}
			paths[path] = true
			switch binding.Sources[path] {
			case "user", "project", "environment":
			default:
				return graphError("configuration binding requires field origins")
			}
		}
		return nil
	}
	if err := collect(selection, ""); err != nil {
		return err
	}
	if len(paths) != len(binding.Sources) {
		return graphError("configuration binding contains unrelated field origins")
	}
	for _, decision := range dispatch.Instruction.Decisions {
		if decision.ID != binding.DecisionID || !nonempty(decision.Source) {
			continue
		}
		var approval struct {
			Kind              string           `json:"kind"`
			TaskID            string           `json:"taskId"`
			AdditionalTaskIDs []string         `json:"additionalTaskIds,omitempty"`
			Reason            string           `json:"reason"`
			Allowed           []map[string]any `json:"allowed"`
		}
		if err := DecodeGraphInput(strings.NewReader(decision.Text), &approval); err != nil ||
			approval.Kind != "taxion.execution-config.v1" || !nonempty(approval.TaskID) || !nonempty(approval.Reason) {
			return graphError("configuration binding requires a saved configuration approval")
		}
		taskAllowed := approval.TaskID == dispatch.TaskID
		seen := map[string]bool{approval.TaskID: true}
		for _, id := range approval.AdditionalTaskIDs {
			if id == "" || strings.ContainsAny(id, " \t\r\n*?[]") || seen[id] {
				return graphError("configuration approval requires unique exact additional Task IDs")
			}
			seen[id] = true
			taskAllowed = taskAllowed || id == dispatch.TaskID
		}
		if !taskAllowed {
			return graphError("configuration approval does not name this Dispatch Task")
		}
		for _, allowed := range approval.Allowed {
			if reflect.DeepEqual(selection, allowed) {
				return nil
			}
		}
	}
	return graphError("configuration binding differs from the saved Dispatch approval")
}

// BindDispatchConfiguration cannot attach new settings to a legacy SDK session.
func (g *TaskGraph) BindDispatchConfiguration(dispatchID string, binding DispatchConfiguration) error {
	dispatch, ok := g.GraphDispatch(dispatchID)
	task, taskOK := g.GraphTask(dispatch.TaskID)
	if !ok || !taskOK || task.Status != Active || !dispatchActive(dispatch.Status) ||
		dispatch.Status == DispatchCancelRequested || dispatch.Status == DispatchSuspended {
		return graphError("configuration binding requires an executable Dispatch")
	}
	if err := binding.validate(dispatch); err != nil {
		return err
	}
	matched := false
	for _, current := range task.Decisions {
		for _, saved := range dispatch.Instruction.Decisions {
			matched = matched || current.ID == binding.DecisionID && current == saved
		}
	}
	if !matched || task.HumanDecision != nil && task.HumanDecision.SelectedOptionID == "" {
		return graphError("configuration binding requires the current saved approval")
	}
	if dispatch.Configuration != nil {
		if reflect.DeepEqual(dispatch.Configuration.Selection, binding.Selection) &&
			dispatch.Configuration.DecisionID == binding.DecisionID {
			return nil
		}
		return graphError("Dispatch configuration is already selected; issue a newly authorized Dispatch")
	}
	if dispatch.RuntimeSessionID != "" {
		return graphError("legacy session has no configuration selection; issue a new Dispatch")
	}
	next := cloneGraph(*g)
	for i := range next.Dispatches {
		if next.Dispatches[i].ID == dispatchID {
			next.Dispatches[i].Configuration = cloneDispatchConfiguration(&binding)
		}
	}
	if err := next.Validate(); err != nil {
		return err
	}
	*g = next
	return nil
}

func cloneDispatchConfiguration(binding *DispatchConfiguration) *DispatchConfiguration {
	if binding == nil {
		return nil
	}
	copy := *binding
	copy.Selection = slices.Clone(binding.Selection)
	copy.Sources = maps.Clone(binding.Sources)
	return &copy
}
