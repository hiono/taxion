package controller

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/gitproof"
	"github.com/hiono/taxion/internal/store"
)

// IntakeRequest selects legacy Task intake when Task.JobID is empty and
// first-class Job intake only when Task.JobID names an existing Job.
type IntakeRequest struct {
	ContextSources          []core.ContextDocument `json:"contextSources,omitempty"`
	ObservationHash         string                 `json:"observationHash,omitempty"`
	ReviewInput             *core.ReviewInput      `json:"reviewInput,omitempty"`
	ReconciliationHash      string                 `json:"reconciliationHash,omitempty"`
	Request                 string                 `json:"request"`
	Task                    core.GraphTask         `json:"task"`
	Actor                   string                 `json:"actor"`
	Source                  string                 `json:"source"`
	PlanningSelection       *config.Config         `json:"planningSelection,omitempty"`
	PlanningReason          string                 `json:"planningReason,omitempty"`
	ConfigurationDecisionID string                 `json:"configurationDecisionId,omitempty"`
}

type JobIntakeRequest struct {
	JobID   string `json:"jobId,omitempty"`
	Request string `json:"request"`
	Goal    string `json:"goal"`
	Actor   string `json:"actor"`
	Source  string `json:"source"`
}

type IntakeConfirmation struct {
	ObservationHash         string               `json:"observationHash,omitempty"`
	ReconciliationHash      string               `json:"reconciliationHash,omitempty"`
	TaskID                  string               `json:"taskId"`
	ExpectedHash            string               `json:"expectedHash"`
	Actor                   string               `json:"actor"`
	Source                  string               `json:"source"`
	Reason                  string               `json:"reason"`
	Selection               config.Config        `json:"selection"`
	AdditionalTaskIDs       []string             `json:"additionalTaskIds"`
	ConfigurationDecisionID string               `json:"configurationDecisionId,omitempty"`
	ReplacesFlow            string               `json:"replacesFlow,omitempty"`
	Target                  *core.FeedbackTarget `json:"target,omitempty"`
	RequestOperationID      string               `json:"requestOperationId,omitempty"`
	RequestBasisHash        string               `json:"requestBasisHash,omitempty"`
	JobContract             *core.JobContract    `json:"jobContract,omitempty"`
	ExpectedJobContractHash string               `json:"expectedJobContractHash,omitempty"`
}

// VerifyRequestConfirmation reads the formal intake decision and its original
// contract. A checkpoint's approval flag is never an authorization source.
func VerifyRequestConfirmation(graph core.TaskGraph, taskID, operationID, proposalHash, authorizationID, basisHash string, selected config.Config) error {
	task, ok := graph.GraphTask(taskID)
	if ok && modelFreeVerification(task) && task.JobID != "" {
		return fmt.Errorf("taxion: model-free verification cannot be attached to a Job")
	}
	if !ok || taskID != operationID || authorizationID != "intake-confirmation:"+taskID ||
		task.AuthorizationID != authorizationID || basisHash == "" {
		return fmt.Errorf("taxion: request approval identity mismatch")
	}
	var confirmation *IntakeConfirmation
	for _, decision := range task.Decisions {
		if decision.ID != authorizationID {
			continue
		}
		var record IntakeConfirmation
		if confirmation != nil || core.DecodeGraphInput(strings.NewReader(decision.Text), &record) != nil ||
			decision.Source != "human:console:"+operationID {
			return fmt.Errorf("taxion: request approval is invalid or ambiguous")
		}
		confirmation = &record
	}
	if confirmation == nil || confirmation.TaskID != taskID || confirmation.ExpectedHash != proposalHash ||
		confirmation.RequestOperationID != operationID || confirmation.RequestBasisHash != basisHash ||
		confirmation.Selection != selected || confirmation.Source != "human:console:"+operationID {
		return fmt.Errorf("taxion: request checkpoint does not match formal approval")
	}
	var draftFound bool
	var approved *core.GraphTask
	for _, event := range graph.History {
		if event.TaskID != taskID {
			continue
		}
		if event.Task.Status == core.Draft {
			hash, err := IntakeHash(event.Task)
			draftFound = draftFound || err == nil && hash == proposalHash
		}
		if event.Task.Status == core.Ready && event.Task.AuthorizationID == authorizationID && approved == nil {
			copy := event.Task
			approved = &copy
		}
	}
	if !draftFound || approved == nil {
		return fmt.Errorf("taxion: request approval lacks its original contract")
	}
	flow, exists := graph.ReviewFlow(operationID + "-flow")
	if exists {
		if flow.TaskID != taskID || !reflect.DeepEqual(flow.Contract, *approved) || !flowContractMatches(graph, flow, task) {
			return fmt.Errorf("taxion: approved request contract or execution identity drifted")
		}
	} else if !reflect.DeepEqual(task, *approved) {
		return fmt.Errorf("taxion: approved request contract drifted")
	}
	if !modelFreeVerification(task) && task.JobID == "" {
		if confirmation.JobContract != nil || confirmation.ExpectedJobContractHash != "" {
			return fmt.Errorf("taxion: legacy Task approval cannot carry Job authorization")
		}
	}
	if !modelFreeVerification(task) && task.JobID != "" {
		if confirmation.JobContract == nil {
			return fmt.Errorf("taxion: request approval lacks its Job contract")
		}
		if err := taskCoveredByJobContract(task, *confirmation.JobContract); err != nil {
			return err
		}
		job, ok := graph.Job(task.JobID)
		if !ok || job.Status == core.JobCancelled {
			return fmt.Errorf("taxion: approved request Task references no usable Job")
		}
		if confirmation.ExpectedJobContractHash == "" {
			return fmt.Errorf("taxion: request approval lacks the exact Job contract hash")
		}
		approval, ok := graph.CurrentJobApproval(job.ID)
		if !ok || approval.ProposalHash != confirmation.ExpectedJobContractHash ||
			approval.Proposal.Revision != confirmation.JobContract.Revision ||
			!reflect.DeepEqual(approval.Proposal.Contract, *confirmation.JobContract) {
			return fmt.Errorf("taxion: request approval lacks the exact immutable Job approval")
		}
	}
	return nil
}

