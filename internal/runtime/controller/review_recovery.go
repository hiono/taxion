package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

// ReviewRecoveryRequest binds one human decision to the entire review-only
// successor. Subordinate hashes are derived, never separate consent requests.
type ReviewRecoveryRequest struct {
	ContinueStoppedExecution bool                            `json:"continueStoppedExecution,omitempty"`
	DeadlineRecontract       *store.ReviewDeadlineRecontract `json:"deadlineRecontract,omitempty"`
	Reconciliation           DispatchReconciliationRequest   `json:"reconciliation"`
	TaskID                   string                          `json:"taskId"`
	FlowID                   string                          `json:"flowId"`
	ReviewRoot               string                          `json:"reviewRoot"`
	Deadline                 time.Time                       `json:"deadline"`
	TimeoutSeconds           int64                           `json:"timeoutSeconds"`
	MaxNanoAIU               float64                         `json:"maxNanoAiu"`
	StageBudget              core.StageBudget                `json:"stageBudget"`
	ExpectedHash             string                          `json:"expectedHash,omitempty"`
}

type ReviewRecoveryPreview struct {
	Version        int                           `json:"version"`
	StateChanged   bool                          `json:"stateChanged"`
	Hash           string                        `json:"hash"`
	Request        ReviewRecoveryRequest         `json:"request"`
	Reconciliation DispatchReconciliationPreview `json:"reconciliation"`
	Proposal       core.GraphTask                `json:"proposal"`
	Plan           StagePlanPreview              `json:"plan"`
	Flow           core.ReviewFlow               `json:"flow"`
}

// RecoverArtifactReview stages existing controller operations in an isolated
// ledger and commits their validated state atomically. No worker or host
// validation is executed, and the original worktrees are only observed.
func RecoverArtifactReview(db *store.Store, ledgerPath string, request ReviewRecoveryRequest, preview bool) (ReviewRecoveryPreview, error) {
	if db == nil || request.TaskID == "" || request.FlowID == "" ||
		request.Reconciliation.ExpectedHash != "" || request.TimeoutSeconds <= 0 ||
		request.TimeoutSeconds > int64(365*24*time.Hour/time.Second) ||
		!request.Deadline.After(time.Now()) {
		return ReviewRecoveryPreview{}, fmt.Errorf("taxion: review recovery requires new identities, bounded timeout, future absolute deadline and no subordinate approval hash")
	}
	expected := request.ExpectedHash
	request.ExpectedHash = ""
	before, err := db.LoadWorkspace()
	if err != nil {
		return ReviewRecoveryPreview{}, err
	}
	hash, err := core.CanonicalSHA256(struct {
		State   store.Workspace
		Request ReviewRecoveryRequest
	}{before, request})
	if err != nil {
		return ReviewRecoveryPreview{}, err
	}
	if !preview && (expected == "" || expected != hash) {
		return ReviewRecoveryPreview{}, fmt.Errorf("taxion: review recovery changed; approve the one exact --preview hash")
	}
	report, after, err := stageReviewRecovery(before, ledgerPath, request, hash)
	if err != nil || preview {
		return report, err
	}
	err = db.UpdateWorkspace(func(current *store.Workspace) error {
		currentHash, err := core.CanonicalSHA256(struct {
			State   store.Workspace
			Request ReviewRecoveryRequest
		}{*current, request})
		if err != nil || currentHash != hash {
			return fmt.Errorf("taxion: review recovery state changed; request a fresh combined preview")
		}
		// Recheck mutable Git inputs at the commit boundary, not just in staging.
		if _, err := previewReviewRecoverySource(*current, request); err != nil {
			return err
		}
		if !time.Now().Before(after.Graph.ReviewFlows[len(after.Graph.ReviewFlows)-1].Deadline) {
			return fmt.Errorf("taxion: review recovery deadline elapsed before commit")
		}
		*current = after
		return nil
	})
	report.StateChanged = err == nil
	return report, err
}

