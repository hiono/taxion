package controller

import (
	"encoding/json"
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

// PreviewPacketAuthorization does not track a Task or create execution resources.
func PreviewPacketAuthorization(state store.Workspace, taskID, actor, source, reason string,
	unavailable []store.UnavailablePacket, configuration core.ApprovalConfiguration) (store.PacketAuthorization, error) {
	task, ok := state.Graph.GraphTask(taskID)
	if !state.HasGraph || !ok || task.Status != core.Ready || task.Owner != "CODER" || task.Reviewer != "REVIEWER" {
		return store.PacketAuthorization{}, fmt.Errorf("packet lifecycle requires a READY CODER Task with REVIEWER")
	}
	for _, dispatch := range state.Graph.Dispatches {
		if dispatch.TaskID == taskID {
			return store.PacketAuthorization{}, fmt.Errorf("packet lifecycle must be authorized before Dispatch issuance")
		}
	}
	bundle, ok := state.Graph.MatchingApproval(taskID, task, configuration)
	if !ok {
		return store.PacketAuthorization{}, fmt.Errorf("packet lifecycle requires an existing matching ApprovalBundle")
	}
	hash, err := core.CanonicalSHA256(task)
	if err != nil {
		return store.PacketAuthorization{}, err
	}
	tree, err := gitproof.CommitTree(task.Worktree, task.BaselineSHA)
	if err != nil {
		return store.PacketAuthorization{}, err
	}
	missing := slices.Clone(unavailable)
	slices.SortFunc(missing, func(a, b store.UnavailablePacket) int {
		if a.Role < b.Role {
			return -1
		}
		if a.Role > b.Role {
			return 1
		}
		return 0
	})
	authorization := store.PacketAuthorization{
		Version: 1, TaskID: taskID, TaskHash: hash, ProposalHash: bundle.ProposalHash,
		BundleHash: bundle.BundleHash, BaselineSHA: task.BaselineSHA, InitialTree: tree,
		ScopeDigest: bundle.Dimensions.Scope, Acceptance: bundle.Dimensions.Acceptance,
		ReviewerPolicy: "same-contract-reviewer/v1",
		Unavailable:    missing, Actor: actor, Source: source, Reason: reason,
	}
	authorization.Hash, err = core.CanonicalSHA256(authorization)
	if err != nil {
		return store.PacketAuthorization{}, err
	}
	if err := authorization.Verify(); err != nil {
		return store.PacketAuthorization{}, err
	}
	for _, missing := range unavailable {
		for _, artifact := range state.Records.Packets {
			if artifact.Hash == missing.Hash {
				return store.PacketAuthorization{}, fmt.Errorf("available packet cannot be marked unavailable")
			}
		}
	}
	return authorization, nil
}

// lifecycleReviewerConfiguration resolves only the exact same-contract review
// delegation; it does not expand the parent decision or grant repair entry.
func lifecycleReviewerConfiguration(state *store.Workspace, flow core.ReviewFlow, reviewID string,
	candidate config.Config) (core.TaskDecision, bool, error) {
	auth, enabled, err := lifecycleAuthorization(state, flow)
	if err != nil || !enabled {
		return core.TaskDecision{}, enabled, err
	}
	task, ok := state.Graph.GraphTask(flow.TaskID)
	if !ok || !flowContractMatches(state.Graph, flow, task) {
		return core.TaskDecision{}, true, fmt.Errorf("packet lifecycle current contract changed")
	}
	permitted := false
	for index := flow.Repairs; index <= flow.MaxRepairs; index++ {
		permitted = permitted || reviewID == fmt.Sprintf("%s-review-%d", flow.ID, index)
	}
	if !permitted {
		return core.TaskDecision{}, true, fmt.Errorf("Reviewer is outside the bounded lifecycle")
	}
	if _, err := checkTaskConfiguration(state.Graph, flow.TaskID, candidate); err != nil {
		return core.TaskDecision{}, true, err
	}
	matching := false
	for _, bundle := range state.Graph.ApprovalsForTask(flow.TaskID) {
		if bundle.BundleHash != auth.BundleHash {
			continue
		}
		var selected config.Config
		if err := core.DecodeGraphInput(strings.NewReader(string(bundle.Configuration.Selection)), &selected); err != nil {
			return core.TaskDecision{}, true, fmt.Errorf("lifecycle configuration is invalid")
		}
		matching = selected == candidate
	}
	if !matching {
		return core.TaskDecision{}, true, fmt.Errorf("Reviewer configuration differs from lifecycle approval")
	}
	reference := "packet-lifecycle:" + auth.Hash
	id := core.ConfigurationReauthorizationID(candidate.Execution.ApprovalRef, reviewID, reference)
	text, err := json.Marshal(config.Approval{
		Kind: config.ApprovalKind, TaskID: reviewID, Reason: "Same-contract Reviewer delegated by explicit packet lifecycle approval",
		DecisionID: id, AuthorizationRef: reference, ApprovalRef: candidate.Execution.ApprovalRef,
		Allowed: []config.Config{candidate},
	})
	if err != nil {
		return core.TaskDecision{}, true, err
	}
	if err := config.CheckApproval(string(text), reviewID, id, candidate); err != nil {
		return core.TaskDecision{}, true, err
	}
	return core.TaskDecision{ID: id, Source: reference, Text: string(text)}, true, nil
}

// AppendPacketAuthorization must run inside UpdateWorkspace with freshly resolved
// configuration. Recomputing the entire preview detects Task and approval drift.
func AppendPacketAuthorization(state *store.Workspace, approved store.PacketAuthorization,
	configuration core.ApprovalConfiguration) error {
	if err := approved.Verify(); err != nil {
		return err
	}
	current, err := PreviewPacketAuthorization(*state, approved.TaskID, approved.Actor, approved.Source,
		approved.Reason, approved.Unavailable, configuration)
	if err != nil {
		return err
	}
	if current.Hash != approved.Hash {
		return fmt.Errorf("packet lifecycle basis changed after preview")
	}
	for _, previous := range state.Records.PacketAuthorizations {
		if previous.Hash == approved.Hash {
			return nil
		}
	}
	state.Records.PacketAuthorizations = append(state.Records.PacketAuthorizations, current)
	return nil
}

func lifecycleAuthorization(state *store.Workspace, flow core.ReviewFlow) (store.PacketAuthorization, bool, error) {
	auth, enabled := state.Records.PacketAuthorization(flow.TaskID)
	if !enabled {
		return auth, false, nil
	}
	hash, err := core.CanonicalSHA256(flow.Contract)
	if err != nil || hash != auth.TaskHash {
		return auth, true, fmt.Errorf("packet lifecycle Task contract changed")
	}
	found := false
	for _, bundle := range state.Graph.ApprovalsForTask(flow.TaskID) {
		if bundle.BundleHash == auth.BundleHash && bundle.ProposalHash == auth.ProposalHash {
			found = true
			if err := bundle.Verify(flow.Contract, bundle.Configuration); err != nil {
				return auth, true, err
			}
		}
	}
	if !found {
		return auth, true, fmt.Errorf("packet lifecycle approval basis is missing")
	}
	return auth, true, auth.Verify()
}

func saveLifecyclePacket(state *store.Workspace, auth store.PacketAuthorization, dispatch core.GraphDispatch,
	role, previous, tree string) (store.PacketAudit, error) {
	schema := "taxion.dispatch-result/v1"
	if role == "VALIDATOR" {
		schema = "taxion.gate-report/v1"
	}
	packet, err := core.NewRoleHandoffPacket(core.RoleHandoffPacketFields{
		TaskID: auth.TaskID, TaskHash: auth.TaskHash, BaselineSHA: auth.BaselineSHA,
		TargetTreeHash: tree, ScopeDigest: auth.ScopeDigest, AcceptanceDigest: auth.Acceptance,
		ProhibitedActions: []string{"Do not change the approved Task contract or repository configuration.",
			"Do not disclose credentials or private reasoning.", "Do not fabricate predecessor evidence."},
		Role: role, PreviousEvidenceHash: previous, RequiredOutputSchema: schema,
	})
	if err != nil {
		return store.PacketAudit{}, err
	}
	audit := store.PacketAudit{Version: 1, AuthorizationHash: auth.Hash, TaskID: auth.TaskID,
		DispatchID: dispatch.ID, InstructionHash: dispatch.InstructionHash, Role: role,
		PacketHash: packet.Hash(), At: time.Now().UTC()}
	for _, existing := range state.Records.PacketAudits {
		if existing.DispatchID == dispatch.ID && existing.Role == role {
			if existing.PacketHash != audit.PacketHash || existing.AuthorizationHash != auth.Hash ||
				existing.InstructionHash != dispatch.InstructionHash {
				return store.PacketAudit{}, fmt.Errorf("packet lifecycle binding conflict")
			}
			if _, err := state.Records.ReadPacket(existing.PacketHash); err != nil {
				return store.PacketAudit{}, err
			}
			return existing, nil
		}
	}
	if err := state.Records.SavePacket(packet); err != nil {
		return store.PacketAudit{}, err
	}
	state.Records.PacketAudits = append(state.Records.PacketAudits, audit)
	return audit, nil
}

func resultPacketEvidence(state *store.Workspace, dispatch core.GraphDispatch) (string, string, error) {
	if dispatch.Status != core.DispatchSucceeded || dispatch.Result == nil || dispatch.Result.GitEvidence == nil {
		return "", "", fmt.Errorf("packet predecessor requires actual successful Controller evidence")
	}
	if err := validateResultReceipt(state, dispatch); err != nil {
		return "", "", err
	}
	if err := dispatch.ValidateResult(dispatch.Status, dispatch.Result); err != nil {
		return "", "", err
	}
	tree := dispatch.Result.OutputTreeSHA
	if tree == "" {
		var err error
		worktree := dispatch.Instruction.Worktree
		if dispatch.Recipient == "REVIEWER" && dispatch.ReviewWorktreeSource != "" {
			worktree = dispatch.ReviewWorktreeSource
		}
		tree, err = gitproof.CommitTree(worktree, dispatch.Result.OutputSHA)
		if err != nil {
			return "", "", err
		}
	}
	hash, err := core.CanonicalSHA256(dispatch.Result)
	return hash, tree, err
}

// The explicit lifecycle confirmation delegates only the derived independent
// Reviewer approval. It does not replace the original bundle or entry checker.
func prepareLifecycleReviewer(state *store.Workspace, flow core.ReviewFlow, coder core.GraphDispatch,
	review *core.GraphTask, configuration core.ApprovalConfiguration) error {
	auth, enabled, err := lifecycleAuthorization(state, flow)
	if err != nil || !enabled {
		return err
	}
	if _, err := reviewerPacketEvidence(state, flow, auth, coder); err != nil {
		return err
	}
	matching := false
	for _, bundle := range state.Graph.ApprovalsForTask(flow.TaskID) {
		if bundle.BundleHash == auth.BundleHash && reflect.DeepEqual(bundle.Configuration, configuration) {
			matching = true
		}
	}
	expected, err := core.BundleDimensions(flow.Contract, configuration)
	if err != nil {
		return err
	}
	actual, err := core.BundleDimensions(*review, configuration)
	if err != nil {
		return err
	}
	if !matching || review.Owner != "REVIEWER" || review.Type != "REVIEW" ||
		actual.Scope != expected.Scope || actual.Acceptance != expected.Acceptance ||
		actual.Budget != expected.Budget || actual.Security != expected.Security ||
		!reflect.DeepEqual(review.FileAccess, flow.Contract.FileAccess) || review.OutputMode != flow.Contract.OutputMode ||
		review.Objective != flow.Contract.Objective || review.ID != fmt.Sprintf("%s-review-%d", flow.ID, flow.Repairs) ||
		review.Worktree != filepath.Join(flow.ReviewRoot, review.ID) ||
		review.BaselineSHA != coder.Result.OutputSHA || review.TargetSHA != coder.Result.OutputSHA {
		return fmt.Errorf("derived Reviewer exceeds packet lifecycle delegation")
	}
	var selected config.Config
	if err := core.DecodeGraphInput(strings.NewReader(string(configuration.Selection)), &selected); err != nil {
		return fmt.Errorf("derived Reviewer configuration is invalid")
	}
	decision, _, err := lifecycleReviewerConfiguration(state, flow, review.ID, selected)
	if err != nil {
		return err
	}
	review.Decisions = append(review.Decisions, decision)
	review.Decisions = slices.DeleteFunc(review.Decisions, func(decision core.TaskDecision) bool {
		return strings.HasPrefix(decision.ID, "intake-confirmation:")
	})
	if !strings.HasPrefix(auth.ProposalHash, "sha256:") {
		// Manifest-authorized v1 Tasks have no intake confirmation. Retain their
		// exact proposal reference rather than inventing or normalizing a receipt.
		return nil
	}
	reference, err := json.Marshal(struct {
		TaskID       string `json:"taskId"`
		ExpectedHash string `json:"expectedHash"`
	}{review.ID, auth.ProposalHash})
	if err != nil {
		return err
	}
	review.Decisions = append(review.Decisions, core.TaskDecision{
		ID: "intake-confirmation:" + review.ID, Source: "packet-lifecycle:" + auth.Hash, Text: string(reference),
	})
	return nil
}

func approveLifecycleReviewer(state *store.Workspace, flow core.ReviewFlow, reviewID string,
	configuration core.ApprovalConfiguration) error {
	auth, enabled, err := lifecycleAuthorization(state, flow)
	if err != nil || !enabled {
		return err
	}
	review, ok := state.Graph.GraphTask(reviewID)
	if !ok || review.Status != core.Ready {
		return fmt.Errorf("derived Reviewer must be READY before delegated approval")
	}
	bundle, err := core.ComposeApprovalBundleWithProposalHash(
		"packet-reviewer:"+reviewID+":"+auth.Hash, auth.ProposalHash, review, configuration,
		"controller", "packet-lifecycle:"+auth.Hash, "same-contract independent Reviewer authorized by lifecycle confirmation", time.Now().UTC())
	if err != nil {
		return err
	}
	return state.Graph.AppendApproval(bundle)
}

func issueLifecyclePacket(state *store.Workspace, flow core.ReviewFlow, dispatch core.GraphDispatch) error {
	auth, enabled, err := lifecycleAuthorization(state, flow)
	if err != nil || !enabled {
		return err
	}
	tree, previous := auth.InitialTree, ""
	if len(flow.DispatchIDs) == 0 && flow.SourceDispatchID != "" {
		coder, err := budgetContinuationSource(state.Graph, flow.Contract, flow.SourceDispatchID, state.Records)
		if err != nil {
			return err
		}
		previous, err = reviewerPacketEvidence(state, flow, auth, coder)
		if err != nil {
			return err
		}
		_, tree, err = resultPacketEvidence(state, coder)
		if err != nil {
			return err
		}
	}
	if len(flow.DispatchIDs) > 0 {
		last, ok := state.Graph.GraphDispatch(flow.DispatchIDs[len(flow.DispatchIDs)-1])
		if !ok {
			return fmt.Errorf("packet predecessor Dispatch is missing")
		}
		previous, tree, err = resultPacketEvidence(state, last)
		if err != nil {
			return err
		}
		if dispatch.Recipient == "REVIEWER" {
			previous, err = validatedPacketEvidence(state, auth, last)
			if err != nil {
				return err
			}
		}
	}
	if _, err := saveLifecyclePacket(state, auth, dispatch, dispatch.Recipient, previous, tree); err != nil {
		return err
	}
	return nil
}

// The Validator is persisted in its own transaction before the non-model
// verification stage starts. Failed verification cannot produce Reviewer input.
func preparePacketValidator(db *store.Store, id, runnerID string) error {
	snapshot, err := db.LoadWorkspace()
	if err != nil {
		return err
	}
	flow, ok := snapshot.Graph.ReviewFlow(id)
	if !ok {
		return fmt.Errorf("packet lifecycle Flow is missing")
	}
	if _, enabled := snapshot.Records.PacketAuthorization(flow.TaskID); !enabled {
		return nil
	}
	if flow.RunnerID != runnerID {
		return fmt.Errorf("%w: %s", ErrFlowClaimed, flow.RunnerID)
	}
	if flow.HandoffHeld() || !time.Now().Before(flow.Deadline) {
		return nil
	}
	last, ok := snapshot.Graph.GraphDispatch(flow.DispatchIDs[len(flow.DispatchIDs)-1])
	if !ok || flow.Status != "RUNNING" || last.Recipient != "CODER" || last.Status != core.DispatchSucceeded {
		return nil
	}
	return db.UpdateWorkspace(func(state *store.Workspace) error {
		current, ok := state.Graph.ReviewFlow(id)
		if !ok || current.RunnerID != runnerID || current.Status != "RUNNING" ||
			!slices.Equal(current.DispatchIDs, flow.DispatchIDs) ||
			!time.Now().Before(current.Deadline) {
			return fmt.Errorf("packet Validator requires an active authorized handoff")
		}
		if current.HandoffHeld() {
			return fmt.Errorf("%w: %s", core.ErrHandoffHeld, flow.ID)
		}
		auth, _, err := lifecycleAuthorization(state, current)
		if err != nil {
			return err
		}
		coder, _ := state.Graph.GraphDispatch(last.ID)
		if err := checkLifecyclePacket(state, coder.ID); err != nil {
			return err
		}
		hash, tree, err := resultPacketEvidence(state, coder)
		if err != nil {
			return err
		}
		_, err = saveLifecyclePacket(state, auth, coder, "VALIDATOR", hash, tree)
		return err
	})
}

// This receipt records verification of saved real validation results, not a
// model verdict and not invented successful command output.
func packetValidationReport(auth store.PacketAuthorization, evidence string, at time.Time) GateReport {
	report := newGateReport(GateExecution, auth.TaskID)
	report.ObservedAt = at
	report.Hashes = GateHashes{Task: auth.TaskHash, Record: auth.TaskHash,
		Proposal: auth.ProposalHash, Bundle: auth.BundleHash, Evidence: evidence}
	report.NextAuthorizedTransition = "issue Reviewer Dispatch with verified packet"
	return report
}

func recordPacketValidation(state *store.Workspace, flow core.ReviewFlow, coder core.GraphDispatch) error {
	auth, enabled, err := lifecycleAuthorization(state, flow)
	if err != nil || !enabled || coder.Recipient != "CODER" {
		return err
	}
	hash, _, err := resultPacketEvidence(state, coder)
	if err != nil {
		return err
	}
	for _, audit := range state.Records.PacketAudits {
		if audit.DispatchID != coder.ID || audit.Role != "VALIDATOR" || audit.Validation != "" {
			continue
		}
		packet, err := state.Records.ReadPacket(audit.PacketHash)
		if err != nil {
			return err
		}
		if audit.AuthorizationHash != auth.Hash || packet.Readback().PreviousEvidenceHash != hash {
			return fmt.Errorf("Validator packet predecessor mismatch")
		}
		at := time.Now().UTC()
		data, err := core.CanonicalJSON(packetValidationReport(auth, hash, at))
		if err != nil {
			return err
		}
		for _, previous := range state.Records.PacketAudits {
			if previous.DispatchID == coder.ID && previous.Role == "VALIDATOR" && previous.Validation != "" {
				if _, err := validatedPacketEvidence(state, auth, coder); err != nil {
					return fmt.Errorf("validation evidence changed")
				}
				return nil
			}
		}
		audit.Validation, audit.EvidenceHash, audit.At = string(data), core.SHA256Hex(data), at
		state.Records.PacketAudits = append(state.Records.PacketAudits, audit)
		return nil
	}
	return fmt.Errorf("Validator packet must be persisted before verification")
}

func validatedPacketEvidence(state *store.Workspace, auth store.PacketAuthorization, coder core.GraphDispatch) (string, error) {
	resultHash, _, err := resultPacketEvidence(state, coder)
	if err != nil {
		return "", err
	}
	for _, audit := range state.Records.PacketAudits {
		if audit.DispatchID == coder.ID && audit.Role == "VALIDATOR" && audit.Validation != "" {
			packet, err := state.Records.ReadPacket(audit.PacketHash)
			if err != nil {
				return "", err
			}
			expected, err := core.CanonicalJSON(packetValidationReport(auth, resultHash, audit.At))
			if err != nil || audit.Validation != string(expected) ||
				audit.InstructionHash != coder.InstructionHash ||
				audit.AuthorizationHash != auth.Hash || core.SHA256Hex(expected) != audit.EvidenceHash ||
				packet.Readback().PreviousEvidenceHash != resultHash {
				return "", fmt.Errorf("Controller validation evidence mismatch")
			}
			return audit.EvidenceHash, nil
		}
	}
	return "", fmt.Errorf("Reviewer requires actual Controller validation evidence")
}

func reviewerPacketEvidence(state *store.Workspace, flow core.ReviewFlow, auth store.PacketAuthorization, coder core.GraphDispatch) (string, error) {
	if flow.SourceDispatchID != coder.ID {
		return validatedPacketEvidence(state, auth, coder)
	}
	if _, err := budgetContinuationSource(state.Graph, flow.Contract, coder.ID, state.Records); err != nil {
		return "", err
	}
	source, ok := flowForDispatch(state.Graph, coder.ID)
	if !ok || flow.Contract.CreatedFrom != "flow:"+source.ID {
		return "", fmt.Errorf("packet continuation requires its exact source Flow")
	}
	sourceAuth, enabled, err := lifecycleAuthorization(state, source)
	if err != nil {
		return "", err
	}
	if !enabled {
		return "", fmt.Errorf("packet continuation requires source packet authorization")
	}
	if auth.ScopeDigest != sourceAuth.ScopeDigest || auth.Acceptance != sourceAuth.Acceptance ||
		flow.Contract.Objective != source.Contract.Objective || flow.Contract.OutputMode != source.Contract.OutputMode {
		return "", fmt.Errorf("packet continuation changed the verified scope or acceptance")
	}
	var currentBundle, sourceBundle core.ApprovalBundle
	for _, bundle := range state.Graph.Approvals {
		if bundle.BundleHash == auth.BundleHash {
			currentBundle = bundle
		}
		if bundle.BundleHash == sourceAuth.BundleHash {
			sourceBundle = bundle
		}
	}
	if currentBundle.Dimensions.Security != sourceBundle.Dimensions.Security ||
		currentBundle.Configuration.Digest != sourceBundle.Configuration.Digest {
		return "", fmt.Errorf("packet continuation changed security or execution configuration")
	}
	if err := checkLifecyclePacket(state, coder.ID); err != nil {
		return "", err
	}
	// The retained report belongs to the original approval; the new Reviewer
	// packet binds its evidence hash to the separately approved successor.
	return validatedPacketEvidence(state, sourceAuth, coder)
}

func checkLifecyclePacket(state *store.Workspace, dispatchID string) error {
	dispatch, ok := state.Graph.GraphDispatch(dispatchID)
	if !ok {
		return fmt.Errorf("packet Dispatch is missing")
	}
	flow, inFlow := flowForDispatch(state.Graph, dispatchID)
	taskID := dispatch.TaskID
	if inFlow {
		taskID = flow.TaskID
	}
	if _, enabled := state.Records.PacketAuthorization(taskID); !enabled {
		return nil
	}
	if !inFlow {
		return fmt.Errorf("authorized packet lifecycle requires its bounded review Flow")
	}
	auth, _, err := lifecycleAuthorization(state, flow)
	if err != nil {
		return err
	}
	previous, tree := "", auth.InitialTree
	position := slices.Index(flow.DispatchIDs, dispatchID)
	if position < 0 {
		return fmt.Errorf("packet Dispatch is outside its Flow")
	}
	if position > 0 {
		prior, _ := state.Graph.GraphDispatch(flow.DispatchIDs[position-1])
		previous, tree, err = resultPacketEvidence(state, prior)
		if err != nil {
			return err
		}
		if dispatch.Recipient == "REVIEWER" {
			previous, err = validatedPacketEvidence(state, auth, prior)
			if err != nil {
				return err
			}
		}
	} else if flow.SourceDispatchID != "" {
		coder, ok := state.Graph.GraphDispatch(flow.SourceDispatchID)
		if !ok || dispatch.Recipient != "REVIEWER" || dispatch.RelatedID != coder.ID {
			return fmt.Errorf("packet continuation requires its independent Reviewer")
		}
		previous, err = reviewerPacketEvidence(state, flow, auth, coder)
		if err != nil {
			return err
		}
		_, tree, err = resultPacketEvidence(state, coder)
		if err != nil {
			return err
		}
	}
	for _, audit := range state.Records.PacketAudits {
		if audit.DispatchID == dispatchID && audit.Role == dispatch.Recipient {
			packet, err := state.Records.ReadPacket(audit.PacketHash)
			if err != nil {
				return err
			}
			fields := packet.Readback()
			if audit.AuthorizationHash != auth.Hash || audit.InstructionHash != dispatch.InstructionHash ||
				fields.PreviousEvidenceHash != previous || fields.TargetTreeHash != tree {
				return fmt.Errorf("packet does not match Dispatch or predecessor")
			}
			return nil
		}
	}
	return fmt.Errorf("verified role packet is missing")
}

func CheckRolePacket(db *store.Store, dispatchID string) error {
	if db == nil {
		return fmt.Errorf("packet verification requires a workspace")
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		return err
	}
	return checkLifecyclePacket(&state, dispatchID)
}
