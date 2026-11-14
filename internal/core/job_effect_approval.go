package core

import (
	"strconv"
	"time"
)

// JobEffectApprovalRequest persists one concrete high-risk effect request.
// It is bound to a Job revision and one RoleExecution; it grants no authority.
type JobEffectApprovalRequest struct {
	ID               string               `json:"id"`
	JobID            string               `json:"jobId"`
	ContractRevision int                  `json:"contractRevision"`
	RoleExecutionID  string               `json:"roleExecutionId"`
	Observation      JobEffectObservation `json:"observation"`
	ObservationHash  string               `json:"observationHash"`
	RequestedAt      time.Time            `json:"requestedAt"`
}

func NewJobEffectApprovalRequest(id, jobID string, revision int, executionID string, observation JobEffectObservation, at time.Time, contract JobContract) (JobEffectApprovalRequest, error) {
	hash, err := CanonicalSHA256(observation)
	if err != nil || !nonempty(id) || !nonempty(jobID) || revision < 1 ||
		!nonempty(executionID) || at.IsZero() || contract.Revision != revision ||
		contract.EvaluateJobEffect(observation) != JobEffectRequireApproval {
		return JobEffectApprovalRequest{}, graphError("Job effect approval request requires one concrete, inspectable effect under the current contract")
	}
	return JobEffectApprovalRequest{
		ID: id, JobID: jobID, ContractRevision: revision, RoleExecutionID: executionID,
		Observation: cloneJobEffectObservation(observation), ObservationHash: hash, RequestedAt: at,
	}, nil
}

func (r JobEffectApprovalRequest) Verify() error {
	hash, err := CanonicalSHA256(r.Observation)
	if err != nil || !nonempty(r.ID) || !nonempty(r.JobID) || r.ContractRevision < 1 ||
		!nonempty(r.RoleExecutionID) || r.RequestedAt.IsZero() || hash != r.ObservationHash {
		return graphError("invalid Job effect approval request")
	}
	return nil
}

// JobEffectApprovalDecision is an append-only human decision for one exact request.
type JobEffectApprovalDecision struct {
	ID          string    `json:"id"`
	RequestID   string    `json:"requestId"`
	RequestHash string    `json:"requestHash"`
	Approved    bool      `json:"approved"`
	Actor       string    `json:"actor"`
	Source      string    `json:"source"`
	Reason      string    `json:"reason"`
	At          time.Time `json:"at"`
	RecordHash  string    `json:"recordHash"`
}

func NewJobEffectApprovalDecision(id string, request JobEffectApprovalRequest, approved bool, actor, reason string, at time.Time) (JobEffectApprovalDecision, error) {
	if err := request.Verify(); err != nil || !nonempty(id) || !nonempty(actor) ||
		!nonempty(reason) || at.IsZero() || at.Before(request.RequestedAt) {
		return JobEffectApprovalDecision{}, graphError("Job effect approval decision requires a valid request and explicit human decision")
	}
	decision := JobEffectApprovalDecision{
		ID: id, RequestID: request.ID, RequestHash: request.ObservationHash,
		Approved: approved, Actor: actor, Source: "user", Reason: reason, At: at,
	}
	hash, err := decision.computeRecordHash()
	if err != nil {
		return JobEffectApprovalDecision{}, err
	}
	decision.RecordHash = hash
	return decision, nil
}

func (d JobEffectApprovalDecision) computeRecordHash() (string, error) {
	copy := d
	copy.RecordHash = ""
	return CanonicalSHA256(copy)
}

func (d JobEffectApprovalDecision) Verify() error {
	hash, err := d.computeRecordHash()
	if err != nil || !nonempty(d.ID) || !nonempty(d.RequestID) || !nonempty(d.RequestHash) ||
		!nonempty(d.Actor) || d.Source != "user" || !nonempty(d.Reason) || d.At.IsZero() || hash != d.RecordHash {
		return graphError("invalid Job effect approval decision")
	}
	return nil
}

