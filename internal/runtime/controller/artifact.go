package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/gitproof"
	"github.com/hiono/taxion/internal/store"
)

func startArtifactReview(state *store.Workspace, flow *core.ReviewFlow, request ReviewFlowRequest, planned *core.ArtifactTarget) error {
	if flow.StagePlan == nil || planned == nil || *request.MaxRepairs != 0 || request.ContinueFromDispatchID != "" ||
		flow.Contract.OutputMode != core.OutputModeWorktreeTreeSnapshot || len(flow.Contract.ValidationCommands) == 0 {
		return fmt.Errorf("taxion: artifact review requires an approved snapshot stage plan, validation commands and zero repairs")
	}
	if _, enabled := state.Records.PacketAuthorization(flow.TaskID); enabled {
		return fmt.Errorf("taxion: artifact-first packet lifecycle delegation is not supported; use a separately approved Task without optional packet delegation")
	}
	if time.Until(flow.Deadline) < time.Duration(flow.StagePlan.ValidationSeconds+flow.StagePlan.ReviewSeconds)*time.Second {
		return fmt.Errorf("taxion: caller deadline cannot fit approved input validation and independent review")
	}
	if strings.TrimSpace(request.ArtifactSource) != request.ArtifactSource ||
		core.Redact(request.ArtifactSource) != request.ArtifactSource {
		return fmt.Errorf("taxion: artifact source must be a non-secret provenance reference")
	}
	target := *planned
	if err := gitproof.RetainArtifact(flow.Contract, target, flow.ID, request.Configuration.Selection); err != nil {
		return err
	}
	flow.Artifact = &core.ArtifactReview{
		Target: target, Configuration: request.Configuration, ObservedAt: time.Now().UTC(),
		Validation: core.ArtifactValidation{Status: "PENDING", TargetHash: target.Hash, Commands: []core.DispatchValidation{}},
	}
	if request.ReuseArtifactValidation {
		prior, err := validatedArtifactSource(state.Graph, flow.Contract, request)
		if err != nil {
			return err
		}
		var hash string
		for _, decision := range flow.Contract.Decisions {
			if decision.ID == "reconciled-input:"+flow.TaskID {
				hash = decision.Text
			}
		}
		audit, err := reconciledInput(*state, prior.ID, hash)
		if err != nil || !audit.PreviousFlow.StoppedArtifactReviewer(audit.PreviousDispatch) {
			return fmt.Errorf("taxion: validation reuse requires its exact proposal-bound Reviewer reconciliation")
		}
		if err := gitproof.VerifyArtifact(prior.Contract, prior.Artifact.Target, request.Configuration.Selection); err != nil {
			return err
		}
		inherited := *prior.Artifact
		inherited.ValidationSourceFlowID = prior.ID
		flow.Artifact = &inherited
	}
	flow.Budget.MaxDispatches, flow.Budget.MaxCoderSessions, flow.Budget.MaxReviewerSessions = 1, 0, 1
	return state.Graph.Transition(flow.TaskID, core.Active, "controller",
		"accept unverified input artifact; no implementation or quality success is asserted", core.TransitionProof{})
}

