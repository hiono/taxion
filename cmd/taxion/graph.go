package main

import (
	"crypto/subtle"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/config"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/gitproof"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/store"
)

type graphRequest struct {
	Task    core.GraphTask `json:"task"`
	Actor   string         `json:"actor"`
	Reason  string         `json:"reason"`
	Concern *core.Concern  `json:"concern,omitempty"`
}

func loadGraphRequest(file string) (graphRequest, error) {
	var request graphRequest
	if file == "" {
		return request, fmt.Errorf("--manifest is required")
	}
	input, err := os.Open(file)
	if err != nil {
		return request, err
	}
	defer input.Close()
	if err := core.DecodeGraphInput(input, &request); err != nil {
		return request, err
	}
	return request, nil
}

func taskValidateCmd(file string) error {
	request, err := loadGraphRequest(file)
	if err != nil {
		return validateError(err)
	}
	graph, ledger, initialized, err := loadGraphReadOnly()
	if err != nil {
		return validateError(err)
	}
	if _, exists := graph.GraphTask(request.Task.ID); !exists && request.Task.BudgetGuidance == nil {
		request.Task.BudgetGuidance, err = newTaskBudgetGuidance()
		if err != nil {
			return err
		}
	}
	report := graph.ValidateTracking(request.Task, request.Actor, request.Reason, request.Concern, ledger, initialized)
	if err := printJSON(report); err != nil {
		return err
	}
	if !report.Valid {
		return validateError(fmt.Errorf("%d validator error(s); no Task was stored", len(report.ValidatorErrors)))
	}
	return nil
}

type approvalStageReport struct {
	Stage              core.ApprovalStage `json:"stage"`
	Approval           string             `json:"approval"`
	ReapprovalRequired bool               `json:"reapprovalRequired"`
}

type approvalGateReport struct {
	Reused                  bool                  `json:"reused,omitempty"`
	PriorBundleID           string                `json:"priorBundleId,omitempty"`
	PriorBundleHash         string                `json:"priorBundleHash,omitempty"`
	TaskID                  string                `json:"taskId"`
	ApprovalID              string                `json:"approvalId"`
	BundleVersion           int                   `json:"bundleVersion"`
	CanonicalizationVersion int                   `json:"canonicalizationVersion"`
	ProposalHash            string                `json:"proposalHash"`
	RecordHash              string                `json:"recordHash"`
	ConfigurationDigest     string                `json:"configurationDigest"`
	BundleHash              string                `json:"bundleHash"`
	Stages                  []approvalStageReport `json:"stages"`
	ReapprovalRequired      bool                  `json:"reapprovalRequired"`
	Drifts                  []core.ApprovalDrift  `json:"drifts"`
}