func stageReviewRecovery(before store.Workspace, ledger string, request ReviewRecoveryRequest, hash string) (ReviewRecoveryPreview, store.Workspace, error) {
	fail := func(err error) (ReviewRecoveryPreview, store.Workspace, error) {
		return ReviewRecoveryPreview{}, store.Workspace{}, err
	}
	old, found := before.Graph.ReviewFlow(request.Reconciliation.FlowID)
	dispatch, exists := before.Graph.GraphDispatch(request.Reconciliation.DispatchID)
	eligible := old.StoppedArtifactReviewer(dispatch)
	if request.ContinueStoppedExecution {
		eligible = old.StoppedExecutedArtifactReviewer(dispatch)
	}
	if !found || !exists || !eligible || request.FlowID == old.ID ||
		request.TaskID == old.TaskID || request.StageBudget.CoderSessionCredits != 0 {
		return fail(fmt.Errorf("taxion: combined recovery requires the first stopped validated artifact Reviewer, new identities and zero Coder allocation"))
	}
	// Do not alias the caller's snapshots while running normal controller writes.
	data, err := json.Marshal(before)
	if err != nil {
		return fail(err)
	}
	var isolated store.Workspace
	if err := json.Unmarshal(data, &isolated); err != nil {
		return fail(err)
	}
	staged, err := store.Open(":memory:")
	if err != nil {
		return fail(err)
	}
	defer staged.Close()
	if err := staged.UpdateWorkspace(func(s *store.Workspace) error { *s = isolated; return nil }); err != nil {
		return fail(err)
	}
	decision := request.Reconciliation
	// Preserve the original decision text and explicitly link derived records to
	// the single outer approval, without pretending each hash was human-entered.
	decision.Reason += "; combined-review-approval:" + hash
	reconciliation, err := previewReviewRecoverySource(before, request)
	if err != nil {
		return fail(err)
	}
	if request.ContinueStoppedExecution {
		reconciliation.Hash = hash
		if err := recordExecutedReviewContinuation(staged, request, hash); err != nil {
			return fail(err)
		}
	} else {
		preview, err := ReconcileStoppedDispatch(staged, decision, true)
		if err != nil {
			return fail(err)
		}
		reconciliation = preview
		decision.ExpectedHash = reconciliation.Hash
		if _, err := reconcileStoppedDispatch(staged, decision, false, request.DeadlineRecontract); err != nil {
			return fail(err)
		}
	}
	state, err := staged.LoadWorkspace()
	if err != nil {
		return fail(err)
	}
	cancelled, _ := state.Graph.GraphDispatch(dispatch.ID)
	target, err := core.ObserveFeedbackTarget(cancelled)
	if err != nil {
		return fail(err)
	}
	task := old.Contract
	task.ID, task.Status, task.AuthorizationID, task.Decisions = request.TaskID, core.Draft, "", nil
	task.CreatedFrom, task.Supersedes = "flow:"+old.ID, old.TaskID
	task.TargetSHA, task.TargetTreeSHA, task.TargetChangedFiles = old.Artifact.Target.CommitSHA, old.Artifact.Target.TreeSHA, old.Artifact.Target.ChangedFiles
	input, err := core.ReviewInputFromDecisions(old.Contract.Decisions)
	if err != nil {
		return fail(err)
	}
	proposal, err := ProposeIntake(staged, IntakeRequest{
		ReviewInput: input,
		Task:        task, Request: "Review unchanged validated input; combined-review-approval:" + hash,
		Actor: decision.Actor, Source: decision.Source, ReconciliationHash: reconciliation.Hash,
	})
	if err != nil {
		return fail(err)
	}
	proposalHash, err := IntakeHash(proposal)
	if err != nil {
		return fail(err)
	}
	var selection config.Config
	if err := core.DecodeGraphInput(strings.NewReader(string(old.Artifact.Configuration.Selection)), &selection); err != nil {
		return fail(err)
	}
	if _, err := ConfirmIntake(staged, IntakeConfirmation{
		TaskID: task.ID, ExpectedHash: proposalHash, Actor: decision.Actor, Source: decision.Source,
		Reason: decision.Reason, Selection: selection, ConfigurationDecisionID: selection.Execution.ApprovalRef + "@task:" + task.ID,
		AdditionalTaskIDs: []string{request.FlowID + "-review-0"}, ReplacesFlow: old.ID,
		Target: &target, ReconciliationHash: reconciliation.Hash,
	}); err != nil {
		return fail(err)
	}
	zero := 0
	authority, err := json.Marshal(struct {
		Kind    string                `json:"kind"`
		Hash    string                `json:"hash"`
		Request ReviewRecoveryRequest `json:"request"`
	}{"combined-review-approval", hash, request})
	if err != nil {
		return fail(err)
	}
	next := ReviewFlowRequest{
		ID: request.FlowID, TaskID: task.ID, Repository: task.Repo, ReviewRoot: request.ReviewRoot,
		MaxRepairs: &zero, MaxNanoAIU: request.MaxNanoAIU, TimeoutSeconds: request.TimeoutSeconds,
		Configuration: old.Artifact.Configuration, StageBudget: &request.StageBudget,
		ArtifactSource: old.Artifact.Target.Source, ReuseArtifactValidation: true,
		Authorization:       core.TaskDecision{ID: "combined-review-approval:" + hash, Source: decision.Source, Text: "Authorize exactly this bounded review recovery, including derived reconciliation, confirmation and stage-plan hashes: " + string(authority)},
		ContinuationChange:  "Controller creates a fresh review worktree within the approved recovery envelope",
		SavedOutputDecision: "Preserve the exact candidate, old worktrees and passed input-validation receipt",
	}
	state, err = staged.LoadWorkspace()
	if err != nil {
		return fail(err)
	}
	plan, err := PreviewStagePlan(state.Graph, next)
	if err != nil {
		return fail(err)
	}
	next.StagePlanHash = plan.Plan.Hash
	ctx, cancel := context.WithDeadline(context.Background(), request.Deadline)
	defer cancel()
	flow, err := StartReviewFlowContext(ctx, staged, ledger, next)
	if err != nil {
		return fail(err)
	}
	after, err := staged.LoadWorkspace()
	if err != nil {
		return fail(err)
	}
	return ReviewRecoveryPreview{
		Version: 1, Hash: hash, Request: request, Reconciliation: reconciliation,
		Proposal: proposal, Plan: plan, Flow: flow,
	}, after, nil
}