// The execution marker commits before arbitrary approved commands run. An
// interrupted RUNNING receipt is unknown execution, never permission to replay.
func runArtifactValidation(ctx context.Context, db *store.Store, ledgerPath, id, runnerID string,
	configuration core.ApprovalConfiguration) error {
	var flow core.ReviewFlow
	execute := false
	err := db.UpdateWorkspace(func(state *store.Workspace) error {
		var ok bool
		flow, ok = state.Graph.ReviewFlow(id)
		if !ok || flow.RunnerID != runnerID || flow.Status != "RUNNING" || flow.Artifact == nil ||
			len(flow.DispatchIDs) != 0 || flow.HandoffHeld() {
			return fmt.Errorf("taxion: artifact validation requires the running owner at an unheld input boundary")
		}
		if state.Records.Restore != nil && slices.Contains(state.Records.Restore.TaskIDs, flow.TaskID) {
			return fmt.Errorf("taxion: restored artifact execution requires a newly approved successor")
		}
		task, _ := state.Graph.GraphTask(flow.TaskID)
		if !flowContractMatches(state.Graph, flow, task) || task.Status != core.Active {
			return fmt.Errorf("taxion: artifact validation contract or Task state changed")
		}
		switch state.Graph.ApprovalRequirement() {
		case core.ApprovalBundleRequired:
			if _, ok := state.Graph.MatchingApproval(flow.TaskID, flow.Contract, configuration); !ok {
				return fmt.Errorf("taxion: input validation lacks its exact saved contract/configuration ApprovalBundle")
			}
		case core.ApprovalVerifiedLegacy:
		default:
			return fmt.Errorf("taxion: input validation approval classification is invalid or ambiguous")
		}
		if !reflect.DeepEqual(flow.Artifact.Configuration, configuration) {
			return fmt.Errorf("taxion: artifact execution configuration differs from the approved plan")
		}
		if len(configuration.Selection) != 0 {
			var selected config.Config
			if err := json.Unmarshal(configuration.Selection, &selected); err != nil {
				return err
			}
			decision, err := checkTaskConfiguration(state.Graph, flow.TaskID, selected)
			if err != nil {
				return err
			}
			if err := config.CheckApproval(decision.Text, flow.ID+"-review-0", decision.ID, selected); err != nil {
				return fmt.Errorf("taxion: artifact Reviewer configuration is not covered: %w", err)
			}
		}
		if err := state.Graph.CheckTaskSpecificationUse(flow.TaskID, "runtime"); err != nil {
			return err
		}
		if err := verifyStagePlanBinding(state.Records, flow); err != nil {
			return err
		}
		if err := requireBudgetRoom(state.Graph, flow, "REVIEWER"); err != nil {
			return err
		}
		switch flow.Artifact.Validation.Status {
		case "PASSED":
			return nil
		case "PENDING":
			flow.Artifact.Validation.Status, flow.Artifact.Validation.StartedAt = "RUNNING", time.Now().UTC()
			execute = true
			storeFlow(state, flow)
			return appendBudgetAudit(state, flow, "INPUT_VALIDATION_STARTED", taskBudgetReport(state.Graph, flow, time.Now().UTC()))
		default:
			return fmt.Errorf("taxion: saved input validation is %s; preserve evidence and obtain a successor decision, do not replay commands",
				flow.Artifact.Validation.Status)
		}
	})
	if err != nil {
		return err
	}
	if execute {
		deadline := flow.StartedAt.Add(time.Duration(flow.StagePlan.ValidationSeconds) * time.Second)
		reserved := flow.Deadline.Add(-time.Duration(flow.StagePlan.ReviewSeconds) * time.Second)
		if reserved.Before(deadline) {
			deadline = reserved
		}
		bounded, cancel := context.WithDeadline(ctx, deadline)
		checks, validationErr := gitproof.ValidateArtifact(bounded, flow.Contract, flow.Artifact.Target, configuration.Selection)
		cancel()
		for i := range checks {
			checks[i].Output = core.Redact(checks[i].Output)
		}
		saveErr := db.UpdateWorkspace(func(state *store.Workspace) error {
			current, ok := state.Graph.ReviewFlow(id)
			if !ok || current.RunnerID != runnerID || current.Artifact == nil ||
				current.Artifact.Validation.Status != "RUNNING" {
				return fmt.Errorf("taxion: artifact validation ownership changed; execution outcome must be reconciled")
			}
			v := &current.Artifact.Validation
			v.Commands, v.FinishedAt, v.Status = checks, time.Now().UTC(), "PASSED"
			if validationErr != nil {
				v.Status, v.Error = "FAILED", core.Redact(validationErr.Error())
			}
			storeFlow(state, current)
			return appendBudgetAudit(state, current, "INPUT_VALIDATION_FINISHED", taskBudgetReport(state.Graph, current, time.Now().UTC()))
		})
		if err := errors.Join(validationErr, saveErr); err != nil {
			return err
		}
	}
	return db.UpdateWorkspace(func(state *store.Workspace) error {
		current, ok := state.Graph.ReviewFlow(id)
		if !ok || current.RunnerID != runnerID || current.Status != "RUNNING" || current.HandoffHeld() ||
			current.Artifact == nil || current.Artifact.Validation.Status != "PASSED" || len(current.DispatchIDs) != 0 {
			return fmt.Errorf("taxion: independent review requires the unchanged successful input validation boundary")
		}
		if err := gitproof.VerifyArtifact(current.Contract, current.Artifact.Target, configuration.Selection); err != nil {
			return err
		}
		if err := issueArtifactReviewer(state, ledgerPath, &current, configuration); err != nil {
			return err
		}
		storeFlow(state, current)
		return nil
	})
}