func IntakeHash(task core.GraphTask) (string, error) {
	data, err := json.Marshal(task)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(data)), nil
}

func modelFreeVerification(task core.GraphTask) bool {
	return task.ModelFree && task.Type == "VERIFICATION" && task.Owner == "USER"
}

// ProposeIntake records the original request and a contract for the user to inspect.
// It never treats natural-language text as authorization or starts a worker.
func newIntakeJobID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("taxion: create Job identity: %w", err)
	}
	return "job-" + hex.EncodeToString(value[:]), nil
}

// ProposeJob creates a durable Job without creating a Task. A caller-supplied
// JobID makes exact retries idempotent and can later be referenced by multiple Tasks.
func ProposeJob(db *store.Store, request JobIntakeRequest) (core.Job, error) {
	if db == nil || strings.TrimSpace(request.Request) == "" || strings.TrimSpace(request.Goal) == "" ||
		strings.TrimSpace(request.Actor) == "" || strings.TrimSpace(request.Source) == "" {
		return core.Job{}, fmt.Errorf("taxion: Job proposal requires request, goal, actor and source")
	}
	jobID := strings.TrimSpace(request.JobID)
	if jobID == "" {
		var err error
		jobID, err = newIntakeJobID()
		if err != nil {
			return core.Job{}, err
		}
	}
	var result core.Job
	err := db.UpdateWorkspace(func(state *store.Workspace) error {
		if existing, ok := state.Graph.Job(jobID); ok {
			if existing.Request != request.Request || existing.Goal != request.Goal ||
				existing.Status != core.JobDraft {
				return fmt.Errorf("taxion: Job ID already contains another proposal")
			}
			result = existing
			return nil
		}
		job := core.Job{
			ID: jobID, Request: request.Request, Goal: request.Goal, Status: core.JobDraft,
		}
		if err := state.Graph.CreateJobWithActor(job, request.Actor,
			"Job proposed from "+request.Source, time.Now().UTC()); err != nil {
			return err
		}
		result, _ = state.Graph.Job(jobID)
		return nil
	})
	if err != nil {
		return core.Job{}, err
	}
	return result, nil
}

