package main

import (
	"crypto/subtle"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
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
