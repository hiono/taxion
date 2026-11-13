package core

import (
	"math"
	"strconv"
)

// JobUsage is an immutable accounting observation attributed to one
// RoleExecution and its bound provider session.
type JobUsage struct {
	ID              string        `json:"id"`
	JobID           string        `json:"jobId"`
	RoleExecutionID string        `json:"roleExecutionId"`
	Usage           DispatchUsage `json:"usage"`
	RecordHash      string        `json:"recordHash"`
}

func (u JobUsage) computeRecordHash() (string, error) {
	copy := u
	copy.RecordHash = ""
	return CanonicalSHA256(copy)
}

func NewJobUsage(usage JobUsage) (JobUsage, error) {
	if usage.RecordHash != "" {
		return JobUsage{}, graphError("new Job usage must not supply a record hash")
	}
	hash, err := usage.computeRecordHash()
	if err != nil {
		return JobUsage{}, err
	}
	usage.RecordHash = hash
	return usage, nil
}

func (u JobUsage) Verify() error {
	hash, err := u.computeRecordHash()
	if err != nil || hash != u.RecordHash {
		return graphError("Job usage record hash mismatch")
	}
	return nil
}

func cloneJobUsage(usage JobUsage) JobUsage {
	if usage.Usage.IsAuto != nil {
		value := *usage.Usage.IsAuto
		usage.Usage.IsAuto = &value
	}
	if usage.Usage.InputTokens != nil {
		value := *usage.Usage.InputTokens
		usage.Usage.InputTokens = &value
	}
	if usage.Usage.OutputTokens != nil {
		value := *usage.Usage.OutputTokens
		usage.Usage.OutputTokens = &value
	}
	if usage.Usage.CacheReadTokens != nil {
		value := *usage.Usage.CacheReadTokens
		usage.Usage.CacheReadTokens = &value
	}
	if usage.Usage.CacheWriteTokens != nil {
		value := *usage.Usage.CacheWriteTokens
		usage.Usage.CacheWriteTokens = &value
	}
	if usage.Usage.TotalNanoAIU != nil {
		value := *usage.Usage.TotalNanoAIU
		usage.Usage.TotalNanoAIU = &value
	}
	if usage.Usage.AICreditsStatus != nil {
		value := *usage.Usage.AICreditsStatus
		usage.Usage.AICreditsStatus = &value
	}
	if usage.Usage.Accounting != nil {
		accounting := *usage.Usage.Accounting
		usage.Usage.Accounting = &accounting
	}
	return usage
}

func (g *TaskGraph) AppendJobUsage(usage JobUsage) error {
	if g == nil {
		return graphError("Job usage requires a Task Graph")
	}
	if err := usage.Verify(); err != nil {
		return err
	}
	next := cloneGraph(*g)
	next.JobUsage = append(next.JobUsage, cloneJobUsage(usage))
	if err := next.validateJobModel(); err != nil {
		return err
	}
	*g = next
	return nil
}

func (g TaskGraph) validateJobUsage() error {
	seen := map[string]bool{}
	accountingIDs := map[string]bool{}
	for _, item := range g.JobUsage {
		job, jobOK := g.Job(item.JobID)
		execution, executionOK := g.RoleExecution(item.RoleExecutionID)
		usage := item.Usage
		if !jobOK || job.Contract == nil || !executionOK || item.Verify() != nil ||
			!nonempty(item.ID) || seen[item.ID] || execution.JobID != job.ID ||
			execution.RuntimeSessionID == "" || usage.Accounting == nil ||
			usage.Accounting.SourceSessionID != execution.RuntimeSessionID ||
			usage.Accounting.Sequence < 1 || !nonempty(usage.Accounting.UsageID) ||
			usage.TotalNanoAIU == nil || math.IsNaN(*usage.TotalNanoAIU) ||
			math.IsInf(*usage.TotalNanoAIU, 0) || *usage.TotalNanoAIU < 0 ||
			!nonempty(usage.Model) || usage.ObservedAt.IsZero() ||
			(!execution.StartedAt.IsZero() && usage.ObservedAt.Before(execution.StartedAt)) {
			return graphError("invalid or unbound Job usage record")
		}
		seen[item.ID] = true
		accountingKey := usage.Accounting.SourceSessionID + ":" +
			strconv.FormatInt(usage.Accounting.Sequence, 10) + ":" + usage.Accounting.UsageID
		if accountingIDs[accountingKey] {
			return graphError("duplicate Job usage accounting identity")
		}
		accountingIDs[accountingKey] = true
	}
	return nil
}

func (g TaskGraph) JobUsageTotal(jobID string) float64 {
	var total float64
	for _, item := range g.JobUsage {
		if item.JobID == jobID && item.Usage.TotalNanoAIU != nil {
			total += *item.Usage.TotalNanoAIU
		}
	}
	return total
}

func (g TaskGraph) JobUsageRecords(jobID string) []JobUsage {
	result := make([]JobUsage, 0)
	for _, item := range g.JobUsage {
		if item.JobID == jobID {
			result = append(result, cloneJobUsage(item))
		}
	}
	return result
}

