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
}

// VerifyRequestConfirmation reads the formal intake decision and its original
// contract. A checkpoint's approval flag is never an authorization source.
func VerifyRequestConfirmation(graph core.TaskGraph, taskID, operationID, proposalHash, authorizationID, basisHash string, selected config.Config) error {
	task, ok := graph.GraphTask(taskID)
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

func originalIntakeRequest(task core.GraphTask, fallback string) string {
	for _, decision := range task.Decisions {
		if decision.ID != "intake:"+task.ID {
			continue
		}
		var record struct {
			Request string `json:"request"`
		}
		if json.Unmarshal([]byte(decision.Text), &record) == nil && strings.TrimSpace(record.Request) != "" {
			return record.Request
		}
	}
	if strings.TrimSpace(fallback) != "" {
		return fallback
	}
	return "Original request unavailable for legacy Task " + task.ID
}

// ensureIntakeJob assigns the Task's canonical JobID before the Task is tracked.
// Job ownership is stored on Task; Job never maintains a reverse Task ID list.
func ensureIntakeJob(graph *core.TaskGraph, task *core.GraphTask, request string) error {
	if graph == nil || task == nil {
		return fmt.Errorf("taxion: Job assignment requires a graph and Task")
	}
	if task.JobID != "" {
		if _, ok := graph.Job(task.JobID); !ok {
			return fmt.Errorf("taxion: Task references an unknown Job")
		}
		return nil
	}
	anchorID := task.Supersedes
	if anchorID == "" {
		anchorID = task.ParentID
	}
	seen := map[string]bool{}
	for anchorID != "" {
		if seen[anchorID] {
			return fmt.Errorf("taxion: Task ancestry contains a cycle while assigning Job")
		}
		seen[anchorID] = true
		parent, exists := graph.GraphTask(anchorID)
		if !exists {
			return fmt.Errorf("taxion: predecessor Task %s is unavailable for Job assignment", anchorID)
		}
		if parent.JobID != "" {
			job, ok := graph.Job(parent.JobID)
			if !ok || job.Status == core.JobCancelled {
				return fmt.Errorf("taxion: predecessor Task has no usable Job")
			}
			task.JobID = job.ID
			if job.Status == core.JobCompleted || job.Status == core.JobFailed {
				return graph.TransitionJob(job.ID, core.JobWaiting, "controller", "successor Task proposed", time.Now().UTC())
			}
			return nil
		}
		anchorID = parent.ParentID
	}
	jobID, err := newIntakeJobID()
	if err != nil {
		return err
	}
	goal := task.Objective
	if strings.TrimSpace(goal) == "" {
		goal = task.Title
	}
	status := core.JobDraft
	if task.ParentID != "" || task.Supersedes != "" {
		status = core.JobActive
	}
	job := core.Job{ID: jobID, Request: request, Goal: goal, Status: status}
	if err := graph.CreateJob(job); err != nil {
		return err
	}
	task.JobID = jobID
	return nil
}

func ProposeIntake(db *store.Store, request IntakeRequest) (core.GraphTask, error) {
	if db == nil || strings.TrimSpace(request.Request) == "" || strings.TrimSpace(request.Actor) == "" ||
		strings.TrimSpace(request.Source) == "" || request.Task.Status != core.Draft ||
		request.Task.AuthorizationID != "" || len(request.Task.Decisions) != 0 ||
		(request.Task.Owner != "CODER" && !(modelFreeVerification(request.Task) ||
			request.Task.Owner == "USER" && (request.ReconciliationHash != "" ||
				request.ReviewInput != nil && request.Task.Type == "VERIFICATION" && !request.Task.ModelFree))) ||
		request.Task.Reviewer != "REVIEWER" {
		return core.GraphTask{}, fmt.Errorf("taxion: intake requires an original request, sourced DRAFT contract and independent Coder/Reviewer without authorization")
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
			// JobID is assigned by the first proposal. Reconstruct it before
			// comparing an exact replay of the caller's original Task input.
			task.JobID = previous.JobID
			if !reflect.DeepEqual(previous, task) {
				return fmt.Errorf("taxion: intake Task ID already contains another proposal; use a new Task ID")
			}
			if previous.JobID == "" {
				return fmt.Errorf("taxion: existing Task requires explicit Job migration before reuse")
			}
			result = previous
			return nil
		}
		for _, previous := range state.Ledger.Tasks {
			if previous.ID == task.ID {
				return fmt.Errorf("taxion: intake Task ID already exists in the ledger; use a new Task ID")
			}
		}
		if err := ensureIntakeJob(&state.Graph, &task, request.Request); err != nil {
			return err
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
	if len(task.ValidationCommands) > 0 {
		operations = append(operations, "test.run")
	}
	contract := core.JobContract{
		Revision: 1,
		Roles: []string{"PLANNER", "CODER", "REVIEWER"},
		Acceptance: slices.Clone(task.Acceptance),
		RequiredEvidence: slices.Clone(task.RequiredEvidence),
		Repositories: []core.JobRepositoryScope{{
			Repository: task.Repo,
			FileAccess: *task.FileAccess,
		}},
		Operations: operations,
		Budget: budget,
		Deadline: deadline,
		SecurityRestrictions: []string{"no credential export", "no network access"},
		Configuration: configuration,
	}
	if err := contract.Validate(); err != nil {
		return core.JobContract{}, err
	}
	return contract, nil
}

func defaultJobContractForIntake(task core.GraphTask, selection config.Config) (*core.JobContract, error) {
	encoded, err := json.Marshal(selection)
	if err != nil {
		return nil, err
	}
	configuration, err := core.SelectApprovalConfiguration(encoded, nil)
	if err != nil {
		return nil, err
	}
	contract, err := BuildJobContractForIntake(task, configuration,
		core.JobBudget{MaxRoleExecutions: 8, MaxConcurrentExecutions: 2, MaxNanoAIU: 1000},
		time.Now().UTC().Add(24*time.Hour))
	if err != nil {
		return nil, err
	}
	return &contract, nil
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
	if !core.TaskWithinJobScope(task, contract) ||
		!slices.Contains(contract.Operations, "repository.read") ||
		!slices.Contains(contract.Operations, "repository.write") {
		return fmt.Errorf("taxion: Task repository or file access exceeds the approved Job scope")
	}
	for _, acceptance := range task.Acceptance {
		if !slices.Contains(contract.Acceptance, acceptance) {
			return fmt.Errorf("taxion: Task acceptance criterion is outside the approved Job contract")
		}
	}
	for _, evidence := range task.RequiredEvidence {
		if !slices.Contains(contract.RequiredEvidence, evidence) {
			return fmt.Errorf("taxion: Task evidence requirement is outside the approved Job contract")
		}
	}
	if len(task.ValidationCommands) > 0 &&
		(!slices.Contains(contract.Operations, "process.exec") || !slices.Contains(contract.Operations, "test.run")) {
		return fmt.Errorf("taxion: Task validation commands are outside the approved Job operation scope")
	}
	return nil
}

func appendConfirmedJobApproval(state *store.Workspace, task core.GraphTask, request IntakeConfirmation) error {
	if modelFreeVerification(task) {
		return nil
	}
	job, ok := state.Graph.JobForTask(task.ID)
	if !ok || request.JobContract == nil {
		return fmt.Errorf("taxion: non-model-free intake requires an explicit Job contract")
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
	} else if !reflect.DeepEqual(*job.Contract, contract) {
		if contract.Revision != job.Contract.Revision+1 {
			return fmt.Errorf("taxion: changed Job contract requires the next revision")
		}
	} else if contract.Revision != job.Contract.Revision {
		return fmt.Errorf("taxion: unchanged Job contract must retain its revision")
	}
	job.Contract = &contract
	job.UpdatedAt = time.Now().UTC()
	for i := range state.Graph.Jobs {
		if state.Graph.Jobs[i].ID == job.ID {
			state.Graph.Jobs[i] = job
			break
		}
	}
	proposal, err := core.JobProposal(job)
	if err != nil {
		return err
	}
	proposalHash, err := proposal.Hash()
	if err != nil {
		return err
	}
	if existing, ok := state.Graph.CurrentJobApproval(job.ID); ok && existing.ProposalHash == proposalHash {
		return nil
	}
	for _, existing := range state.Graph.JobApprovals {
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
	return state.Graph.AppendJobApproval(approval)
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
		if request.JobContract != nil {
			return core.GraphTask{}, fmt.Errorf("taxion: model-free verification cannot include a Job execution contract")
		}
	} else if request.JobContract == nil {
		job, ok := initialState.Graph.JobForTask(taskForContract.ID)
		if !ok {
			return core.GraphTask{}, fmt.Errorf("taxion: intake Task has no owning Job")
		}
		if job.Contract != nil {
			request.JobContract, err = cloneIntakeJobContract(job.Contract)
		} else {
			request.JobContract, err = defaultJobContractForIntake(taskForContract, request.Selection)
		}
		if err != nil {
			return core.GraphTask{}, err
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
		job, ok := state.Graph.JobForTask(task.ID)
		if !ok {
			return fmt.Errorf("taxion: approved Task is not associated with a Job")
		}
		if job.Status == core.JobDraft || job.Status == core.JobWaiting {
			if err := state.Graph.TransitionJob(job.ID, core.JobActive, request.Actor, "intake approved", time.Now().UTC()); err != nil {
				return err
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
	if modelFreeVerification(task) {
		return nil
	}
	if err := appendConfirmedJobApproval(state, task, request); err != nil {
		return err
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
