package core

import (
	"encoding/hex"
	"slices"
	"time"
)

// JobOutput is an append-only reference to an immutable result produced by a
// RoleExecution. The underlying bytes remain in the repository or artifact store.
type JobOutput struct {
	ID              string    `json:"id"`
	JobID           string    `json:"jobId"`
	TaskID          string    `json:"taskId"`
	RoleExecutionID string    `json:"roleExecutionId"`
	Repository      string    `json:"repository"`
	Kind            string    `json:"kind"`
	ContentSHA256   string    `json:"contentSha256"`
	CommitSHA       string    `json:"commitSha,omitempty"`
	TreeSHA         string    `json:"treeSha,omitempty"`
	BlobSHA         string    `json:"blobSha,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	RecordHash      string    `json:"recordHash"`
}

// JobEvidence records a verification result for one Job acceptance condition.
// A producer cannot verify its own evidence.
type JobEvidence struct {
	ID                    string    `json:"id"`
	JobID                 string    `json:"jobId"`
	TaskID                string    `json:"taskId"`
	ProducedByExecutionID string    `json:"producedByExecutionId"`
	OutputID              string    `json:"outputId,omitempty"`
	Acceptance            string    `json:"acceptance"`
	Kind                  string    `json:"kind"`
	ContentSHA256         string    `json:"contentSha256"`
	Status                string    `json:"status"`
	VerifiedBy            string    `json:"verifiedBy,omitempty"`
	VerifiedByExecutionID string    `json:"verifiedByExecutionId,omitempty"`
	CreatedAt             time.Time `json:"createdAt"`
	VerifiedAt            time.Time `json:"verifiedAt,omitempty"`
	RecordHash            string    `json:"recordHash"`
}


func (o JobOutput) computeRecordHash() (string, error) {
	copy := o
	copy.RecordHash = ""
	return CanonicalSHA256(copy)
}

func NewJobOutput(output JobOutput) (JobOutput, error) {
	if output.RecordHash != "" {
		return JobOutput{}, graphError("new Job output must not supply a record hash")
	}
	hash, err := output.computeRecordHash()
	if err != nil {
		return JobOutput{}, err
	}
	output.RecordHash = hash
	return output, nil
}

func (o JobOutput) Verify() error {
	hash, err := o.computeRecordHash()
	if err != nil || hash != o.RecordHash {
		return graphError("Job output record hash mismatch")
	}
	return nil
}

func (e JobEvidence) computeRecordHash() (string, error) {
	copy := e
	copy.RecordHash = ""
	return CanonicalSHA256(copy)
}

func NewJobEvidence(evidence JobEvidence) (JobEvidence, error) {
	if evidence.RecordHash != "" {
		return JobEvidence{}, graphError("new Job evidence must not supply a record hash")
	}
	hash, err := evidence.computeRecordHash()
	if err != nil {
		return JobEvidence{}, err
	}
	evidence.RecordHash = hash
	return evidence, nil
}

func (e JobEvidence) Verify() error {
	hash, err := e.computeRecordHash()
	if err != nil || hash != e.RecordHash {
		return graphError("Job evidence record hash mismatch")
	}
	return nil
}

func validContentSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && hex.EncodeToString(decoded) == value
}

func (g *TaskGraph) AppendJobOutput(output JobOutput) error {
	if g == nil {
		return graphError("Job output requires a Task Graph")
	}
	if err := output.Verify(); err != nil {
		return err
	}
	next := cloneGraph(*g)
	for _, existing := range next.JobOutputs {
		if existing.ID == output.ID {
			return graphError("duplicate Job output ID")
		}
	}
	next.JobOutputs = append(next.JobOutputs, output)
	if err := next.validateJobModel(); err != nil {
		return err
	}
	*g = next
	return nil
}

func (g *TaskGraph) AppendJobEvidence(evidence JobEvidence) error {
	if g == nil {
		return graphError("Job evidence requires a Task Graph")
	}
	if err := evidence.Verify(); err != nil {
		return err
	}
	next := cloneGraph(*g)
	for _, existing := range next.JobEvidence {
		if existing.ID == evidence.ID {
			return graphError("duplicate Job evidence ID")
		}
	}
	next.JobEvidence = append(next.JobEvidence, evidence)
	if err := next.validateJobModel(); err != nil {
		return err
	}
	*g = next
	return nil
}

func (g TaskGraph) validateJobArtifacts() error {
	jobs := make(map[string]Job, len(g.Jobs))
	tasks := make(map[string]GraphTask, len(g.Tasks))
	executions := make(map[string]RoleExecution, len(g.RoleExecutions))
	outputs := make(map[string]JobOutput, len(g.JobOutputs))
	for _, job := range g.Jobs {
		jobs[job.ID] = job
	}
	for _, task := range g.Tasks {
		tasks[task.ID] = task
	}
	for _, execution := range g.RoleExecutions {
		executions[execution.ID] = execution
	}
	for _, output := range g.JobOutputs {
		job, jobOK := jobs[output.JobID]
		task, taskOK := tasks[output.TaskID]
		execution, executionOK := executions[output.RoleExecutionID]
		if !jobOK || job.Contract == nil || !taskOK || !executionOK ||
			output.Verify() != nil || !nonempty(output.ID) || !nonempty(output.Kind) || !validContentSHA256(output.ContentSHA256) ||
			output.CreatedAt.IsZero() || output.CreatedAt.Before(job.CreatedAt) || output.CreatedAt.Before(execution.CreatedAt) ||
			task.JobID != job.ID || task.Repo != output.Repository ||
			execution.JobID != job.ID || execution.TaskID != task.ID ||
			!TaskWithinJobScope(task, *job.Contract) {
			return graphError("invalid or out-of-scope Job output")
		}
		if !slices.ContainsFunc(job.Contract.Repositories, func(scope JobRepositoryScope) bool {
			return scope.Repository == output.Repository
		}) {
			return graphError("Job output repository is outside the approved Job scope")
		}
		if _, exists := outputs[output.ID]; exists {
			return graphError("duplicate Job output ID")
		}
		outputs[output.ID] = output
	}
	seenEvidence := map[string]bool{}
	for _, evidence := range g.JobEvidence {
		job, jobOK := jobs[evidence.JobID]
		task, taskOK := tasks[evidence.TaskID]
		producer, producerOK := executions[evidence.ProducedByExecutionID]
		if !jobOK || job.Contract == nil || !taskOK || !producerOK ||
			evidence.Verify() != nil || !nonempty(evidence.ID) || !nonempty(evidence.Acceptance) || !nonempty(evidence.Kind) ||
			!validContentSHA256(evidence.ContentSHA256) || evidence.CreatedAt.IsZero() ||
			evidence.CreatedAt.Before(job.CreatedAt) || evidence.CreatedAt.Before(producer.CreatedAt) || seenEvidence[evidence.ID] ||
			task.JobID != job.ID || producer.JobID != job.ID || producer.TaskID != task.ID ||
			!slices.Contains(job.Contract.Acceptance, evidence.Acceptance) ||
			!slices.Contains(job.Contract.RequiredEvidence, evidence.Kind) {
			return graphError("invalid or out-of-scope Job evidence")
		}
		seenEvidence[evidence.ID] = true
		if evidence.OutputID != "" {
			output, ok := outputs[evidence.OutputID]
			if !ok || output.JobID != job.ID || output.TaskID != task.ID ||
				output.ContentSHA256 != evidence.ContentSHA256 || output.CreatedAt.After(evidence.CreatedAt) {
				return graphError("Job evidence references a missing or mismatched output")
			}
		}
		switch evidence.Status {
		case "observed", "unverified":
			if evidence.VerifiedBy != "" || evidence.VerifiedByExecutionID != "" || !evidence.VerifiedAt.IsZero() {
				return graphError("unverified Job evidence cannot contain verifier metadata")
			}
		case "passed", "failed":
			if !nonempty(evidence.VerifiedBy) || evidence.VerifiedAt.IsZero() ||
				evidence.VerifiedAt.Before(evidence.CreatedAt) {
				return graphError("verified Job evidence requires verifier identity and time")
			}
			if evidence.VerifiedByExecutionID != "" {
				verifier, ok := executions[evidence.VerifiedByExecutionID]
				if !ok || verifier.JobID != job.ID || verifier.ID == producer.ID ||
					verifier.Role != evidence.VerifiedBy || verifier.Status != RoleExecutionSucceeded {
					return graphError("Job evidence must be verified by an independent completed execution")
				}
			} else if evidence.VerifiedBy != "controller" && evidence.VerifiedBy != "human" {
				return graphError("Job evidence requires an independent verifier")
			}
		default:
			return graphError("unknown Job evidence status")
		}
	}
	return nil
}

// JobCompletionReady reports whether every mandatory acceptance condition has
// passing evidence and no RoleExecution remains active or unresolved.
func (g TaskGraph) JobCompletionReady(jobID string) (bool, []string) {
	if err := g.Validate(); err != nil {
		return false, []string{err.Error()}
	}
	job, ok := g.Job(jobID)
	if !ok || job.Contract == nil || g.HasUnapprovedJob(jobID) {
		return false, []string{"current Job contract is missing or unapproved"}
	}
	missing := make([]string, 0)
	for _, acceptance := range job.Contract.Acceptance {
		passed := false
		for _, evidence := range g.JobEvidence {
			if evidence.JobID == jobID && evidence.Acceptance == acceptance && evidence.Status == "passed" &&
				evidence.VerifyEvidence(g) == nil {
				passed = true
				break
			}
		}
		if !passed {
			missing = append(missing, "acceptance: "+acceptance)
		}
	}
	for _, kind := range job.Contract.RequiredEvidence {
		passed := false
		for _, evidence := range g.JobEvidence {
			if evidence.JobID == jobID && evidence.Kind == kind && evidence.Status == "passed" &&
				evidence.VerifyEvidence(g) == nil {
				passed = true
				break
			}
		}
		if !passed {
			missing = append(missing, "evidence: "+kind)
		}
	}
	for _, execution := range g.RoleExecutionsForJob(jobID) {
		switch execution.Status {
		case RoleExecutionCreated, RoleExecutionRunning, RoleExecutionWaiting, RoleExecutionCancelRequested, RoleExecutionSuspended:
			missing = append(missing, "unresolved RoleExecution: "+execution.ID)
		}
	}
	return len(missing) == 0, missing
}

func (e JobEvidence) VerifyEvidence(g TaskGraph) error {
	if err := g.validateJobArtifacts(); err != nil {
		return err
	}
	for _, candidate := range g.JobEvidence {
		if candidate.ID == e.ID && candidate.Status == e.Status && candidate.ContentSHA256 == e.ContentSHA256 {
			return nil
		}
	}
	return graphError("Job evidence is not present in the graph")
}