func taskAuthorizeCmd(file, confirm string) error {
	request, err := loadGraphRequest(file)
	if err != nil {
		return authorizeError(err)
	}
	graph, ledger, initialized, err := loadGraphReadOnly()
	if err != nil {
		return authorizeError(err)
	}
	configuration, err := approvalConfiguration()
	if err != nil {
		return authorizeError(err)
	}
	proposal := request.Task
	if _, exists := graph.GraphTask(proposal.ID); !exists && proposal.BudgetGuidance == nil {
		proposal.BudgetGuidance, err = newTaskBudgetGuidance()
		if err != nil {
			return authorizeError(err)
		}
	}
	validation := graph.ValidateTracking(proposal, request.Actor, request.Reason, request.Concern, ledger, initialized)
	if !validation.Valid {
		if err := printJSON(validation); err != nil {
			return err
		}
		return authorizeError(fmt.Errorf("%d validator error(s); no Task was stored", len(validation.ValidatorErrors)))
	}
	if validation.DigestWithheld {
		return authorizeError(fmt.Errorf("contract digest is withheld; remove the credential-like value and authorize again"))
	}
	dimensions, err := core.BundleDimensions(proposal, configuration)
	if err != nil {
		return authorizeError(err)
	}
	content := struct {
		Status              string                  `json:"status"`
		TaskID              string                  `json:"taskId"`
		Title               string                  `json:"title"`
		Objective           string                  `json:"objective"`
		ProposalHash        string                  `json:"proposalHash"`
		ConfigurationDigest string                  `json:"configurationDigest"`
		Configuration       []string                `json:"configurationFields"`
		Dimensions          core.ApprovalDimensions `json:"dimensions"`
		ConfirmationToken   string                  `json:"confirmationToken"`
		NextAction          string                  `json:"nextAction,omitempty"`
	}{
		Status: "awaiting-approval",
		TaskID: request.Task.ID, Title: request.Task.Title, Objective: request.Task.Objective,
		ProposalHash: validation.CanonicalSHA256, ConfigurationDigest: configuration.Digest,
		Configuration: approvalFieldPaths(configuration.Selection), Dimensions: dimensions,
	}
	token, err := core.ConfirmationTokenFor(validation.CanonicalSHA256, dimensions)
	if err != nil {
		return authorizeError(err)
	}
	content.ConfirmationToken = token
	if confirm == "" {
		content.NextAction = "rerun with --confirm <confirmationToken> to approve"
		return printJSON(content)
	}
	if subtle.ConstantTimeCompare([]byte(confirm), []byte(token)) != 1 {
		return authorizeError(fmt.Errorf("--confirm does not match the approval basis; rerun without --confirm to review it"))
	}
	content.Status = "approved"
	if stored, exists := graph.GraphTask(proposal.ID); exists {
		if recordHash, err := core.CanonicalSHA256(stored); err == nil && recordHash == validation.CanonicalSHA256 {
			if bundle, ok := graph.MatchingApproval(proposal.ID, stored, configuration); ok {
				return printJSON(approvalReuseReport(bundle))
			}
		}
	}
	if err := updateWorkspace(func(current *store.Workspace) error {
		if !current.HasGraph {
			if err := core.GuardActiveDispatch(current.Ledger.Dispatches); err != nil {
				return err
			}
			current.Graph = core.TaskGraph{Version: 1}
			current.HasGraph = true
		}
		if err := current.Graph.Track(proposal, request.Actor, request.Reason); err != nil {
			return err
		}
		if request.Concern != nil {
			if err := current.Graph.AddConcern(*request.Concern); err != nil {
				return err
			}
		}
		return current.Graph.CheckLedger(current.Ledger)
	}); err != nil {
		return authorizeError(err)
	}
	readbackGraph, _, _, err := loadGraphReadOnly()
	if err != nil {
		return authorizeError(err)
	}
	stored, exists := readbackGraph.GraphTask(proposal.ID)
	if !exists {
		return authorizeError(fmt.Errorf("post-save readback lost Task %s; approval aborted", proposal.ID))
	}
	recordHash, err := core.CanonicalSHA256(stored)
	if err != nil {
		return authorizeError(err)
	}
	if recordHash != validation.CanonicalSHA256 {
		return authorizeError(fmt.Errorf("post-save readback does not match the validated contract; approval aborted"))
	}
	at := time.Now()
	bundle, err := core.ComposeApprovalBundle(approvalID(proposal.ID, recordHash, configuration.Digest, at), request.Task, stored, configuration, request.Actor, "human:"+request.Actor, request.Reason, at)
	if err != nil {
		return authorizeError(err)
	}
	if err := bundle.Verify(stored, configuration); err != nil {
		return authorizeError(err)
	}
	if err := updateWorkspace(func(current *store.Workspace) error {
		return current.Graph.AppendApproval(bundle)
	}); err != nil {
		return authorizeError(err)
	}
	_, priorMatches := readbackGraph.MatchingApproval(stored.ID, stored, configuration)
	newest, hasNewest := readbackGraph.NewestApproval(stored.ID)
	drifts := []core.ApprovalDrift{}
	if hasNewest {
		computed, err := newest.Drifts(stored, configuration)
		if err != nil {
			return authorizeError(err)
		}
		drifts = computed
	}
	report := approvalGateReport{
		TaskID: bundle.TaskID, ApprovalID: bundle.ID, BundleVersion: bundle.BundleVersion,
		CanonicalizationVersion: bundle.CanonicalizationVersion, ProposalHash: bundle.ProposalHash,
		RecordHash: bundle.RecordHash, ConfigurationDigest: bundle.Configuration.Digest,
		BundleHash: bundle.BundleHash, Drifts: drifts,
	}
	if hasNewest {
		report.PriorBundleID = newest.ID
		report.PriorBundleHash = newest.BundleHash
	}
	for _, stage := range core.ApprovalStages() {
		stageReport := approvalStageReport{Stage: stage, Approval: "approved"}
		if priorMatches {
			stageReport.Approval = "reused"
		}
		if err := bundle.CheckApprovalFor(stored, configuration, stage); err != nil {
			stageReport.Approval = "reapproval-required"
			report.ReapprovalRequired = true
		}
		report.Stages = append(report.Stages, stageReport)
	}
	return printJSON(report)
}

type taskReauthorizationPreview struct {
	Status                  string                  `json:"status"`
	TaskID                  string                  `json:"taskId"`
	State                   core.TaskStatus         `json:"state"`
	ProposalHash            string                  `json:"proposalHash"`
	RecordHash              string                  `json:"recordHash"`
	AuthorizedRecordHash    string                  `json:"authorizedRecordHash,omitempty"`
	BundleVersion           int                     `json:"bundleVersion"`
	CanonicalizationVersion int                     `json:"canonicalizationVersion"`
	Dimensions              core.ApprovalDimensions `json:"dimensions"`
	RedactedDriftPaths      []string                `json:"redactedDriftPaths"`
	ConfirmationToken       string                  `json:"confirmationToken"`

	configuration     core.ApprovalConfiguration `json:"-"`
	decision          core.TaskDecision          `json:"-"`
	approvalID        string                     `json:"-"`
	approvalCount     int                        `json:"-"`
	priorApprovalID   string                     `json:"-"`
	priorApprovalHash string                     `json:"-"`
	priorDecisions    []core.TaskDecision        `json:"-"`
	priorTask         core.GraphTask             `json:"-"`
}

