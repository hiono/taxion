package controller

import (
	"bytes"
	"fmt"
	"path/filepath"
	"time"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

// Internal role transitions derive authority from the saved contract and
// explicit configuration coverage, not from success or metadata alone.
func appendFlowEntryApproval(state *store.Workspace, flow core.ReviewFlow, taskID string,
	stage core.ApprovalEntryStage, configuration core.ApprovalConfiguration) error {
	if state.Graph.ApprovalRequirement() == core.ApprovalVerifiedLegacy {
		return nil
	}
	task, ok := state.Graph.GraphTask(taskID)
	if !ok || task.Status != core.Ready {
		return fmt.Errorf("taxion: role admission requires a READY Task")
	}
	if _, ok := state.Graph.MatchingApproval(taskID, task, configuration); ok {
		return nil
	}
	if state.Graph.ApprovalRequirement() != core.ApprovalBundleRequired {
		return fmt.Errorf("taxion: role admission approval schema is ambiguous")
	}
	if _, enabled := state.Records.PacketAuthorization(flow.TaskID); enabled {
		return checkEntryApproval(state.Graph, task, stage, configuration)
	}
	source, ok := state.Graph.MatchingApproval(flow.TaskID, flow.Contract, configuration)
	if !ok {
		return checkEntryApproval(state.Graph, task, stage, configuration)
	}
	decision, err := core.NewInputDecision(struct {
		Source string
		Task   core.GraphTask
		Stage  core.ApprovalEntryStage
	}{source.BundleHash, task, stage})
	if err != nil {
		return err
	}
	decision.Metadata = "verified"
	block := func(code, field, reason string) {
		decision.Block(core.InputCondition{
			Code: code, Field: field, Reason: reason, EvidenceSource: "approval:" + source.ID,
			Resolution:       "use the exact saved contract and explicitly covered role configuration",
			RequiresApproval: true,
		})
	}
	expected, err := core.BundleDimensions(flow.Contract, configuration)
	if err != nil {
		return err
	}
	actual, err := core.BundleDimensions(task, configuration)
	if err != nil {
		return err
	}
	if task.Repo != flow.Contract.Repo || task.Objective != flow.Contract.Objective ||
		task.OutputMode != flow.Contract.OutputMode ||
		actual.Scope != expected.Scope || actual.Acceptance != expected.Acceptance ||
		actual.Budget != expected.Budget || actual.Security != expected.Security {
		block("role-contract-drift", "contract", "derived role differs from the saved contract")
	}
	switch stage {
	case core.EntryReviewerDispatch, core.EntryReReviewDispatch:
		if task.ID != fmt.Sprintf("%s-review-%d", flow.ID, flow.Repairs) ||
			task.Worktree != filepath.Join(flow.ReviewRoot, task.ID) ||
			task.Type != "REVIEW" || task.Owner != "REVIEWER" || task.Reviewer != "" ||
			flow.Repairs < 0 || flow.Repairs > flow.MaxRepairs {
			block("role-handoff-mismatch", "role", "Reviewer is outside the bounded role handoff")
		}
	case core.EntryRepairDispatch:
		if task.ID != flow.TaskID || task.Owner != "CODER" || task.Type != flow.Contract.Type ||
			task.Reviewer != flow.Contract.Reviewer ||
			task.Worktree != flow.Contract.Worktree || task.Branch != flow.Contract.Branch ||
			task.BaselineSHA != flow.Contract.BaselineSHA || task.AuthorizationID != flow.Contract.AuthorizationID ||
			flow.Repairs <= 0 || flow.Repairs > flow.MaxRepairs {
			block("role-handoff-mismatch", "role", "repair is outside the bounded role handoff")
		}
	default:
		block("role-not-delegated", "role", "this entry requires its own existing approval")
	}
	var selected config.Config
	if err := core.DecodeGraphInput(bytes.NewReader(configuration.Selection), &selected); err != nil {
		return err
	}
	if _, err := checkTaskConfiguration(state.Graph, taskID, selected); err != nil {
		block("role-configuration-not-covered", "configuration", err.Error())
	}
	if err := decision.Err(); err != nil {
		return err
	}
	bundle, err := core.ComposeApprovalBundleWithProposalHash(
		fmt.Sprintf("role-entry:%s:%s:%d", flow.ID, stage, flow.Repairs), source.ProposalHash,
		task, configuration, "controller", "approval:"+source.ID,
		"same-contract role admitted under saved flow authorization "+flow.Authorization.ID, time.Now().UTC())
	if err != nil {
		return err
	}
	return state.Graph.AppendApproval(bundle)
}
