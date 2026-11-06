package controller

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/gitproof"
	"github.com/hiono/taxion/internal/store"
)

type InputObservationRequest struct {
	TaskID                     string          `json:"taskId"`
	FlowID                     string          `json:"flowId,omitempty"`
	DispatchID                 string          `json:"dispatchId,omitempty"`
	Scope                      []string        `json:"scope"`
	TargetSHA                  string          `json:"targetSha"`
	Configuration              json.RawMessage `json:"configuration,omitempty"`
	Actor                      string          `json:"actor"`
	Source                     string          `json:"source"`
	Reason                     string          `json:"reason"`
	Evidence                   []string        `json:"evidence"`
	NoActiveExecutionConfirmed bool            `json:"noActiveExecutionConfirmed"`
	AcceptDeliveryUncertainty  bool            `json:"acceptDeliveryUncertainty"`
	ExpectedHash               string          `json:"expectedHash,omitempty"`
}

type InputObservationReport struct {
	Version      int                    `json:"version"`
	StateChanged bool                   `json:"stateChanged"`
	Observation  store.InputObservation `json:"observation"`
	Decision     core.InputDecision     `json:"decision"`
}

func inputQuiescence(state store.Workspace, decision *core.InputDecision) {
	block := func(code, field, reason, source string) {
		decision.Block(core.InputCondition{
			Code: code, Field: field, Reason: reason, EvidenceSource: source,
			Resolution: "Inspect saved ownership and use the owning controller's supported stop operation; do not replay execution.",
		})
	}
	if state.Records.Operation != nil && state.Records.Operation.Active() {
		block("operation-active", "operation", "an operation is still active", "saved-operation")
	}
	for _, flow := range state.Graph.ReviewFlows {
		if flow.Status == "RUNNING" || flow.RunnerID != "" {
			block("flow-active", "flowId", "a Flow is running or claimed", "flow:"+flow.ID)
		}
	}
	for _, dispatch := range state.Graph.Dispatches {
		if active(dispatch.Status) {
			block("dispatch-active", "dispatchId", "a Dispatch is still active", "dispatch:"+dispatch.ID)
		}
	}
	for _, dispatch := range state.Ledger.Dispatches {
		if dispatch.Active {
			block("legacy-dispatch-active", "dispatchId", "a legacy Dispatch is still active", "ledger:"+dispatch.ID)
		}
	}
}

func ObserveInput(db *store.Store, request InputObservationRequest, preview bool) (InputObservationReport, error) {
	var report InputObservationReport
	if db == nil {
		return report, fmt.Errorf("taxion: input observation requires an existing workspace")
	}
	check := func(state store.Workspace) error {
		var err error
		report, err = previewInputObservation(state, request)
		if err != nil {
			return err
		}
		return report.Decision.Err()
	}
	if preview {
		state, err := db.LoadWorkspace()
		if err != nil {
			return report, err
		}
		err = check(state)
		return report, err
	}
	err := db.UpdateWorkspace(func(state *store.Workspace) error {
		if err := check(*state); err != nil {
			return err
		}
		o := report.Observation
		if request.ExpectedHash == "" || request.ExpectedHash != o.Hash {
			return fmt.Errorf("taxion: observation changed; approve the exact observe-input --preview hash")
		}
		if existing, found := state.Records.InputObservation(o.Hash); found {
			report.Observation = existing
			return nil
		}
		contract := o.Contract
		located := state.Records.RestoredDispatch(core.GraphDispatch{Instruction: core.DispatchInstruction{Worktree: contract.Worktree}})
		contract.Worktree = located.Instruction.Worktree
		if err := gitproof.RetainInput(contract, o.Target, "observation:"+o.Hash, o.Configuration); err != nil {
			return err
		}
		o.ObservedAt = time.Now().UTC()
		state.Records.InputObservations = append(state.Records.InputObservations, o)
		report.Observation, report.StateChanged = o, true
		return nil
	})
	return report, err
}