func taskReauthorizeCmd(args []string) error {
	fs := flag.NewFlagSet("task reauthorize", flag.ContinueOnError)
	taskID := fs.String("task", "", "existing READY Task ID")
	confirm := fs.String("confirm", "", "full-basis confirmation token from the preview")
	if err := fs.Parse(args); err != nil {
		return authorizeError(err)
	}
	if fs.NArg() != 0 || *taskID == "" {
		return authorizeError(fmt.Errorf("usage: taxion task reauthorize --task TASK_ID [--confirm TOKEN]"))
	}
	graph, _, initialized, err := loadGraphReadOnly()
	if err != nil {
		return authorizeError(err)
	}
	if !initialized {
		return authorizeError(fmt.Errorf("Task Graph is not initialized"))
	}
	if err := graph.Validate(); err != nil {
		return authorizeError(err)
	}
	task, exists := graph.GraphTask(*taskID)
	if !exists {
		return authorizeError(fmt.Errorf("Task %s was not found", *taskID))
	}
	if task.Status != core.Ready {
		return authorizeError(fmt.Errorf("requires READY Task; found %s", task.Status))
	}
	if err := checkReauthorizationProposal(graph, task); err != nil {
		return authorizeError(err)
	}
	configuration, err := approvalConfiguration()
	if err != nil {
		return authorizeError(err)
	}
	if bundle, ok := graph.MatchingApproval(task.ID, task, configuration); ok && taskHasConfigurationApproval(task, configuration) {
		return printJSON(taskReauthorizationReuseReport(task, bundle))
	}
	preview, err := buildTaskReauthorizationPreview(graph, task, configuration)
	if err != nil {
		return authorizeError(err)
	}
	if *confirm == "" {
		return printJSON(preview)
	}
	if subtle.ConstantTimeCompare([]byte(*confirm), []byte(preview.ConfirmationToken)) != 1 {
		return authorizeError(fmt.Errorf("--confirm does not match the approval basis; rerun without --confirm to review it"))
	}

	if err := updateWorkspace(func(current *store.Workspace) error {
		if !current.HasGraph || current.Graph.ApprovalSchemaVersion < 0 ||
			current.Graph.ApprovalSchemaVersion > core.CurrentApprovalSchemaVersion {
			return fmt.Errorf("Task Graph approval schema is unknown")
		}
		currentTask, ok := current.Graph.GraphTask(preview.TaskID)
		if !ok || currentTask.Status != core.Ready {
			return fmt.Errorf("Task state changed after reauthorization preview")
		}
		currentHash, err := core.CanonicalSHA256(currentTask)
		if err != nil {
			return err
		}
		if currentHash != preview.RecordHash {
			return fmt.Errorf("Task changed after reauthorization preview")
		}
		if err := checkReauthorizationProposal(current.Graph, currentTask); err != nil {
			return err
		}
		if len(current.Graph.Approvals) != preview.approvalCount {
			return fmt.Errorf("approval history changed after reauthorization preview")
		}
		if preview.approvalCount > 0 {
			last := current.Graph.Approvals[preview.approvalCount-1]
			if last.ID != preview.priorApprovalID || last.BundleHash != preview.priorApprovalHash {
				return fmt.Errorf("approval history changed after reauthorization preview")
			}
		}
		resolved, err := resolveConfiguration()
		if err != nil {
			return err
		}
		freshConfiguration, err := approvalConfigurationFor(resolved.Config, resolved.Sources)
		if err != nil {
			return err
		}
		if freshConfiguration.Digest != preview.configuration.Digest ||
			!reflect.DeepEqual(freshConfiguration.Sources, preview.configuration.Sources) {
			return fmt.Errorf("effective configuration changed after reauthorization preview")
		}
		if err := config.CheckApproval(preview.decision.Text, preview.TaskID, preview.decision.ID, resolved.Config); err != nil {
			return fmt.Errorf("reauthorization decision does not match the effective configuration: %w", err)
		}
		beforeFlows := current.Graph.ReviewFlows
		beforeDispatches := current.Graph.Dispatches
		beforeDecisions := slices.Clone(currentTask.Decisions)
		if err := current.Graph.AppendReauthorizationDecision(preview.TaskID, preview.RecordHash, preview.decision); err != nil {
			return err
		}
		updatedTask, ok := current.Graph.GraphTask(preview.TaskID)
		if !ok || updatedTask.Status != core.Ready || len(updatedTask.Decisions) != len(beforeDecisions)+1 ||
			!slices.Equal(updatedTask.Decisions[:len(beforeDecisions)], beforeDecisions) {
			return fmt.Errorf("Task changed while appending configuration authorization")
		}
		contractOnly := updatedTask
		contractOnly.Decisions = currentTask.Decisions
		if !reflect.DeepEqual(contractOnly, currentTask) ||
			!reflect.DeepEqual(beforeFlows, current.Graph.ReviewFlows) ||
			!reflect.DeepEqual(beforeDispatches, current.Graph.Dispatches) {
			return fmt.Errorf("reauthorization changed Task contract, Flow or Dispatch state")
		}
		bundle, err := core.ComposeReauthorizationApprovalBundleWithProposalHash(
			preview.approvalID, preview.ProposalHash, updatedTask, freshConfiguration,
			"human", "human:task-reauthorization", "approve exact effective configuration for READY Task", time.Now().UTC(),
		)
		if err != nil {
			return err
		}
		if err := bundle.Verify(updatedTask, freshConfiguration); err != nil {
			return err
		}
		if err := current.Graph.AppendApproval(bundle); err != nil {
			return err
		}
		return current.Graph.CheckLedger(current.Ledger)
	}); err != nil {
		return authorizeError(err)
	}

	readbackGraph, _, _, err := loadGraphReadOnly()
	if err != nil {
		return authorizeError(err)
	}
	readbackTask, ok := readbackGraph.GraphTask(preview.TaskID)
	if !ok || readbackTask.Status != core.Ready ||
		len(readbackTask.Decisions) != len(preview.priorDecisions)+1 ||
		!slices.Equal(readbackTask.Decisions[:len(preview.priorDecisions)], preview.priorDecisions) {
		return authorizeError(fmt.Errorf("post-save readback did not preserve READY Task decisions"))
	}
	contractOnly := readbackTask
	contractOnly.Decisions = preview.priorTask.Decisions
	if !reflect.DeepEqual(contractOnly, preview.priorTask) {
		return authorizeError(fmt.Errorf("post-save readback changed the Task contract"))
	}
	bundle, ok := readbackGraph.Approval(preview.approvalID)
	if !ok {
		return authorizeError(fmt.Errorf("post-save readback lost the appended approval"))
	}
	if err := bundle.Verify(readbackTask, preview.configuration); err != nil {
		return authorizeError(fmt.Errorf("post-save approval verification failed: %w", err))
	}
	db, err := openWorkspace()
	if err != nil {
		return authorizeError(err)
	}
	defer db.Close()
	resolved, err := resolveConfiguration()
	if err != nil {
		return authorizeError(err)
	}
	if _, err := controller.CheckConfiguration(db, preview.TaskID, resolved.Config); err != nil {
		return authorizeError(fmt.Errorf("task-bound configuration check failed after reauthorization: %w", err))
	}
	preview.Status = "approved"
	preview.AuthorizedRecordHash = bundle.RecordHash
	return printJSON(preview)
}