func ProposeIntake(db *store.Store, request IntakeRequest) (core.GraphTask, error) {
	if db == nil || strings.TrimSpace(request.Request) == "" || strings.TrimSpace(request.Actor) == "" ||
		strings.TrimSpace(request.Source) == "" || request.Task.Status != core.Draft ||
		request.Task.AuthorizationID != "" || len(request.Task.Decisions) != 0 ||
		(request.Task.Owner != "CODER" && !(modelFreeVerification(request.Task) ||
			request.Task.Owner == "USER" && (request.ReconciliationHash != "" ||
				request.ReviewInput != nil && request.Task.Type == "VERIFICATION" && !request.Task.ModelFree))) ||
		request.Task.Reviewer != "REVIEWER" || modelFreeVerification(request.Task) && request.Task.JobID != "" {
		return core.GraphTask{}, fmt.Errorf("taxion: intake requires an original request, sourced DRAFT contract and independent Coder/Reviewer without authorization; model-free verification cannot join a Job")
	}
	text, err := json.Marshal(struct {
		Kind    string `json:"kind"`
		Request string `json:"request"`
		Actor   string `json:"actor"`
	}{"human-request", request.Request, request.Actor})
	if err != nil {
		return core.GraphTask{}, err
	}
	task := request.Task
	if task.FileAccess == nil {
		task.FileAccess = core.DefaultFileAccessPolicy()
	}
	if task.BudgetGuidance == nil && request.ReconciliationHash == "" {
		mode := ""
		var selected config.Config
		if request.PlanningSelection != nil {
			mode = request.PlanningSelection.Execution.BudgetGuidance
			selected = *request.PlanningSelection
		}
		task.BudgetGuidance, err = core.NewBillingBudgetGuidance(mode, "", selected.Billing)
		if err != nil {
			return core.GraphTask{}, err
		}
	}
	task.Decisions = []core.TaskDecision{{ID: "intake:" + task.ID, Source: request.Source, Text: string(text)}}
	for _, document := range request.ContextSources {
		decision, err := core.ContextDocumentDecision(document, "context-source:"+task.ID+":"+document.ID)
		if err != nil {
			return core.GraphTask{}, err
		}
		task.Decisions = append(task.Decisions, decision)
	}
	if _, err := core.ContextDocumentsFromDecisions(task.Decisions); err != nil {
		return core.GraphTask{}, err
	}
	if request.ReviewInput != nil {
		if err := request.ReviewInput.Validate(); err != nil {
			return core.GraphTask{}, err
		}
		packet, err := json.Marshal(request.ReviewInput)
		if err != nil {
			return core.GraphTask{}, err
		}
		task.Decisions = append(task.Decisions, core.TaskDecision{
			ID: "review-input:" + task.ID, Source: request.Source, Text: string(packet),
		})
	}
	if request.PlanningSelection != nil {
		if strings.TrimSpace(request.PlanningReason) == "" || request.PlanningSelection.Repository.Path != task.Repo {
			return core.GraphTask{}, fmt.Errorf("taxion: isolated intake planning requires explicit configuration reason and matching repository")
		}
		selection := *request.PlanningSelection
		decisionID := request.ConfigurationDecisionID
		if decisionID == "" {
			decisionID = selection.Execution.ApprovalRef
		}
		approval, err := json.Marshal(config.Approval{
			Kind: config.ApprovalKind, TaskID: task.ID, Reason: request.PlanningReason, Allowed: []config.Config{selection},
		})
		if err != nil {
			return core.GraphTask{}, err
		}
		if err := config.CheckApproval(string(approval), task.ID, decisionID, selection); err != nil {
			return core.GraphTask{}, err
		}
		task.Decisions = append(task.Decisions, core.TaskDecision{ID: decisionID, Source: request.Source, Text: string(approval)})
	} else if request.PlanningReason != "" || request.ConfigurationDecisionID != "" {
		return core.GraphTask{}, fmt.Errorf("taxion: planning reason requires an explicit selected configuration")
	}
	var result core.GraphTask
	err = db.UpdateWorkspace(func(state *store.Workspace) error {
		if !state.HasGraph {
			state.HasGraph, state.Graph = true, core.TaskGraph{Version: 1}
		}
		if request.ObservationHash != "" {
			if request.ReconciliationHash != "" {
				return fmt.Errorf("taxion: observation and unstarted reconciliation are distinct input sources")
			}
			reference, err := observationDecision(*state, task, request.ObservationHash)
			if err != nil {
				return err
			}
			task.Decisions = append(task.Decisions, reference)
			if err := verifyObservedIntake(*state, task, request.ObservationHash, nil); err != nil {
				return err
			}
		}
		predecessor, err := artifactPredecessorDecision(state.Graph, task)
		if err != nil {
			return err
		}
		if predecessor != nil {
			task.Decisions = append(task.Decisions, *predecessor)
		}
		if request.ReconciliationHash != "" {
			if err := verifyReconciledInput(*state, task, request.ReconciliationHash); err != nil {
				return err
			}
			task.Decisions = append(task.Decisions, core.TaskDecision{
				ID: "reconciled-input:" + task.ID, Source: request.Source, Text: request.ReconciliationHash,
			})
			if continuation := reviewContinuationDecision(*state, task, request.ReconciliationHash); continuation != nil {
				task.Decisions = append(task.Decisions, *continuation)
			}
		}
		if previous, ok := state.Graph.GraphTask(task.ID); ok {
			if previous.JobID != task.JobID {
				return fmt.Errorf("taxion: intake replay cannot change explicit Job membership")
			}
			if !reflect.DeepEqual(previous, task) {
				return fmt.Errorf("taxion: intake Task ID already contains another proposal; use a new Task ID")
			}
			result = previous
			return nil
		}
		for _, previous := range state.Ledger.Tasks {
			if previous.ID == task.ID {
				return fmt.Errorf("taxion: intake Task ID already exists in the ledger; use a new Task ID")
			}
		}
		// Job membership is explicit. An unbound Task remains in the legacy
		// Task/ReviewFlow authorization model; intake never creates or infers a Job.
		if task.JobID != "" {
			job, ok := state.Graph.Job(task.JobID)
			if !ok || job.Status == core.JobCancelled {
				return fmt.Errorf("taxion: intake Task references an unknown or cancelled Job")
			}
			if job.Contract != nil {
				if err := taskCoveredByJobContract(task, *job.Contract); err != nil {
					return err
				}
			}
			if job.Status == core.JobCompleted || job.Status == core.JobFailed {
				if err := state.Graph.TransitionJob(job.ID, core.JobWaiting, "controller",
					"successor Task explicitly attached", time.Now().UTC()); err != nil {
					return err
				}
			}
		}
		for _, predecessorID := range []string{task.ParentID, task.Supersedes} {
			if predecessorID == "" {
				continue
			}
			predecessor, exists := state.Graph.GraphTask(predecessorID)
			if !exists {
				return fmt.Errorf("taxion: predecessor Task %s is unavailable for explicit intake association", predecessorID)
			}
			if predecessor.JobID != task.JobID {
				return fmt.Errorf("taxion: Task ancestry cannot cross legacy Task and Job authorization modes")
			}
		}
		if err := state.Graph.Track(task, request.Actor, "Propose explicit scope and acceptance for the original request"); err != nil {
			return err
		}
		state.Ledger.Tasks = append(state.Ledger.Tasks, core.Task{ID: task.ID, Title: task.Title, Repo: task.Repo, CreatedAt: time.Now().UTC()})
		result, _ = state.Graph.GraphTask(task.ID)
		return nil
	})
	return result, err
}