func previewInputObservation(state store.Workspace, request InputObservationRequest) (InputObservationReport, error) {
	report := InputObservationReport{Version: 1}
	decision, err := core.NewInputDecision(struct {
		Request InputObservationRequest `json:"request"`
		Graph   core.TaskGraph          `json:"graph"`
	}{request, state.Graph})
	if err != nil {
		return report, err
	}
	report.Decision = decision
	block := func(code, field, reason, resolution string, approval bool) {
		report.Decision.Metadata = "insufficient"
		report.Decision.Block(core.InputCondition{
			Code: code, Field: field, Reason: reason, EvidenceSource: "saved-contract-and-request",
			Resolution: resolution, RequiresApproval: approval,
		})
	}
	for _, text := range append([]string{request.Actor, request.Source, request.Reason}, request.Evidence...) {
		if text == "" || strings.TrimSpace(text) != text || core.Redact(text) != text {
			block("invalid-provenance", "evidence", "sourced non-secret observation evidence is required",
				"Supply a trimmed actor, source, reason and redacted evidence.", false)
			break
		}
	}
	if len(request.Scope) == 0 || request.TargetSHA == "" || len(request.Evidence) == 0 || !request.NoActiveExecutionConfirmed || !request.AcceptDeliveryUncertainty {
		block("observation-authority-missing", "scope", "explicit observation scope, quiescence evidence and uncertainty acceptance are required",
			"Approve the observation scope and confirm execution quiescence; absence of a session is not stop proof.", true)
	}
	task, found := state.Graph.GraphTask(request.TaskID)
	if !found || !state.HasGraph || state.Graph.IsArchived(request.TaskID) {
		block("source-task-unavailable", "taskId", "an existing non-archived source Task is required",
			"Use task graph to locate the source; create an ordinary supplied-input proposal when no metadata exists.", false)
		return report, nil
	}
	inputQuiescence(state, &report.Decision)
	if request.FlowID == "" {
		for _, flow := range state.Graph.ReviewFlows {
			if flow.TaskID == task.ID {
				block("source-metadata-required", "flowId", "the source Task already has saved Flow metadata",
					"Inspect saved Flows and specify the exact source Flow and its latest terminal Dispatch; do not downgrade metadata to absent.", false)
				break
			}
		}
		for _, dispatch := range state.Graph.Dispatches {
			if dispatch.TaskID == task.ID {
				block("source-metadata-required", "dispatchId", "the source Task already has saved Dispatch metadata",
					"Retain the source Flow and Dispatch identities and verify their result receipts.", false)
				break
			}
		}
	}
	o := store.InputObservation{
		Version: 1, SourceTaskID: task.ID, SourceFlowID: request.FlowID, SourceDispatchID: request.DispatchID,
		Actor: request.Actor, Source: request.Source, Reason: request.Reason, Evidence: slices.Clone(request.Evidence),
		NoActiveExecutionConfirmed: request.NoActiveExecutionConfirmed, AcceptDeliveryUncertainty: request.AcceptDeliveryUncertainty,
		Configuration: slices.Clone(request.Configuration),
	}
	prior := core.GraphDispatch{Repository: task.Repo, Recipient: "CODER", Instruction: core.DispatchInstruction{
		Worktree: task.Worktree, BaselineSHA: task.BaselineSHA, TargetSHA: task.TargetSHA,
		AuthorizedActions: task.Scope, ForbiddenActions: task.OutOfScope, FileAccess: task.FileAccess,
	}}
	if request.FlowID != "" {
		flow, ok := state.Graph.ReviewFlow(request.FlowID)
		if !ok || flow.TaskID != task.ID || flow.Status != "STOPPED" || !flow.HandoffHeld() ||
			flow.RunnerID != "" || len(flow.PendingFeedbackIDs()) != 0 || flow.PendingDraftID() != "" ||
			request.DispatchID != flow.LastDispatchID() {
			block("source-flow-unresolved", "flowId", "the source Flow must be stopped, held, unclaimed and have no unresolved feedback",
				"Inspect workflow show and feedback-target; preserve the stop and resolve pending human decisions.", false)
		} else {
			task = flow.Contract
			if flow.Artifact != nil {
				o.SourceArtifactHash, err = core.CanonicalSHA256(flow.Artifact)
				if err != nil {
					return report, err
				}
			}
		}
	}
	if request.DispatchID != "" {
		dispatch, ok := state.Graph.GraphDispatch(request.DispatchID)
		if !ok || request.FlowID == "" || dispatch.Result == nil || active(dispatch.Status) {
			block("source-result-unavailable", "dispatchId", "a terminal result bound to the stopped source Flow is required",
				"Inspect the saved Dispatch result; do not manufacture a partial result or remove its session.", false)
		} else {
			hasReceipt := false
			for _, receipt := range state.Records.Events {
				hasReceipt = hasReceipt || receipt.DispatchID == dispatch.ID && receipt.Type == "verified-result"
			}
			if !hasReceipt {
				block("source-receipt-missing", "dispatchId", "the terminal Dispatch has no controller result receipt",
					"Preserve the result and obtain supported controller reconciliation; quiescence assertions do not replace execution evidence.", false)
			}
			if err := validateResultReceipt(&state, dispatch); err != nil {
				block("source-receipt-unverified", "dispatchId", err.Error(),
					"Use the saved controller receipt; unverified execution cannot be resolved by another model call.", false)
			}
			o.SourceResultHash, err = core.CanonicalSHA256(dispatch.Result)
			if err != nil {
				return report, err
			}
			prior = state.Records.RestoredDispatch(dispatch)
			if len(o.Configuration) == 0 && prior.Configuration != nil {
				o.Configuration = slices.Clone(prior.Configuration.Selection)
			}
			task.Worktree, task.Branch = dispatch.Instruction.Worktree, dispatch.Instruction.Branch
			task.BaselineSHA = prior.Instruction.BaselineSHA
			task.OutOfScope, task.FileAccess = slices.Clone(prior.Instruction.ForbiddenActions), core.CloneFileAccessPolicy(prior.Instruction.FileAccess)
		}
	}
	if report.Decision.Verdict != "accept" {
		report.Decision.NextActions = []string{"inspect-saved-evidence"}
		return report, nil
	}
	var selected config.Config
	if core.DecodeGraphInput(strings.NewReader(string(o.Configuration)), &selected) != nil ||
		selected.Repository.Path != task.Repo || core.Redact(string(o.Configuration)) != string(o.Configuration) {
		block("observation-configuration-invalid", "configuration", "an exact non-secret repository configuration is required",
			"Reuse the saved Dispatch selection, or obtain the explicit repository configuration from config check --sources.", false)
		return report, nil
	}
	task.Scope, task.TargetSHA = slices.Clone(request.Scope), request.TargetSHA
	task.TargetTreeSHA, task.TargetChangedFiles = "", nil
	task.OutputMode = core.OutputModeWorktreeTreeSnapshot
	task.Status, task.AuthorizationID, task.Decisions = core.Draft, "", nil
	capture := task
	located := state.Records.RestoredDispatch(core.GraphDispatch{Instruction: core.DispatchInstruction{Worktree: task.Worktree}})
	capture.Worktree = located.Instruction.Worktree
	target, err := gitproof.ObserveInput(capture, request.Source, o.Configuration)
	if err != nil {
		block("observation-unavailable", "target", err.Error(),
			"Check the exact target and observation scope; forbidden paths and file-access restrictions remain enforced.", true)
		return report, nil
	}
	task.TargetTreeSHA, task.TargetChangedFiles = target.TreeSHA, slices.Clone(target.ChangedFiles)
	o.Contract, o.Target = task, target
	o.PriorPolicy, err = gitproof.AssessChangedPaths(prior, target.ChangedFiles)
	if err != nil {
		return report, err
	}
	o.Hash, err = o.ContentHash()
	if err != nil {
		return report, err
	}
	report.Observation = o
	report.Decision.Metadata = "verified"
	report.Decision.NextActions = []string{"approve-observation-hash", "propose-purpose-bound-successor"}
	return report, nil
}

