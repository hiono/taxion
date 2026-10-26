package controller

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/gitproof"
	"github.com/hiono/taxion/internal/memory"
	"github.com/hiono/taxion/internal/store"
)

const lostHandoffStopReason = "advance failed: task graph rejected: handoff change requires ID, action, actor, source and reason; timestamp is controller-owned"

type ReviewAcceptanceRequest struct {
	FlowID       string `json:"flowId"`
	Actor        string `json:"actor"`
	Source       string `json:"source"`
	Reason       string `json:"reason"`
	AllowExpired bool   `json:"allowExpired"`
	ExpectedHash string `json:"expectedHash,omitempty"`
}

type ReviewAcceptancePreview struct {
	Version                   int             `json:"version"`
	StateChanged              bool            `json:"stateChanged"`
	FlowID                    string          `json:"flowId"`
	TaskID                    string          `json:"taskId"`
	ReviewerDispatchID        string          `json:"reviewerDispatchId"`
	ReviewerTaskID            string          `json:"reviewerTaskId"`
	ReviewerTaskStatus        core.TaskStatus `json:"reviewerTaskStatus"`
	ReviewerTaskFinalStatus   core.TaskStatus `json:"reviewerTaskFinalStatus"`
	OutputSHA                 string          `json:"outputSha"`
	OutputTreeSHA             string          `json:"outputTreeSha"`
	InstructionHash           string          `json:"instructionHash"`
	ResultHash                string          `json:"resultHash"`
	Deadline                  time.Time       `json:"deadline"`
	DeadlineExpired           bool            `json:"deadlineExpired"`
	OriginalReasonUnavailable bool            `json:"originalReasonUnavailable"`
	Checks                    []string        `json:"checks"`
	Transitions               []string        `json:"transitions"`
	Hash                      string          `json:"hash"`
}

// AcceptSavedReview never starts a runner or executes validation commands.
// Preview and apply share all checks; apply binds the exact workspace and request.
func AcceptSavedReview(ctx context.Context, db *store.Store, ledgerPath string, request ReviewAcceptanceRequest,
	configuration core.ApprovalConfiguration, previewOnly bool) (ReviewAcceptancePreview, error) {
	if db == nil {
		return ReviewAcceptancePreview{}, fmt.Errorf("taxion: saved review acceptance requires a workspace")
	}
	check := func(state store.Workspace) (ReviewAcceptancePreview, error) {
		return previewSavedReview(ctx, state, ledgerPath, request, configuration, time.Now().UTC())
	}
	if previewOnly {
		state, err := db.LoadWorkspace()
		if err != nil {
			return ReviewAcceptancePreview{}, err
		}
		return check(state)
	}
	var preview ReviewAcceptancePreview
	err := db.UpdateWorkspace(func(state *store.Workspace) error {
		var err error
		preview, err = check(*state)
		if err != nil {
			return err
		}
		if request.ExpectedHash == "" || request.ExpectedHash != preview.Hash {
			return fmt.Errorf("taxion: saved review acceptance preview changed; review --preview again and provide its expectedHash")
		}
		flow, _ := state.Graph.ReviewFlow(request.FlowID)
		previous := flow
		last, _ := state.Graph.GraphDispatch(preview.ReviewerDispatchID)
		proof := core.TransitionProof{
			ActionPerformed: true, Evidence: last.Result.Evidence, Acceptance: flow.Contract.Acceptance,
			ReviewedSHA: last.Result.OutputSHA, ReviewedTreeSHA: last.Result.OutputTreeSHA, Reviewer: "REVIEWER",
		}
		for _, status := range []core.TaskStatus{core.Approved, core.Completed} {
			if err := state.Graph.Transition(flow.TaskID, status, request.Actor,
				"saved independent review accepted: "+request.Reason, proof); err != nil {
				return err
			}
		}
		flow.Status, flow.StopCode, flow.StopCause = "COMPLETED", "", ""
		flow.Reason = "saved independent review and acceptance verified for " + preview.OutputTreeSHA
		storeFlow(state, flow)
		state.Records.ReviewAcceptances = append(state.Records.ReviewAcceptances, store.ReviewAcceptance{
			FlowID: flow.ID, DispatchID: last.ID, InstructionHash: last.InstructionHash,
			ResultHash: preview.ResultHash, PreviewHash: preview.Hash,
			Actor: request.Actor, Source: request.Source, Reason: request.Reason,
			AllowExpired: request.AllowExpired, OriginalReasonUnavailable: true,
			PreviousFlow: previous, At: time.Now().UTC(),
		})
		preview.StateChanged = true
		return nil
	})
	return preview, err
}