func buildTaskReauthorizationPreview(graph core.TaskGraph, task core.GraphTask, configuration core.ApprovalConfiguration) (taskReauthorizationPreview, error) {
	if err := checkReauthorizationProposal(graph, task); err != nil {
		return taskReauthorizationPreview{}, err
	}
	prior, found, err := priorTaskConfigurationApproval(task, configuration)
	if err != nil {
		return taskReauthorizationPreview{}, err
	}
	if !found {
		return taskReauthorizationPreview{}, fmt.Errorf("no prior configuration approval exists for this Task and approval reference")
	}
	driftPaths, err := core.ConfigurationDriftFields(prior, configuration.Selection)
	if err != nil {
		return taskReauthorizationPreview{}, err
	}
	if len(driftPaths) == 0 {
		return taskReauthorizationPreview{}, fmt.Errorf("the existing configuration approval already matches the effective configuration")
	}
	for _, existing := range graph.ApprovalsForTask(task.ID) {
		if !core.SupportsApprovalBundleVersion(existing.BundleVersion) ||
			!core.SupportsApprovalCanonicalizationVersion(existing.CanonicalizationVersion) {
			return taskReauthorizationPreview{}, fmt.Errorf("existing approval uses an unknown bundle or canonicalization version")
		}
	}
	proposalHash, err := core.ImmutableProposalHash(task)
	if err != nil {
		return taskReauthorizationPreview{}, err
	}
	recordHash, err := core.CanonicalSHA256(task)
	if err != nil {
		return taskReauthorizationPreview{}, err
	}
	reference, err := configurationApprovalRef(configuration.Selection)
	if err != nil {
		return taskReauthorizationPreview{}, err
	}
	authorizationRef, err := core.CanonicalSHA256(struct {
		TaskID        string
		ProposalHash  string
		RecordHash    string
		Configuration string
	}{task.ID, proposalHash, recordHash, configuration.Digest})
	if err != nil {
		return taskReauthorizationPreview{}, err
	}
	authorizationRef = strings.TrimPrefix(authorizationRef, "sha256:")
	decisionID := core.ConfigurationReauthorizationID(reference, task.ID, authorizationRef)
	selected, err := configurationValue(configuration.Selection)
	if err != nil {
		return taskReauthorizationPreview{}, err
	}
	approvalText, err := json.Marshal(config.Approval{
		Kind: config.ApprovalKind, TaskID: task.ID,
		Reason:     "append-only approval of the exact effective configuration for this READY Task",
		DecisionID: decisionID, AuthorizationRef: authorizationRef, ApprovalRef: reference,
		Allowed: []config.Config{selected},
	})
	if err != nil {
		return taskReauthorizationPreview{}, err
	}
	decision := core.TaskDecision{ID: decisionID, Source: "human:task-reauthorization", Text: string(approvalText)}
	candidate := task
	candidate.Decisions = append(slices.Clone(task.Decisions), decision)
	dimensions, err := core.BundleDimensionsForVersion(candidate, configuration, core.ReauthorizationCanonicalizationVersion)
	if err != nil {
		return taskReauthorizationPreview{}, err
	}
	token, err := core.ConfirmationTokenForVersion(core.ReauthorizationCanonicalizationVersion, proposalHash, dimensions)
	if err != nil {
		return taskReauthorizationPreview{}, err
	}
	bundleID := "reauthorization-" + authorizationRef[:16]
	preview := taskReauthorizationPreview{
		Status: "awaiting-confirmation", TaskID: task.ID, State: task.Status,
		ProposalHash: proposalHash, RecordHash: recordHash,
		BundleVersion: core.ApprovalBundleVersion, CanonicalizationVersion: core.ReauthorizationCanonicalizationVersion,
		Dimensions: dimensions, RedactedDriftPaths: driftPaths, ConfirmationToken: token,
		configuration: configuration, decision: decision, approvalID: bundleID,
		approvalCount: len(graph.Approvals), priorDecisions: slices.Clone(task.Decisions), priorTask: task,
	}
	if len(graph.Approvals) > 0 {
		last := graph.Approvals[len(graph.Approvals)-1]
		preview.priorApprovalID, preview.priorApprovalHash = last.ID, last.BundleHash
	}
	return preview, nil
}