type inputObservationReference struct {
	Kind          string                    `json:"kind"`
	Hash          string                    `json:"hash"`
	Disposition   string                    `json:"disposition"`
	KnownFailures []string                  `json:"knownFailures"`
	Validations   []core.DispatchValidation `json:"validations"`
	Unknowns      []string                  `json:"unknowns"`
}

func observationDecision(state store.Workspace, task core.GraphTask, hash string) (core.TaskDecision, error) {
	o, ok := state.Records.InputObservation(hash)
	if !ok {
		return core.TaskDecision{}, fmt.Errorf("taxion: the referenced input observation is missing")
	}
	reference := inputObservationReference{
		Kind: "taxion.input-observation.v1", Hash: hash, Disposition: "unverified-current-input",
		KnownFailures: []string{}, Validations: []core.DispatchValidation{},
		Unknowns: []string{"authorship", "stop-time identity", "unrecorded checks and causes", "unrecorded usage; never assume zero"},
	}
	if flow, ok := state.Graph.ReviewFlow(o.SourceFlowID); ok && flow.Artifact != nil {
		if flow.Artifact.Validation.Error != "" {
			reference.KnownFailures = append(reference.KnownFailures, core.Redact(flow.Artifact.Validation.Error))
		}
		for _, check := range flow.Artifact.Validation.Commands {
			check.Command, check.Output = core.Redact(check.Command), core.Redact(check.Output)
			reference.Validations = append(reference.Validations, check)
		}
	}
	if d, ok := state.Graph.GraphDispatch(o.SourceDispatchID); ok && d.Result != nil {
		for _, blocker := range d.Result.Blockers {
			reference.KnownFailures = append(reference.KnownFailures, core.Redact(blocker))
		}
		for _, check := range d.Result.Validations {
			check.Command, check.Output = core.Redact(check.Command), core.Redact(check.Output)
			reference.Validations = append(reference.Validations, check)
		}
	}
	text, err := json.Marshal(reference)
	return core.TaskDecision{ID: "input-observation:" + task.ID, Source: "input-observation", Text: string(text)}, err
}