// BuildJobContractForIntake binds a new Job to the exact Task scope and
// the explicitly approved runtime configuration and execution budget.
func BuildJobContractForIntake(task core.GraphTask, configuration core.ApprovalConfiguration, budget core.JobBudget, deadline time.Time) (core.JobContract, error) {
	if task.FileAccess == nil || strings.TrimSpace(task.Repo) == "" ||
		len(task.Acceptance) == 0 || len(task.RequiredEvidence) == 0 {
		return core.JobContract{}, fmt.Errorf("taxion: Job contract requires repository access, acceptance criteria and evidence")
	}
	operations := []string{"repository.read", "repository.write", "process.exec"}
	coderOperations := []string{"repository.read", "repository.write", "process.exec"}
	if len(task.ValidationCommands) > 0 {
		operations = append(operations, "test.run")
		coderOperations = append(coderOperations, "test.run")
	}
	roleOperations := map[string][]string{
		"PLANNER":  {"repository.read"},
		"CODER":    coderOperations,
		"REVIEWER": {"repository.read"},
	}
	noExtensions, err := core.NewJobRoleToolPolicy(nil)
	if err != nil {
		return core.JobContract{}, err
	}
	roleToolPolicies := map[string]core.JobRoleToolPolicy{
		"PLANNER":  noExtensions,
		"CODER":    noExtensions,
		"REVIEWER": noExtensions,
	}
	contract := core.JobContract{
		Revision:         1,
		Roles:            []string{"PLANNER", "CODER", "REVIEWER"},
		Acceptance:       slices.Clone(task.Acceptance),
		RequiredEvidence:     slices.Clone(task.RequiredEvidence),
		ValidationCommands:   append([]string(nil), task.ValidationCommands...),
		Repositories: []core.JobRepositoryScope{{
			Repository: task.Repo,
			FileAccess: *task.FileAccess,
		}},
		Operations:           operations,
		RoleOperations:       roleOperations,
		RoleToolPolicies:     roleToolPolicies,
		Budget:               budget,
		Deadline:             deadline,
		SecurityRestrictions: []string{"no credential export", "no network access"},
		Configuration:        configuration,
	}
	if err := contract.Validate(); err != nil {
		return core.JobContract{}, err
	}
	return contract, nil
}

func cloneIntakeJobContract(contract *core.JobContract) (*core.JobContract, error) {
	if contract == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(contract)
	if err != nil {
		return nil, err
	}
	var clone core.JobContract
	if err := json.Unmarshal(encoded, &clone); err != nil {
		return nil, err
	}
	return &clone, nil
}

func taskCoveredByJobContract(task core.GraphTask, contract core.JobContract) error {
	if !core.TaskWithinJobContract(task, contract) {
		return fmt.Errorf("taxion: Task exceeds the approved Job contract")
	}
	return nil
}

func appendConfirmedJobApproval(state *store.Workspace, task core.GraphTask, request IntakeConfirmation) error {
	if modelFreeVerification(task) {
		return nil
	}
	if state == nil {
		return fmt.Errorf("taxion: Job approval requires a workspace")
	}

	// Stage the contract and approval on a candidate graph. A rejected or
	// replayed confirmation must not mutate the caller's workspace.
	graph := state.Graph
	graph.Jobs = append([]core.Job(nil), state.Graph.Jobs...)
	if task.JobID == "" || request.JobContract == nil {
		return fmt.Errorf("taxion: Job intake requires an explicit Job association and contract")
	}
	job, ok := graph.Job(task.JobID)
	if !ok || job.Status == core.JobCancelled {
		return fmt.Errorf("taxion: Job intake references no usable Job")
	}
	contract := *request.JobContract
	if err := contract.Validate(); err != nil {
		return err
	}
	if err := taskCoveredByJobContract(task, contract); err != nil {
		return err
	}
	if job.Contract == nil {
		if contract.Revision != 1 {
			return fmt.Errorf("taxion: first Job approval must use revision 1")
		}
	} else {
		currentHash, err := core.CanonicalSHA256(*job.Contract)
		if err != nil {
			return err
		}
		requestedHash, err := core.CanonicalSHA256(contract)
		if err != nil {
			return err
		}
		if currentHash != requestedHash {
			if contract.Revision != job.Contract.Revision+1 {
				return fmt.Errorf("taxion: changed Job contract requires the next revision (current=%d requested=%d)", job.Contract.Revision, contract.Revision)
			}
		} else if contract.Revision != job.Contract.Revision {
			return fmt.Errorf("taxion: unchanged Job contract must retain its revision")
		}
	}
	job.Contract = &contract
	proposal, err := core.JobProposal(job)
	if err != nil {
		return err
	}
	proposalHash, err := proposal.Hash()
	if err != nil {
		return err
	}
	if request.ExpectedJobContractHash == "" || proposalHash != request.ExpectedJobContractHash {
		return fmt.Errorf("taxion: Job contract differs from the exact proposal shown for approval")
	}
	if existing, ok := graph.CurrentJobApproval(job.ID); ok && existing.ProposalHash == proposalHash {
		return nil
	}
	for _, existing := range graph.JobApprovals {
		if existing.Proposal.JobID == job.ID && existing.Proposal.Revision == contract.Revision {
			return fmt.Errorf("taxion: Job revision already has an approval history but is not currently authorized")
		}
	}
	approval, err := core.NewJobApproval(
		fmt.Sprintf("job-approval:%s:%d", job.ID, contract.Revision),
		request.Actor, "user", request.Reason, proposal, time.Now().UTC(),
	)
	if err != nil {
		return err
	}
	job.UpdatedAt = time.Now().UTC()
	for i := range graph.Jobs {
		if graph.Jobs[i].ID == job.ID {
			graph.Jobs[i] = job
			break
		}
	}
	if err := graph.AppendJobApproval(approval); err != nil {
		return err
	}
	state.Graph = graph
	return nil
}