func checkReauthorizationProposal(graph core.TaskGraph, task core.GraphTask) error {
	proposalHash, err := core.ImmutableProposalHash(task)
	if err != nil {
		return err
	}
	for _, bundle := range graph.ApprovalsForTask(task.ID) {
		if bundle.ProposalHash != proposalHash {
			return fmt.Errorf("existing approval conflicts with the immutable proposal reference; no implicit migration is permitted")
		}
	}
	return nil
}

func priorTaskConfigurationApproval(task core.GraphTask, current core.ApprovalConfiguration) (json.RawMessage, bool, error) {
	ref, err := configurationApprovalRef(current.Selection)
	if err != nil {
		return nil, false, err
	}
	prefix := core.ConfigurationApprovalID(ref, task.ID)
	for index := len(task.Decisions) - 1; index >= 0; index-- {
		decision := task.Decisions[index]
		if decision.ID != ref && !strings.HasPrefix(decision.ID, prefix) {
			continue
		}
		var approval config.Approval
		if err := json.Unmarshal([]byte(decision.Text), &approval); err != nil {
			return nil, false, fmt.Errorf("existing configuration approval is ambiguous")
		}
		if approval.Kind != config.ApprovalKind || approval.Reason == "" {
			return nil, false, fmt.Errorf("existing configuration approval is ambiguous")
		}
		taskAllowed := approval.TaskID == task.ID || slices.Contains(approval.AdditionalTaskIDs, task.ID)
		if !taskAllowed {
			continue
		}
		for _, allowed := range approval.Allowed {
			if allowed.Execution.ApprovalRef != ref {
				continue
			}
			if err := config.CheckApproval(decision.Text, task.ID, decision.ID, allowed); err != nil {
				return nil, false, fmt.Errorf("existing configuration approval is ambiguous")
			}
			selection, err := json.Marshal(allowed)
			if err != nil {
				return nil, false, err
			}
			if _, err := core.SelectApprovalConfiguration(selection, nil); err != nil {
				return nil, false, fmt.Errorf("existing configuration approval is invalid")
			}
			return selection, true, nil
		}
	}
	return nil, false, nil
}

func configurationApprovalRef(selection json.RawMessage) (string, error) {
	var selected struct {
		Execution struct {
			ApprovalRef string `json:"approvalRef"`
		} `json:"execution"`
	}
	if err := json.Unmarshal(selection, &selected); err != nil || selected.Execution.ApprovalRef == "" {
		return "", fmt.Errorf("effective configuration has no approval reference")
	}
	return selected.Execution.ApprovalRef, nil
}

func configurationValue(selection json.RawMessage) (config.Config, error) {
	var selected config.Config
	if err := json.Unmarshal(selection, &selected); err != nil {
		return config.Config{}, err
	}
	if err := selected.Validate(); err != nil {
		return config.Config{}, err
	}
	return selected, nil
}

func taskHasConfigurationApproval(task core.GraphTask, selected core.ApprovalConfiguration) bool {
	value, err := configurationValue(selected.Selection)
	if err != nil {
		return false
	}
	ref, err := configurationApprovalRef(selected.Selection)
	if err != nil {
		return false
	}
	prefix := core.ConfigurationApprovalID(ref, task.ID)
	for _, decision := range task.Decisions {
		if decision.ID != ref && !strings.HasPrefix(decision.ID, prefix) {
			continue
		}
		if config.CheckApproval(decision.Text, task.ID, decision.ID, value) == nil {
			return true
		}
	}
	return false
}