func previewSavedReview(ctx context.Context, state store.Workspace, ledgerPath string, request ReviewAcceptanceRequest,
	configuration core.ApprovalConfiguration, now time.Time) (ReviewAcceptancePreview, error) {
	reject := func(message string) (ReviewAcceptancePreview, error) {
		return ReviewAcceptancePreview{}, fmt.Errorf("taxion: saved review acceptance: %s", message)
	}
	for _, text := range []string{request.FlowID, request.Actor, request.Source, request.Reason} {
		if strings.TrimSpace(text) == "" || strings.TrimSpace(text) != text {
			return reject("flowId, actor, source and reason must be nonempty and trimmed")
		}
	}
	if err := state.Graph.Validate(); err != nil {
		return ReviewAcceptancePreview{}, err
	}
	flow, ok := state.Graph.ReviewFlow(request.FlowID)
	if !state.HasGraph || !ok || flow.Status != "STOPPED" || flow.StopCode != "SAFETY_STOP" ||
		flow.Reason != lostHandoffStopReason || flow.RunnerID != "" || !flow.HandoffHeld() ||
		flow.SourceDispatchID == "" || len(flow.DispatchIDs) != 1 || flow.Contract.OutputMode != core.OutputModeWorktreeTreeSnapshot {
		return reject("requires an unclaimed review-only snapshot Flow stopped by the lost-handoff-reason defect")
	}
	hold := flow.HandoffChanges[len(flow.HandoffChanges)-1]
	if hold.ID != "flow-stop:"+flow.ID+":"+flow.DispatchIDs[0] ||
		hold.Actor != "controller" || hold.Source != "controller:flow-stop" || hold.Reason != flow.Reason ||
		len(flow.PendingFeedbackIDs()) != 0 || flow.PendingDraftID() != "" {
		return reject("stop hold differs or unresolved feedback/draft requires a separate decision")
	}
	if state.Graph.IsArchived(flow.TaskID) {
		return reject("archived work cannot be accepted")
	}
	if operation := state.Records.Operation; operation != nil && operation.Active() {
		return reject("reconcile the active operation before accepting saved results")
	}
	for _, other := range state.Graph.ReviewFlows {
		if other.RunnerID != "" || other.Status == "RUNNING" {
			return reject("a Flow is still running or claimed")
		}
	}
	for _, dispatch := range state.Graph.Dispatches {
		if active(dispatch.Status) {
			return reject("a Dispatch is still active")
		}
	}
	for _, dispatch := range state.Ledger.Dispatches {
		if dispatch.Active {
			return reject("a legacy Dispatch is still active")
		}
	}
	task, _ := state.Graph.GraphTask(flow.TaskID)
	if task.Status != core.WaitingForReview || !flowContractMatches(state.Graph, flow, task) {
		return reject("current Task must retain its WAITING_FOR_REVIEW contract")
	}
	if err := state.Graph.CheckTaskSpecificationUse(task.ID, "runtime"); err != nil {
		return ReviewAcceptancePreview{}, err
	}
	if err := state.Graph.CheckApprovedDraft(flow); err != nil {
		return ReviewAcceptancePreview{}, err
	}
	last, _ := state.Graph.GraphDispatch(flow.DispatchIDs[0])
	coder, ok := state.Graph.GraphDispatch(flow.SourceDispatchID)
	if !ok || last.Result == nil || last.Result.GitEvidence == nil ||
		last.Result.ReviewVerdict != "APPROVE" || len(last.Result.Findings) != 0 || len(last.Result.Blockers) != 0 {
		return reject("requires a saved independent APPROVE without findings or blockers")
	}
	review, ok := state.Graph.GraphTask(last.TaskID)
	if !ok || review.Status != core.Blocked || review.DeferredReason != "Dispatch ended: SUCCEEDED" ||
		review.HumanDecision != nil || state.Graph.IsArchived(review.ID) || state.Graph.IsArchived(coder.TaskID) {
		return reject("Reviewer Task must retain its recorded successful-Dispatch blocker")
	}
	// Receipts prove that success was persisted before the deadline, not just
	// that a model supplied a timestamp in its result.
	var current config.Config
	if err := core.DecodeGraphInput(strings.NewReader(string(configuration.Selection)), &current); err != nil {
		return reject("effective configuration is invalid")
	}
	for _, dispatch := range []core.GraphDispatch{coder, last} {
		if err := validateResultReceipt(&state, dispatch); err != nil {
			return ReviewAcceptancePreview{}, err
		}
		if err := dispatch.ValidateResult(dispatch.Status, dispatch.Result); err != nil {
			return ReviewAcceptancePreview{}, err
		}
		if dispatch.Configuration == nil {
			return reject("saved Dispatch configuration is missing")
		}
		var selected config.Config
		if core.DecodeGraphInput(strings.NewReader(string(dispatch.Configuration.Selection)), &selected) != nil ||
			selected != current {
			return reject("saved Dispatch configuration differs from the effective configuration")
		}
	}
	var successAt time.Time
	for _, receipt := range state.Records.Events {
		if receipt.DispatchID == last.ID && receipt.Type == "verified-result" {
			successAt = receipt.At
		}
	}
	if successAt.IsZero() || successAt.Before(flow.StartedAt) || !successAt.Before(flow.Deadline) ||
		last.Result.GitEvidence.VerifiedAt.Before(flow.StartedAt) || last.Result.GitEvidence.VerifiedAt.After(successAt) ||
		hold.At.Before(successAt) || now.Before(successAt) {
		return reject("saved success must have controller receipt and Git evidence before the original deadline")
	}
	expired := !now.Before(flow.Deadline)
	if expired && !request.AllowExpired {
		return reject("deadline expired; explicit allowExpired approval for acceptance only is required")
	}
	auth, enabled, err := lifecycleAuthorization(&state, flow)
	if err != nil {
		return ReviewAcceptancePreview{}, err
	}
	bundle, matching := state.Graph.MatchingApproval(task.ID, flow.Contract, configuration)
	if !enabled || !matching || auth.BundleHash != bundle.BundleHash {
		return reject("matching contract bundle and packet authorization are required")
	}
	if _, err := checkTaskConfiguration(state.Graph, task.ID, current); err != nil {
		return ReviewAcceptancePreview{}, err
	}
	approvedReview := review
	approvedReview.Status, approvedReview.DeferredReason, approvedReview.ResumeCondition, approvedReview.NextAction = core.Ready, "", "", ""
	if _, ok := state.Graph.MatchingApproval(review.ID, approvedReview, configuration); !ok {
		return reject("Reviewer approval no longer matches its saved contract")
	}
	if err := verifyAcceptancePackets(&state, flow, auth, coder, last, configuration); err != nil {
		return ReviewAcceptancePreview{}, err
	}
	memoryContext, err := memory.ResolveDispatchContextReadOnly(ledgerPath, last.Instruction.MemoryRefs)
	if err != nil {
		return ReviewAcceptancePreview{}, err
	}
	if !reflect.DeepEqual(memoryContext, last.Instruction.MemoryContext) {
		return reject("required Memory differs from the reviewed snapshot")
	}
	observation, cancel := context.WithTimeout(ctx, DefaultLaneTimeout)
	defer cancel()
	locatedTask := task
	locatedCoder, locatedReview := state.Records.RestoredDispatch(coder), state.Records.RestoredDispatch(last)
	// Restores change locations, never the stored contract or result receipt.
	if state.Records.Restore != nil {
		for _, path := range state.Records.Restore.Paths {
			if path.Original == task.Worktree {
				locatedTask.Worktree = path.Local
			}
		}
	}
	if err := gitproof.VerifySavedSnapshotAcceptance(observation, locatedCoder, locatedReview, locatedTask); err != nil {
		return ReviewAcceptancePreview{}, err
	}
	resultHash, err := core.CanonicalSHA256(last.Result)
	if err != nil {
		return ReviewAcceptancePreview{}, err
	}
	preview := ReviewAcceptancePreview{
		Version: 1, FlowID: flow.ID, TaskID: task.ID, ReviewerDispatchID: last.ID,
		ReviewerTaskID: review.ID, ReviewerTaskStatus: review.Status, ReviewerTaskFinalStatus: review.Status,
		OutputSHA: task.TargetSHA, OutputTreeSHA: task.TargetTreeSHA, InstructionHash: last.InstructionHash,
		ResultHash: resultHash, Deadline: flow.Deadline, DeadlineExpired: expired, OriginalReasonUnavailable: true,
		Checks: []string{"result-receipts", "success-before-deadline", "contract-and-acceptance",
			"configuration-and-approvals", "independent-role-packets", "required-memory",
			"snapshot-ref-and-tree", "retained-coder-snapshot", "current-task-branch-index-worktree", "released-review"},
		Transitions: []string{"Task: WAITING_FOR_REVIEW -> APPROVED -> COMPLETED", "Flow: STOPPED -> COMPLETED",
			"Reviewer Task: BLOCKED (unchanged)", "HOLD: retained (not released)"},
	}
	request.ExpectedHash = ""
	preview.Hash, err = core.CanonicalSHA256(struct {
		State         store.Workspace
		Request       ReviewAcceptanceRequest
		Configuration core.ApprovalConfiguration
		Preview       ReviewAcceptancePreview
	}{state, request, configuration, preview})
	return preview, err
}