func issueArtifactReviewer(state *store.Workspace, ledgerPath string, flow *core.ReviewFlow,
	configuration core.ApprovalConfiguration) error {
	a := flow.Artifact
	proof := core.TransitionProof{
		ActionPerformed: true, OutputSHA: a.Target.CommitSHA, OutputTreeSHA: a.Target.TreeSHA,
		OutputChangedFiles: a.Target.ChangedFiles, Evidence: []string{"validated-input:" + a.Target.Hash},
	}
	if err := state.Graph.Transition(flow.TaskID, core.WaitingForReview, "controller",
		"actual Controller validation of supplied input completed; no Coder result is asserted", proof); err != nil {
		return err
	}
	review := flow.Contract
	review.ID = flow.ID + "-review-0"
	review.Title, review.Type, review.Owner, review.Reviewer, review.Status = "Independent supplied-input review", "REVIEW", "REVIEWER", "", core.Draft
	review.CreatedFrom, review.Supersedes, review.ParentID, review.DependsOn = "artifact:"+a.Target.Hash, "", "", nil
	review.BaselineSHA, review.TargetSHA = a.Target.CommitSHA, a.Target.CommitSHA
	review.TargetTreeSHA, review.TargetChangedFiles = a.Target.TreeSHA, slices.Clone(a.Target.ChangedFiles)
	review.Worktree = filepath.Join(flow.ReviewRoot, review.ID)
	review.AuthorizationID = flow.Authorization.ID
	review.Decisions = slices.DeleteFunc(slices.Clone(review.Decisions), func(d core.TaskDecision) bool {
		return strings.HasPrefix(d.ID, "intake-confirmation:")
	})
	receipt, err := json.Marshal(a)
	if err != nil {
		return err
	}
	review.Decisions = append(review.Decisions, flow.Authorization, core.TaskDecision{
		ID: flow.ArtifactDecisionID(), Source: "flow:" + flow.ID, Text: string(receipt),
	})
	if err := state.Graph.Track(review, "controller", "derive same-contract Reviewer for validated supplied input"); err != nil {
		return err
	}
	state.Ledger.Tasks = append(state.Ledger.Tasks, core.Task{
		ID: review.ID, Title: review.Title, Repo: review.Repo, CreatedAt: time.Now().UTC(),
	})
	if err := state.Graph.Transition(review.ID, core.Ready, "controller", "bounded independent review authorized by exact artifact plan", core.TransitionProof{}); err != nil {
		return err
	}
	review, _ = state.Graph.GraphTask(review.ID)
	if state.Graph.ApprovalRequirement() == core.ApprovalBundleRequired {
		source, ok := state.Graph.MatchingApproval(flow.TaskID, flow.Contract, configuration)
		if !ok {
			return fmt.Errorf("taxion: input review lacks its original ApprovalBundle")
		}
		bundle, err := core.ComposeApprovalBundleWithProposalHash("artifact-reviewer:"+review.ID, source.ProposalHash,
			review, configuration, "controller", flow.Authorization.Source,
			"derived same-target review under the exact approved input stage plan "+flow.StagePlan.Hash, time.Now().UTC())
		if err != nil {
			return err
		}
		if err := state.Graph.AppendApproval(bundle); err != nil {
			return err
		}
	}
	dispatchID := fmt.Sprintf("%s-dispatch-%d", flow.ID, len(flow.DispatchIDs))
	if err := gitproof.RetainArtifact(flow.Contract, a.Target, dispatchID, configuration.Selection); err != nil {
		return err
	}
	return issueFlowDispatch(state, ledgerPath, flow, review, core.RelationshipNone, "", "", configuration)
}