func taskReauthorizationReuseReport(task core.GraphTask, bundle core.ApprovalBundle) taskReauthorizationPreview {
	return taskReauthorizationPreview{
		Status: "already-authorized", TaskID: task.ID, State: task.Status,
		ProposalHash: bundle.ProposalHash, RecordHash: bundle.RecordHash,
		AuthorizedRecordHash: bundle.RecordHash,
		BundleVersion:        bundle.BundleVersion, CanonicalizationVersion: bundle.CanonicalizationVersion,
		Dimensions: bundle.Dimensions,
	}
}

// approvalReuseReport reports an already matching approval without asking for a
// second approval of the same content.
func approvalReuseReport(bundle core.ApprovalBundle) approvalGateReport {
	report := approvalGateReport{
		Reused: true, TaskID: bundle.TaskID, ApprovalID: bundle.ID,
		BundleVersion: bundle.BundleVersion, CanonicalizationVersion: bundle.CanonicalizationVersion,
		ProposalHash: bundle.ProposalHash, RecordHash: bundle.RecordHash,
		ConfigurationDigest: bundle.Configuration.Digest, BundleHash: bundle.BundleHash,
		Drifts: []core.ApprovalDrift{},
	}
	for _, stage := range core.ApprovalStages() {
		report.Stages = append(report.Stages, approvalStageReport{Stage: stage, Approval: "reused"})
	}
	return report
}

// approvalConfiguration reuses the binding check's field canonicalization and
// keeps only references: resolved credential values never enter the digest.
func approvalConfiguration() (core.ApprovalConfiguration, error) {
	resolution, err := resolveConfiguration()
	if err != nil {
		return core.ApprovalConfiguration{}, err
	}
	return approvalConfigurationFor(resolution.Config, resolution.Sources)
}

// approvalConfigurationFor digests the configuration a caller actually uses so
// that approval bundles and execution compare the same selection.
func approvalConfigurationFor(selected config.Config, sources map[string]string) (core.ApprovalConfiguration, error) {
	selection, err := json.Marshal(selected)
	if err != nil {
		return core.ApprovalConfiguration{}, err
	}
	return core.SelectApprovalConfiguration(selection, sources)
}

func approvalFieldPaths(selection json.RawMessage) []string {
	fields, err := core.SelectionFields(selection)
	if err != nil {
		return nil
	}
	return fields
}

func approvalID(taskID, recordHash, configurationDigest string, at time.Time) string {
	seed := taskID + "|" + recordHash + "|" + configurationDigest + "|" + at.UTC().Format(time.RFC3339Nano)
	return "approval-" + core.SHA256Hex([]byte(seed))[:16]
}

// authorizeError sanitizes a diagnostic so that no credential-like value is
// echoed by the single-approval flow.
func authorizeError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("task authorize: %s", core.Redact(err.Error()))
}

// validateError sanitizes a diagnostic so that no credential-like value from a
// manifest, a workspace identifier or a decoder message is echoed.
func validateError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("task validate: %s", core.Redact(err.Error()))
}

// loadGraphReadOnly observes the stored Task Graph and ledger without creating
// or migrating a workspace.
func loadGraphReadOnly() (core.TaskGraph, core.Ledger, bool, error) {
	empty := core.TaskGraph{}
	if err := validateWorkspaceStorePath(); err != nil {
		return empty, core.Ledger{}, false, err
	}
	path := workspaceDBPath()
	if _, err := os.Stat(path); err == nil {
		db, done, err := openWorkspaceSnapshot(path)
		if err != nil {
			return empty, core.Ledger{}, false, err
		}
		defer done()
		initialized, err := db.WorkspaceInitialized()
		if err != nil {
			if missingSchema(err) {
				return loadLegacyState()
			}
			return empty, core.Ledger{}, false, err
		}
		if initialized {
			state, err := db.LoadWorkspace()
			if err != nil {
				return empty, core.Ledger{}, false, err
			}
			if !state.HasGraph {
				return core.TaskGraph{Version: 1}, state.Ledger, false, nil
			}
			if err := state.Graph.CheckLedger(state.Ledger); err != nil {
				return empty, core.Ledger{}, false, err
			}
			return state.Graph, state.Ledger, true, nil
		}
	} else if !os.IsNotExist(err) {
		return empty, core.Ledger{}, false, err
	}
	return loadLegacyState()
}

// openWorkspaceSnapshot copies the workspace database with its WAL and
// shared-memory files into a temporary directory and opens the copy read-only.
// Validation therefore cannot create or modify any workspace file, even when
// SQLite has to recover a write-ahead log to read the snapshot.
func openWorkspaceSnapshot(path string) (*store.Store, func(), error) {
	dir, err := os.MkdirTemp("", "taxion-validate-")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { os.RemoveAll(dir) }
	copied := filepath.Join(dir, filepath.Base(path))
	for _, suffix := range []string{"", "-wal", "-shm"} {
		data, err := os.ReadFile(path + suffix)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		if err := os.WriteFile(copied+suffix, data, 0o600); err != nil {
			cleanup()
			return nil, nil, err
		}
	}
	db, err := store.OpenReadOnly(copied)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return db, func() {
		db.Close()
		cleanup()
	}, nil
}