// ConfirmIntake confirms exactly the displayed contract. A changed contract is
// a sourced successor Task, never an edit to issued instructions or old budgets.
func ConfirmIntake(db *store.Store, request IntakeConfirmation) (core.GraphTask, error) {
	if db == nil || strings.TrimSpace(request.Actor) == "" || strings.TrimSpace(request.Source) == "" ||
		strings.TrimSpace(request.Reason) == "" || request.ExpectedHash == "" {
		return core.GraphTask{}, fmt.Errorf("taxion: intake confirmation requires exact proposal hash, actor, source and reason")
	}
	initialState, err := db.LoadWorkspace()
	if err != nil {
		return core.GraphTask{}, err
	}
	taskForContract, taskFound := initialState.Graph.GraphTask(request.TaskID)
	if !initialState.HasGraph || !taskFound {
		return core.GraphTask{}, fmt.Errorf("taxion: intake confirmation requires a saved proposal")
	}
	modelFree := modelFreeVerification(taskForContract)
	if modelFree {
		if taskForContract.JobID != "" {
			return core.GraphTask{}, fmt.Errorf("taxion: model-free verification cannot be attached to a Job")
		}
		if request.JobContract != nil || request.ExpectedJobContractHash != "" {
			return core.GraphTask{}, fmt.Errorf("taxion: model-free verification cannot include Job execution authorization")
		}
	} else if taskForContract.JobID == "" {
		if request.JobContract != nil || request.ExpectedJobContractHash != "" {
			return core.GraphTask{}, fmt.Errorf("taxion: legacy Task intake cannot include Job execution authorization")
		}
	} else {
		job, ok := initialState.Graph.Job(taskForContract.JobID)
		if !ok || job.Status == core.JobCancelled {
			return core.GraphTask{}, fmt.Errorf("taxion: Job intake requires an existing, usable Job")
		}
		if request.JobContract == nil {
			if job.Contract == nil {
				return core.GraphTask{}, fmt.Errorf("taxion: Job intake requires an explicitly proposed Job contract")
			}
			request.JobContract, err = cloneIntakeJobContract(job.Contract)
			if err != nil {
				return core.GraphTask{}, err
			}
		}
		if strings.TrimSpace(request.ExpectedJobContractHash) == "" {
			return core.GraphTask{}, fmt.Errorf("taxion: Job intake requires the exact displayed Job contract hash")
		}
	}
	if modelFree {
		if request.Selection != (config.Config{}) || len(request.AdditionalTaskIDs) != 0 ||
			request.ConfigurationDecisionID != "" || request.ReplacesFlow != "" || request.Target != nil ||
			request.ReconciliationHash != "" {
			return core.GraphTask{}, fmt.Errorf("taxion: model-free verification confirmation cannot include runtime configuration or execution authorization")
		}
	} else if err := request.Selection.Validate(); err != nil {
		return core.GraphTask{}, err
	}
	text, err := json.Marshal(request)
	if err != nil {
		return core.GraphTask{}, err
	}
	confirmation := core.TaskDecision{ID: "intake-confirmation:" + request.TaskID, Source: request.Source, Text: string(text)}
	var result core.GraphTask
	err = db.UpdateWorkspace(func(state *store.Workspace) error {
		task, ok := state.Graph.GraphTask(request.TaskID)
		if !state.HasGraph || !ok {
			return fmt.Errorf("taxion: intake confirmation requires a saved proposal")
		}
		if modelFreeVerification(task) != modelFree {
			return fmt.Errorf("taxion: Task model-free classification changed during confirmation")
		}
		if !modelFree {
			if err := task.BudgetGuidance.CheckBillingContract(request.Selection.Billing); err != nil {
				return err
			}
		}
		for _, decision := range task.Decisions {
			if decision.ID == confirmation.ID {
				if decision != confirmation {
					return fmt.Errorf("taxion: intake confirmation cannot be rewritten")
				}
				if err := appendConfirmedIntakeApproval(state, task, request); err != nil {
					return err
				}
				result = task
				return nil
			}
		}
		hash, err := IntakeHash(task)
		if err != nil {
			return err
		}
		decisionCount := len(task.Decisions)
		if decisionCount == 0 {
			return fmt.Errorf("taxion: intake proposal lacks its original request")
		}
		contextSources, err := core.ContextDocumentsFromDecisions(task.Decisions)
		if err != nil {
			return err
		}
		decisionCount -= len(contextSources)
		input, err := core.ReviewInputFromDecisions(task.Decisions)
		if err != nil {
			return err
		}
		if input != nil {
			decisionCount--
		}
		observationHash, err := taskObservationHash(task)
		if err != nil {
			return err
		}
		if observationHash != "" || request.ObservationHash != "" {
			if request.ReconciliationHash != "" {
				return fmt.Errorf("taxion: observed input cannot be relabelled as an unstarted reconciliation")
			}
			selection, err := json.Marshal(request.Selection)
			if err != nil {
				return err
			}
			if err := verifyObservedIntake(*state, task, request.ObservationHash, selection); err != nil {
				return err
			}
			decisionCount--
		}
		predecessor, err := artifactPredecessorDecision(state.Graph, task)
		if err != nil {
			return err
		}
		if predecessor != nil {
			if request.ReplacesFlow != strings.TrimPrefix(predecessor.Source, "flow:") ||
				request.Target != nil || !slices.Contains(task.Decisions, *predecessor) {
				return fmt.Errorf("taxion: artifact successor requires its exact proposal-bound failed receipt, replacesFlow and omitted target")
			}
			decisionCount--
		}
		if request.ReconciliationHash != "" {
			if request.ReplacesFlow == "" || request.Target == nil {
				return fmt.Errorf("taxion: reconciled successor requires its predecessor and exact cancelled Dispatch target")
			}
			decision := core.TaskDecision{ID: "reconciled-input:" + task.ID,
				Source: task.Decisions[0].Source, Text: request.ReconciliationHash}
			if !slices.Contains(task.Decisions, decision) {
				return fmt.Errorf("taxion: reconciliation must be included in the exact intake proposal hash")
			}
			if err := verifyReconciledInput(*state, task, request.ReconciliationHash); err != nil {
				return err
			}
			decisionCount--
		}
		if continuation := reviewContinuationDecision(*state, task, request.ReconciliationHash); continuation != nil {
			if !slices.Contains(task.Decisions, *continuation) {
				return fmt.Errorf("taxion: executed Reviewer continuation must retain its exact readable saved output")
			}
			decisionCount--
		}
		if hash != request.ExpectedHash || task.Status != core.Draft || task.AuthorizationID != "" ||
			!modelFree && task.Repo != request.Selection.Repository.Path || (decisionCount != 1 && decisionCount != 2) ||
			task.Decisions[0].ID != "intake:"+task.ID ||
			core.CheckReviewValidationCommands(task.Decisions, task.ValidationCommands) != nil {
			return fmt.Errorf("taxion: intake proposal changed or lacks explicit execution/validation contract")
		}
		if request.ReplacesFlow != "" {
			flow, ok := state.Graph.ReviewFlow(request.ReplacesFlow)
			if !ok || flow.RunnerID != "" || flow.Status == "COMPLETED" ||
				!flow.HandoffHeld() || task.Supersedes != flow.TaskID || task.CreatedFrom != "flow:"+flow.ID {
				return fmt.Errorf("taxion: revised contract requires a held predecessor, no runner, exact target and explicit supersedes/source relation")
			}
			for _, id := range flow.DispatchIDs {
				dispatch, _ := state.Graph.GraphDispatch(id)
				if active(dispatch.Status) {
					return fmt.Errorf("taxion: stop and reconcile every predecessor execution before reapproval")
				}
			}
			if request.ObservationHash != "" {
				observation, _ := state.Records.InputObservation(request.ObservationHash)
				if observation.SourceFlowID != flow.ID {
					return fmt.Errorf("taxion: observed successor must retain its exact source Flow")
				}
				if observation.SourceDispatchID != "" {
					last, _ := state.Graph.GraphDispatch(observation.SourceDispatchID)
					target, err := core.ObserveFeedbackTarget(last)
					if request.Target == nil || err != nil || target != *request.Target {
						return core.ErrFeedbackStale
					}
				} else if request.Target != nil {
					return fmt.Errorf("taxion: observation without a Dispatch must omit feedback target")
				}
			} else if flow.Artifact != nil {
				if len(flow.DispatchIDs) == 0 && request.Target != nil || task.BaselineSHA != flow.Contract.BaselineSHA ||
					len(flow.DispatchIDs) == 0 && task.TargetSHA != flow.Artifact.Target.CommitSHA ||
					task.TargetTreeSHA != flow.Artifact.Target.TreeSHA ||
					!slices.Equal(task.TargetChangedFiles, flow.Artifact.Target.ChangedFiles) {
					return fmt.Errorf("taxion: artifact successor must retain its exact input target without a model feedback target")
				}
				if len(flow.DispatchIDs) != 0 {
					last, _ := state.Graph.GraphDispatch(flow.LastDispatchID())
					target, err := core.ObserveFeedbackTarget(last)
					if request.Target == nil || err != nil || target != *request.Target {
						return core.ErrFeedbackStale
					}
				}
				if err := gitproof.VerifyArtifact(flow.Contract, flow.Artifact.Target, flow.Artifact.Configuration.Selection); err != nil {
					return err
				}
				if len(flow.DispatchIDs) == 0 {
					selection, err := json.Marshal(request.Selection)
					if err != nil {
						return err
					}
					if err := gitproof.VerifyArtifact(task, flow.Artifact.Target, selection); err != nil {
						return fmt.Errorf("taxion: successor worktree does not preserve the failed artifact input: %w", err)
					}
				}
			} else {
				last, _ := state.Graph.GraphDispatch(flow.LastDispatchID())
				unstarted := last.Recipient == "CODER" && last.Status == core.DispatchCancelled &&
					!last.ExecutionStarted && last.RuntimeSessionID == "" && last.Result != nil &&
					flow.StopCode == "RECONTRACT" &&
					slices.Contains(last.Result.Evidence, "not-started:"+last.ID)
				if unstarted {
					if request.Target != nil || task.BaselineSHA != last.Instruction.BaselineSHA {
						return fmt.Errorf("taxion: unstarted Dispatch re-contract must retain its baseline and omit a feedback target")
					}
				} else {
					if request.Target == nil {
						return fmt.Errorf("taxion: executed predecessor re-contract requires an observed target")
					}
					target, err := core.ObserveFeedbackTarget(last)
					if err != nil || target != *request.Target {
						return core.ErrFeedbackStale
					}
				}
				if last.Recipient == "CODER" && last.Status == core.DispatchCancelled {
					if unstarted {
						// An unstarted Dispatch has no worktree changes to transfer.
					} else if request.ReconciliationHash != "" {
						audit, err := reconciledInput(*state, flow.ID, request.ReconciliationHash)
						if err != nil || audit.Target != *request.Target {
							return fmt.Errorf("taxion: reconciled input does not match the cancelled predecessor")
						}
					} else if last.Result == nil || last.Result.PartialGitEvidence == nil {
						return fmt.Errorf("taxion: cancelled Coder work requires verified partial Git evidence before successor approval")
					} else {
						if err := gitproof.VerifyPartialSuccessor(state.Records.RestoredDispatch(last), task); err != nil {
							return fmt.Errorf("taxion: successor does not preserve the verified partial work: %w", err)
						}
					}
				} else if flow.Status == "STOPPED" &&
					last.Recipient == "CODER" && last.Status == core.DispatchSucceeded {
					if _, err := budgetContinuationSource(state.Graph, task, last.ID, state.Records); err != nil {
						return fmt.Errorf("taxion: successor does not preserve the completed Coder result: %w", err)
					}
				} else if last.Recipient == "REVIEWER" && (flow.StopCode == "BUDGET_LIMIT" ||
					flow.StopCode == "SAFETY_STOP" && last.Status == core.DispatchCancelled) {
					if len(flow.DispatchIDs) < 2 {
						return fmt.Errorf("taxion: stopped Reviewer requires a saved Coder predecessor")
					}
					if _, err := budgetContinuationSource(state.Graph, task, flow.DispatchIDs[len(flow.DispatchIDs)-2], state.Records); err != nil {
						return err
					}
				}
				if last.Result != nil && last.Result.OutputSHA != "" && task.Worktree == flow.Contract.Worktree &&
					task.BaselineSHA != last.Result.OutputSHA {
					return fmt.Errorf("taxion: successor using the same worktree must retain the recorded latest output SHA")
				}
			}
			// Preserve prior result, instruction, approval and ceiling snapshots.
			if request.ReconciliationHash == "" && flow.Status != "STOPPED" {
				flow.Status, flow.Reason = "STOPPED", "the user explicitly confirmed successor contract "+task.ID
				storeFlow(state, flow)
			}
		} else if task.Supersedes != "" || request.Target != nil {
			return fmt.Errorf("taxion: a superseding contract requires explicit predecessor-flow reapproval")
		}
		if !modelFree {
			// Legacy bridge: this Task-level configuration decision is still consumed by
			// the current requestCommand/ReviewFlow execution path. It is not JobApproval
			// and must never grant Job-level authority. Remove it after Job-native request
			// execution no longer reads Task configuration decisions.
			approvalText, err := json.Marshal(config.Approval{
				Kind: config.ApprovalKind, TaskID: task.ID, Reason: request.Reason,
				Allowed: []config.Config{request.Selection}, AdditionalTaskIDs: request.AdditionalTaskIDs,
			})
			if err != nil {
				return err
			}
			decisionID := request.ConfigurationDecisionID
			if decisionID == "" {
				decisionID = request.Selection.Execution.ApprovalRef
			}
			if err := config.CheckApproval(string(approvalText), task.ID, decisionID, request.Selection); err != nil {
				return err
			}
			existingApproval := false
			for _, decision := range task.Decisions {
				if decision.ID == decisionID {
					if err := config.CheckApproval(decision.Text, task.ID, decision.ID, request.Selection); err != nil {
						return err
					}
					if len(request.AdditionalTaskIDs) != 0 {
						return fmt.Errorf("taxion: approval expansion needs a fresh explicit configuration approval ID")
					}
					existingApproval = true
				}
			}
			task.Decisions = append(task.Decisions, confirmation)
			if !existingApproval {
				task.Decisions = append(task.Decisions, core.TaskDecision{
					ID: decisionID, Source: request.Source, Text: string(approvalText),
				})
			}
		} else {
			task.Decisions = append(task.Decisions, confirmation)
		}
		task.AuthorizationID = confirmation.ID
		if err := state.Graph.Track(task, request.Actor, request.Reason); err != nil {
			return err
		}
		if err := state.Graph.Transition(task.ID, core.Ready, request.Actor, request.Reason, core.TransitionProof{}); err != nil {
			return err
		}
		if task.JobID != "" {
			job, ok := state.Graph.Job(task.JobID)
			if !ok || job.Status == core.JobCancelled {
				return fmt.Errorf("taxion: approved Task references no usable Job")
			}
			if job.Status == core.JobDraft || job.Status == core.JobWaiting {
				if err := state.Graph.TransitionJob(job.ID, core.JobActive, request.Actor, "intake approved", time.Now().UTC()); err != nil {
					return err
				}
			}
		}
		if request.ReplacesFlow != "" {
			flow, _ := state.Graph.ReviewFlow(request.ReplacesFlow)
			previous, _ := state.Graph.GraphTask(flow.TaskID)
			if previous.Status != core.Superseded {
				if err := state.Graph.Transition(flow.TaskID, core.Superseded, request.Actor, request.Reason, core.TransitionProof{}); err != nil {
					return err
				}
			}
		}
		result, _ = state.Graph.GraphTask(task.ID)
		if modelFreeVerification(result) {
			return nil
		}
		return appendConfirmedIntakeApproval(state, result, request)
	})
	return result, err
}