func verifyAcceptancePackets(state *store.Workspace, flow core.ReviewFlow, auth store.PacketAuthorization,
	coder, review core.GraphDispatch, configuration core.ApprovalConfiguration) error {
	source, ok := flowForDispatch(state.Graph, coder.ID)
	if !ok || source.SourceDispatchID != "" {
		return fmt.Errorf("taxion: acceptance requires an original Coder Flow")
	}
	sourceAuth, enabled, err := lifecycleAuthorization(state, source)
	if err != nil {
		return err
	}
	sourceBundle, sourceApproved := state.Graph.MatchingApproval(source.TaskID, source.Contract, configuration)
	if !enabled || !sourceApproved || sourceAuth.BundleHash != sourceBundle.BundleHash ||
		auth.ScopeDigest != sourceAuth.ScopeDigest || auth.Acceptance != sourceAuth.Acceptance ||
		flow.Contract.Objective != source.Contract.Objective {
		return fmt.Errorf("taxion: acceptance source contract or approval differs")
	}
	var bundle core.ApprovalBundle
	for _, candidate := range state.Graph.Approvals {
		if candidate.BundleHash == auth.BundleHash {
			bundle = candidate
		}
	}
	if bundle.Dimensions.Security != sourceBundle.Dimensions.Security ||
		bundle.Configuration.Digest != sourceBundle.Configuration.Digest {
		return fmt.Errorf("taxion: acceptance source security or configuration differs")
	}
	if err := checkLifecyclePacket(state, coder.ID); err != nil {
		return err
	}
	evidence, err := validatedPacketEvidence(state, sourceAuth, coder)
	if err != nil {
		return err
	}
	for _, audit := range state.Records.PacketAudits {
		if audit.DispatchID != review.ID || audit.Role != "REVIEWER" {
			continue
		}
		packet, err := state.Records.ReadPacket(audit.PacketHash)
		if err != nil {
			return err
		}
		fields := packet.Readback()
		if audit.AuthorizationHash != auth.Hash || audit.InstructionHash != review.InstructionHash ||
			fields.TaskID != flow.TaskID || fields.TaskHash != auth.TaskHash ||
			fields.BaselineSHA != auth.BaselineSHA || fields.ScopeDigest != auth.ScopeDigest ||
			fields.AcceptanceDigest != auth.Acceptance || fields.Role != "REVIEWER" ||
			fields.PreviousEvidenceHash != evidence || fields.TargetTreeHash != review.Result.OutputTreeSHA {
			return fmt.Errorf("taxion: saved Reviewer packet binding differs")
		}
		return nil
	}
	return fmt.Errorf("taxion: saved Reviewer packet is missing")
}