func cloneJobEffectObservation(observation JobEffectObservation) JobEffectObservation {
	observation.Effects = append([]JobEffect(nil), observation.Effects...)
	observation.Targets = append([]JobEffectTarget(nil), observation.Targets...)
	return observation
}

func cloneJobEffectApprovalRequest(request JobEffectApprovalRequest) JobEffectApprovalRequest {
	request.Observation = cloneJobEffectObservation(request.Observation)
	return request
}

func (g TaskGraph) JobEffectApprovalRequest(id string) (JobEffectApprovalRequest, bool) {
	for _, request := range g.JobEffectApprovalRequests {
		if request.ID == id {
			return cloneJobEffectApprovalRequest(request), true
		}
	}
	return JobEffectApprovalRequest{}, false
}

func (g TaskGraph) JobEffectApprovalDecisionFor(requestID string) (JobEffectApprovalDecision, bool) {
	for _, decision := range g.JobEffectApprovalDecisions {
		if decision.RequestID == requestID {
			return decision, true
		}
	}
	return JobEffectApprovalDecision{}, false
}

// EvaluateJobEffect combines the immutable Job contract with a separately
// persisted human decision. A decision applies only to the exact execution,
// contract revision, provider observation and target hash that were presented.
func (g TaskGraph) EvaluateJobEffect(executionID string, observation JobEffectObservation) JobEffectDecision {
	execution, ok := g.RoleExecution(executionID)
	if !ok || execution.Status != RoleExecutionRunning {
		return JobEffectDeny
	}
	job, ok := g.Job(execution.JobID)
	if !ok || job.Status != JobActive || job.Contract == nil {
		return JobEffectDeny
	}
	if _, ok := g.CurrentJobApproval(job.ID); !ok {
		return JobEffectDeny
	}
	decision := job.Contract.EvaluateJobEffect(observation)
	if decision != JobEffectRequireApproval {
		return decision
	}
	hash, err := CanonicalSHA256(observation)
	if err != nil {
		return JobEffectDeny
	}
	for _, request := range g.JobEffectApprovalRequests {
		if request.JobID != job.ID || request.ContractRevision != job.Contract.Revision ||
			request.RoleExecutionID != executionID || request.ObservationHash != hash {
			continue
		}
		record, ok := g.JobEffectApprovalDecisionFor(request.ID)
		if !ok {
			return JobEffectRequireApproval
		}
		if record.Approved {
			return JobEffectAllow
		}
		return JobEffectDeny
	}
	return JobEffectRequireApproval
}

func (g *TaskGraph) AppendJobEffectApprovalRequest(request JobEffectApprovalRequest) error {
	if g == nil {
		return graphError("Job effect approval request requires a Task Graph")
	}
	next := cloneGraph(*g)
	job, ok := next.Job(request.JobID)
	execution, executionOK := next.RoleExecution(request.RoleExecutionID)
	if !ok || !executionOK || execution.JobID != request.JobID ||
		execution.Status != RoleExecutionRunning || job.Status != JobActive || job.Contract == nil ||
		job.Contract.Revision != request.ContractRevision {
		return graphError("Job effect approval request is not bound to the current active Job and RoleExecution")
	}
	if _, ok := next.CurrentJobApproval(job.ID); !ok {
		return graphError("Job effect approval request requires current human Job approval")
	}
	if err := request.Verify(); err != nil || job.Contract.EvaluateJobEffect(request.Observation) != JobEffectRequireApproval {
		return graphError("Job effect approval request does not describe an approval-required effect")
	}
	for _, existing := range next.JobEffectApprovalRequests {
		if existing.ID == request.ID || existing.JobID == request.JobID &&
			existing.ContractRevision == request.ContractRevision &&
			existing.RoleExecutionID == request.RoleExecutionID &&
			existing.ObservationHash == request.ObservationHash {
			return graphError("duplicate Job effect approval request")
		}
	}
	next.JobEffectApprovalRequests = append(next.JobEffectApprovalRequests, cloneJobEffectApprovalRequest(request))
	if err := next.validateJobModel(); err != nil {
		return err
	}
	*g = next
	return nil
}