// A bundle is a representation of the exact formal confirmation, not a second
// human decision. Legacy graphs keep their existing authorization semantics.
func appendConfirmedIntakeApproval(state *store.Workspace, task core.GraphTask, request IntakeConfirmation) error {
	if state == nil {
		return fmt.Errorf("taxion: intake approval requires a workspace")
	}
	candidate := *state
	candidate.Graph.Jobs = append([]core.Job(nil), state.Graph.Jobs...)
	if err := appendConfirmedIntakeApprovalCandidate(&candidate, task, request); err != nil {
		return err
	}
	state.Graph = candidate.Graph
	return nil
}

func appendConfirmedIntakeApprovalCandidate(state *store.Workspace, task core.GraphTask, request IntakeConfirmation) error {
	if modelFreeVerification(task) {
		return nil
	}
	if task.JobID != "" {
		// Job-native authority path: create only the immutable JobApproval.
		// The Task-level configuration decision above remains a legacy execution
		// bridge; it is not an approval source and is scheduled for removal after
		// the CLI no longer enters the Task/ReviewFlow path.
		return appendConfirmedJobApproval(state, task, request)
	}
	switch state.Graph.ApprovalRequirement() {
	case core.ApprovalVerifiedLegacy:
		return nil
	case core.ApprovalBundleRequired:
	default:
		return fmt.Errorf("taxion: cannot reuse confirmation with an ambiguous approval schema")
	}
	selection, err := json.Marshal(request.Selection)
	if err != nil {
		return err
	}
	configuration, err := core.SelectApprovalConfiguration(selection, nil)
	if err != nil {
		return err
	}
	if _, ok := state.Graph.MatchingApproval(task.ID, task, configuration); ok {
		return nil
	}
	if task.Status != core.Ready {
		for _, event := range state.Graph.History {
			if event.TaskID == task.ID && event.Task.Status == core.Ready {
				bundle, ok := state.Graph.MatchingApproval(task.ID, event.Task, configuration)
				if ok && bundle.ID == "intake-confirmation:"+task.ID {
					return nil // Replay preserves the original approval, not current execution eligibility.
				}
				break
			}
		}
	}
	if len(state.Graph.ApprovalsForTask(task.ID)) != 0 {
		return fmt.Errorf("taxion: existing approval conflicts with the confirmed contract; use explicit reauthorization")
	}
	if task.Status != core.Ready || task.AuthorizationID != "intake-confirmation:"+task.ID {
		return fmt.Errorf("taxion: confirmation reuse requires an unchanged unexecuted READY Task")
	}
	for _, flow := range state.Graph.ReviewFlows {
		if flow.TaskID == task.ID {
			return fmt.Errorf("taxion: confirmation reuse cannot authorize a Task with an existing Flow")
		}
	}
	for _, dispatch := range state.Graph.Dispatches {
		if dispatch.TaskID == task.ID {
			return fmt.Errorf("taxion: confirmation reuse cannot authorize a Task with an existing Dispatch")
		}
	}
	for _, dispatch := range state.Ledger.Dispatches {
		if dispatch.TaskID == task.ID {
			return fmt.Errorf("taxion: confirmation reuse cannot authorize a Task with a ledger Dispatch")
		}
	}
	if _, err := checkTaskConfiguration(state.Graph, task.ID, request.Selection); err != nil {
		return err
	}
	proposalHash, err := core.ImmutableProposalHash(task)
	if err != nil || proposalHash != request.ExpectedHash {
		return fmt.Errorf("taxion: confirmation reuse lacks its immutable proposal")
	}
	draftFound := false
	var approved *core.GraphEvent
	for i := range state.Graph.History {
		event := &state.Graph.History[i]
		if event.TaskID != task.ID {
			continue
		}
		if event.Task.Status == core.Draft {
			hash, err := IntakeHash(event.Task)
			draftFound = draftFound || err == nil && hash == proposalHash
		}
		if event.Task.Status == core.Ready && approved == nil {
			approved = event
		}
	}
	if !draftFound || approved == nil || !reflect.DeepEqual(approved.Task, task) ||
		approved.Actor != request.Actor || approved.Reason != request.Reason {
		return fmt.Errorf("taxion: confirmation reuse requires the original unchanged approved contract and history")
	}
	bundle, err := core.ComposeReauthorizationApprovalBundleWithProposalHash(
		"intake-confirmation:"+task.ID, proposalHash, task, configuration,
		request.Actor, request.Source, request.Reason, approved.At)
	if err != nil {
		return err
	}
	if err := bundle.Verify(task, configuration); err != nil {
		return err
	}
	return state.Graph.AppendApproval(bundle)
}