func missingSchema(err error) bool {
	return strings.Contains(err.Error(), "no such table")
}

// loadLegacyState reads the JSON ledger and graph that a workspace migration
// would import, without writing either of them.
func loadLegacyState() (core.TaskGraph, core.Ledger, bool, error) {
	empty := core.TaskGraph{}
	ledger := core.Ledger{}
	if _, err := os.Stat(storePath); err == nil {
		loaded, err := core.LoadLedger(storePath)
		if err != nil {
			return empty, core.Ledger{}, false, err
		}
		ledger = loaded
	} else if !os.IsNotExist(err) {
		return empty, core.Ledger{}, false, err
	}
	graph, initialized := core.TaskGraph{Version: 1}, false
	if _, err := os.Stat(core.GraphPath(storePath)); err == nil {
		loaded, err := core.LoadTaskGraph(core.GraphPath(storePath))
		if err != nil {
			return empty, core.Ledger{}, false, err
		}
		graph, initialized = loaded, true
		if err := graph.CheckLedger(ledger); err != nil {
			return empty, core.Ledger{}, false, err
		}
	} else if !os.IsNotExist(err) {
		return empty, core.Ledger{}, false, err
	}
	return graph, ledger, initialized, nil
}

func taskGraphCmd(command string, args []string) error {
	fs := flag.NewFlagSet("task "+command, flag.ContinueOnError)
	var file, repo, manifest, confirm string
	var includeArchived bool
	if command == "plan" {
		fs.StringVar(&repo, "repo", "", "explicit repository for follow-up planning")
		fs.BoolVar(&includeArchived, "include-archived", false, "include archived history without permitting execution")
	} else if command == "track" || command == "transition" {
		fs.StringVar(&file, "file", "", "JSON request file")
	} else if command == "validate" || command == "authorize" {
		fs.StringVar(&manifest, "manifest", "", "JSON request file to validate without storing")
		fs.StringVar(&file, "file", "", "JSON request file to validate without storing")
		if command == "authorize" {
			fs.StringVar(&confirm, "confirm", "", "proposal hash printed by the previous invocation")
		}
	} else if command != "graph" {
		return fmt.Errorf("unknown task graph command %s", command)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("unexpected task graph arguments")
	}
	if command == "validate" || command == "authorize" {
		if manifest != "" && file != "" {
			return fmt.Errorf("task %s: use either --manifest or --file, not both", command)
		}
		if manifest != "" {
			file = manifest
		}
		if command == "authorize" {
			return taskAuthorizeCmd(file, confirm)
		}
		return taskValidateCmd(file)
	}
	db, state, err := loadWorkspace()
	if err != nil {
		return err
	}
	defer db.Close()
	if command == "plan" {
		plan, err := controller.PlanWithArchived(db, storePath, repo, includeArchived)
		if err != nil {
			return err
		}
		return printJSON(plan)
	}
	if command == "graph" {
		if !state.HasGraph {
			return fmt.Errorf("task graph is not initialized; use task track")
		}
		if err := state.Graph.CheckLedger(state.Ledger); err != nil {
			return err
		}
		return printJSON(state.Graph)
	}
	if file == "" {
		return fmt.Errorf("task %s: --file is required", command)
	}
	input, err := os.Open(file)
	if err != nil {
		return err
	}
	defer input.Close()
	switch command {
	case "track":
		var request struct {
			Task    core.GraphTask `json:"task"`
			Actor   string         `json:"actor"`
			Reason  string         `json:"reason"`
			Concern *core.Concern  `json:"concern,omitempty"`
		}
		if err := core.DecodeGraphInput(input, &request); err != nil {
			return err
		}
		return updateWorkspace(func(current *store.Workspace) error {
			if !current.HasGraph {
				if err := core.GuardActiveDispatch(current.Ledger.Dispatches); err != nil {
					return fmt.Errorf("resolve legacy active dispatches before graph migration: %w", err)
				}
				current.Graph = core.TaskGraph{Version: 1}
				current.HasGraph = true
			}
			if _, exists := current.Graph.GraphTask(request.Task.ID); !exists && request.Task.BudgetGuidance == nil {
				var err error
				request.Task.BudgetGuidance, err = newTaskBudgetGuidance()
				if err != nil {
					return err
				}
			}
			if err := current.Graph.Track(request.Task, request.Actor, request.Reason); err != nil {
				return err
			}
			if request.Concern != nil {
				if err := current.Graph.AddConcern(*request.Concern); err != nil {
					return err
				}
			}
			return current.Graph.CheckLedger(current.Ledger)
		})
	case "transition":
		var request struct {
			TaskID string               `json:"taskId"`
			Status core.TaskStatus      `json:"status"`
			Actor  string               `json:"actor"`
			Reason string               `json:"reason"`
			Proof  core.TransitionProof `json:"proof"`
		}
		if err := core.DecodeGraphInput(input, &request); err != nil {
			return err
		}
		return updateWorkspace(func(current *store.Workspace) error {
			if !current.HasGraph {
				return fmt.Errorf("task graph is not initialized; use task track")
			}
			return current.Graph.Transition(request.TaskID, request.Status, request.Actor, request.Reason, request.Proof)
		})
	default:
		return fmt.Errorf("unknown task graph command %s", command)
	}
}