func (g *TaskGraph) AppendJobEffectApprovalDecision(decision JobEffectApprovalDecision) error {
	if g == nil {
		return graphError("Job effect approval decision requires a Task Graph")
	}
	next := cloneGraph(*g)
	request, ok := next.JobEffectApprovalRequest(decision.RequestID)
	if !ok || decision.RequestHash != request.ObservationHash {
		return graphError("Job effect approval decision does not match a persisted request")
	}
	job, jobOK := next.Job(request.JobID)
	execution, executionOK := next.RoleExecution(request.RoleExecutionID)
	if !jobOK || !executionOK || job.Contract == nil ||
		job.Contract.Revision != request.ContractRevision ||
		job.Status != JobWaiting || execution.Status != RoleExecutionWaiting {
		return graphError("Job effect approval decision is stale or its execution is not waiting")
	}
	if err := decision.Verify(); err != nil || decision.At.Before(request.RequestedAt) {
		return graphError("invalid Job effect approval decision")
	}
	if _, exists := next.JobEffectApprovalDecisionFor(request.ID); exists {
		return graphError("Job effect approval request already has a decision")
	}
	for _, existing := range next.JobEffectApprovalDecisions {
		if existing.ID == decision.ID {
			return graphError("duplicate Job effect approval decision ID")
		}
	}
	next.JobEffectApprovalDecisions = append(next.JobEffectApprovalDecisions, decision)
	if err := next.validateJobModel(); err != nil {
		return err
	}
	*g = next
	return nil
}

func (g TaskGraph) validateJobEffectApprovals() error {
	jobs := make(map[string]Job, len(g.Jobs))
	for _, job := range g.Jobs {
		jobs[job.ID] = job
	}
	executions := make(map[string]RoleExecution, len(g.RoleExecutions))
	for _, execution := range g.RoleExecutions {
		executions[execution.ID] = execution
	}
	approvals := make(map[string]JobApproval)
	for _, approval := range g.JobApprovals {
		key := approval.Proposal.JobID + ":" + strconv.Itoa(approval.Proposal.Revision)
		approvals[key] = approval
	}
	requests := make(map[string]JobEffectApprovalRequest, len(g.JobEffectApprovalRequests))
	requestKeys := map[string]bool{}
	for _, request := range g.JobEffectApprovalRequests {
		job, jobOK := jobs[request.JobID]
		execution, executionOK := executions[request.RoleExecutionID]
		if err := request.Verify(); err != nil || !jobOK || !executionOK ||
			execution.JobID != request.JobID || job.Contract == nil || request.ContractRevision > job.Contract.Revision ||
			request.RequestedAt.Before(job.CreatedAt) {
			return graphError("invalid Job effect approval request history")
		}
		key := request.JobID + ":" + strconv.Itoa(request.ContractRevision)
		approval, approved := approvals[key]
		if !approved || request.RequestedAt.Before(approval.At) ||
			approval.Proposal.Contract.EvaluateJobEffect(request.Observation) != JobEffectRequireApproval {
			return graphError("Job effect approval request does not match an approved Job revision")
		}
		unique := request.JobID + ":" + strconv.Itoa(request.ContractRevision) + ":" + request.RoleExecutionID + ":" + request.ObservationHash
		if requests[request.ID].ID != "" || requestKeys[unique] {
			return graphError("duplicate Job effect approval request history")
		}
		requests[request.ID] = request
		requestKeys[unique] = true
	}
	decisions := map[string]bool{}
	decisionIDs := map[string]bool{}
	for _, decision := range g.JobEffectApprovalDecisions {
		request, ok := requests[decision.RequestID]
		if err := decision.Verify(); err != nil || !ok || decision.RequestHash != request.ObservationHash ||
			decision.At.Before(request.RequestedAt) || decisions[decision.RequestID] || decisionIDs[decision.ID] {
			return graphError("invalid Job effect approval decision history")
		}
		decisions[decision.RequestID] = true
		decisionIDs[decision.ID] = true
	}
	return nil
}