func taskObservationHash(task core.GraphTask) (string, error) {
	hash := ""
	for _, d := range task.Decisions {
		if d.ID != "input-observation:"+task.ID {
			continue
		}
		var reference inputObservationReference
		if hash != "" || d.Source != "input-observation" ||
			core.DecodeGraphInput(strings.NewReader(d.Text), &reference) != nil ||
			reference.Kind != "taxion.input-observation.v1" || len(reference.Hash) != 64 {
			return "", fmt.Errorf("taxion: input observation reference is invalid or ambiguous")
		}
		hash = reference.Hash
	}
	return hash, nil
}

func AssessInput(db *store.Store, taskID string, selection []byte) (core.InputDecision, error) {
	state, err := db.LoadWorkspace()
	if err != nil {
		return core.InputDecision{}, err
	}
	task, ok := state.Graph.GraphTask(taskID)
	if !ok {
		return core.InputDecision{}, fmt.Errorf("taxion: assess-input requires an existing Task")
	}
	return assessObservedInput(state, task, selection)
}

func assessObservedInput(state store.Workspace, task core.GraphTask, selection []byte) (core.InputDecision, error) {
	hash, metadataErr := taskObservationHash(task)
	decision, err := core.NewInputDecision(struct {
		Task        core.GraphTask `json:"task"`
		Observation string         `json:"observationHash"`
	}{task, hash})
	if err != nil {
		return decision, err
	}
	block := func(code, field, reason, source, resolution string, approval bool) {
		decision.Block(core.InputCondition{Code: code, Field: field, Reason: reason,
			EvidenceSource: source, Resolution: resolution, RequiresApproval: approval})
	}
	if metadataErr != nil {
		decision.Metadata = "invalid"
		block("metadata-invalid", "decisions", metadataErr.Error(), "proposed-contract",
			"Preserve the contradictory proposal and create a correctly bound proposal; do not strip existing metadata.", true)
		return decision, nil
	}
	if hash == "" {
		if err := core.CheckReviewValidationCommands(task.Decisions, task.ValidationCommands); err != nil {
			block("validation-policy-unmet", "validationCommands", err.Error(), "proposed-contract",
				"Supply purpose-appropriate validation commands or an explicitly approved diagnostic input policy.", true)
		}
		decision.NextActions = []string{"confirm-exact-proposal-and-configuration", "plan-coder-and-reviewer"}
		if task.Owner == "USER" {
			decision.NextActions = []string{"confirm-exact-proposal-and-configuration", "plan-supplied-input-validation-and-review"}
		}
		return decision, nil
	}
	decision.Metadata = "verified"
	o, ok := state.Records.InputObservation(hash)
	if !ok {
		decision.Metadata = "invalid"
		block("observation-missing", "observationHash", "the exact saved observation is missing", "records",
			"Use workflow observe-input --file REQUEST --preview; never treat missing metadata as permission.", true)
		return decision, nil
	}
	if err := state.Records.VerifyInputObservations(state.Graph); err != nil {
		return decision, err
	}
	expected, err := observationDecision(state, task, hash)
	if err != nil {
		return decision, err
	}
	if !slices.Contains(task.Decisions, expected) {
		decision.Metadata = "invalid"
		block("metadata-drift", "decisions", "saved failure diagnostics or observation provenance changed", "observation:"+hash,
			"Propose from the saved observation without editing or dropping diagnostic evidence.", true)
	}
	if task.Repo != o.Target.Repository || task.BaselineSHA != o.Target.BaselineSHA ||
		task.TargetSHA != o.Target.CommitSHA || task.TargetTreeSHA != o.Target.TreeSHA ||
		!slices.Equal(task.TargetChangedFiles, o.Target.ChangedFiles) {
		block("target-mismatch", "target", "the successor does not identify the observed input", "observation:"+hash,
			"Preserve both targets; propose the exact observed commit, tree and complete changed paths.", true)
	}
	if o.SourceFlowID != "" {
		flow, found := state.Graph.ReviewFlow(o.SourceFlowID)
		if !found || task.Supersedes != flow.TaskID || task.CreatedFrom != "flow:"+flow.ID ||
			flow.Status != "STOPPED" || !flow.HandoffHeld() || flow.RunnerID != "" ||
			flow.LastDispatchID() != o.SourceDispatchID || len(flow.PendingFeedbackIDs()) != 0 || flow.PendingDraftID() != "" {
			block("predecessor-unresolved", "supersedes", "the successor is not bound to the retained stopped source", "observation:"+hash,
				"Use the saved source Flow and explicit supersedes relation; resolve outstanding feedback.", false)
		}
	}
	if err := core.CheckReviewValidationCommands(task.Decisions, task.ValidationCommands); err != nil {
		block("validation-policy-unmet", "validationCommands", err.Error(), "proposed-contract",
			"Choose explicit quality or diagnostic input policy; do not relabel failed checks as passed.", true)
	}
	located := state.Records.RestoredDispatch(core.GraphDispatch{Instruction: core.DispatchInstruction{Worktree: o.Contract.Worktree}})
	contract := o.Contract
	contract.Worktree = located.Instruction.Worktree
	if len(selection) == 0 {
		selection = o.Configuration
	}
	if err := gitproof.VerifyInput(contract, o.Target, o.Configuration); err != nil {
		decision.Metadata = "stale"
		block("observation-stale", "target", err.Error(), "current-source-worktree",
			"Preserve the old observation; preview and approve a new observation for the changed input.", true)
	}
	if err := gitproof.VerifyInput(task, o.Target, selection); err != nil {
		block("successor-input-unavailable", "scope", err.Error(), "current-successor-worktree",
			"Use an intact copy and explicitly approved scope; the old contract's violation is not waived.", true)
	}
	if err := gitproof.VerifyInputLocation(contract.Worktree, task.Worktree, "observation:"+hash, o.Target.TreeSHA, task.Owner == "CODER"); err != nil {
		block("input-preservation-unverified", "worktree", err.Error(), "retained-observation:"+hash,
			"Keep the recorded source and prepare a distinct implementation worktree in the same repository.", true)
	}
	if decision.Verdict == "accept" {
		decision.NextActions = []string{"confirm-exact-proposal-and-configuration", "plan-within-approved-budget"}
	} else {
		decision.NextActions = []string{"inspect-saved-evidence", "resolve-listed-conditions"}
	}
	return decision, nil
}

func verifyObservedIntake(state store.Workspace, task core.GraphTask, hash string, selection []byte) error {
	bound, err := taskObservationHash(task)
	if err != nil || bound != hash || hash == "" {
		return fmt.Errorf("taxion: confirmation must retain its exact input observation hash")
	}
	decision, err := assessObservedInput(state, task, selection)
	if err != nil {
		return err
	}
	inputQuiescence(state, &decision)
	return decision.Err()
}