func taskArchiveCmd(args []string) error {
	fs := flag.NewFlagSet("task archive", flag.ContinueOnError)
	tasks := fs.String("tasks", "", "comma-separated Task IDs; issued flow role Tasks are included")
	actor := fs.String("actor", "", "actor approving retirement")
	source := fs.String("source", "", "source of retirement approval")
	reason := fs.String("reason", "", "reason for retiring this work")
	dryRun := fs.Bool("dry-run", false, "preview exact scope and approval hash without writing")
	hash := fs.String("expected-hash", "", "exact hash from the reviewed preview")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *tasks == "" {
		return fmt.Errorf("task archive requires --tasks ID[,ID] and no positional arguments")
	}
	db, _, err := loadWorkspace()
	if err != nil {
		return err
	}
	defer db.Close()
	preview, err := controller.ArchiveTasks(db, controller.ArchiveRequest{
		TaskIDs: strings.Split(*tasks, ","), Actor: *actor, Source: *source,
		Reason: *reason, ExpectedHash: *hash,
	}, *dryRun)
	if err != nil {
		return err
	}
	return printJSON(struct {
		controller.ArchivePreview
		Archived bool `json:"archived"`
	}{preview, !*dryRun})
}

func dispatchTransitionCmd(args []string) error {
	fs := flag.NewFlagSet("dispatch transition", flag.ContinueOnError)
	id := fs.String("id", "", "dispatch ID")
	status := fs.String("status", "", "target status")
	actor := fs.String("actor", "", "actor")
	reason := fs.String("reason", "", "reason")
	resultFile := fs.String("result-file", "", "JSON result file required when ending a Dispatch")
	validationTimeout := fs.Duration("validation-timeout", 10*time.Minute, "controller validation deadline")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" || *status == "" || *actor == "" || *reason == "" {
		return fmt.Errorf("dispatch transition: --id, --status, --actor and --reason are required")
	}
	if *validationTimeout <= 0 {
		return fmt.Errorf("dispatch transition: --validation-timeout must be positive")
	}
	ctx, cancel := signalContext(*validationTimeout)
	defer cancel()
	var result *core.DispatchResult
	if *resultFile != "" {
		file, err := os.Open(*resultFile)
		if err != nil {
			return err
		}
		defer file.Close()
		var decoded core.DispatchResult
		if err := core.DecodeGraphInput(file, &decoded); err != nil {
			return fmt.Errorf("dispatch transition result: %w", err)
		}
		result = &decoded
	}
	return updateWorkspace(func(state *store.Workspace) error {
		if !state.HasGraph {
			return fmt.Errorf("dispatch lifecycle requires a Task Graph")
		}
		target := core.DispatchStatus(*status)
		if target == core.DispatchRunning {
			dispatch, ok := state.Graph.GraphDispatch(*id)
			if !ok {
				return fmt.Errorf("dispatch %q not found", *id)
			}
			if dispatch.Instruction.Version >= 4 && dispatch.ExecutionStartSHA == "" {
				start, err := gitproof.ObserveStart(dispatch)
				if err != nil {
					return err
				}
				for index := range state.Graph.Dispatches {
					if state.Graph.Dispatches[index].ID == *id {
						state.Graph.Dispatches[index].ExecutionStartSHA = start
					}
				}
			}
		}
		if result != nil {
			result.GitEvidence = nil
		}
		if target == core.DispatchSucceeded {
			dispatch, ok := state.Graph.GraphDispatch(*id)
			if !ok {
				return fmt.Errorf("dispatch %q not found", *id)
			}
			if dispatch.Status != core.DispatchRunning || !dispatch.ExecutionStarted {
				return fmt.Errorf("successful result requires a running Dispatch")
			}
			if dispatch.Recipient == "REVIEWER" {
				return fmt.Errorf("Reviewer completion requires runtime finalization and managed worktree release")
			}
			if result == nil {
				return fmt.Errorf("successful dispatch requires a result file")
			}
			if err := gitproof.ExecuteValidations(ctx, dispatch, result); err != nil {
				return fmt.Errorf("dispatch Git verification: %w", err)
			}
		}
		ack := target == core.DispatchAcknowledged || target == core.DispatchRunning
		started := target == core.DispatchRunning
		if err := state.Graph.TransitionDispatch(*id, target, *actor, *reason, ack, started, result); err != nil {
			return err
		}
		for i := range state.Ledger.Dispatches {
			if state.Ledger.Dispatches[i].ID == *id {
				state.Ledger.Dispatches[i].Active = target == core.DispatchIssued || target == core.DispatchAcknowledged ||
					target == core.DispatchRunning || target == core.DispatchCancelRequested || target == core.DispatchSuspended
			}
		}
		return nil
	})
}
