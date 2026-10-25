package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

type TaskBudgetReport struct {
	ResumeBasis *ResumeBasis `json:"resumeBasis,omitempty"`

	TaskID                   string   `json:"taskId"`
	FlowID                   string   `json:"flowId"`
	DispatchLimit            int      `json:"dispatchLimit"`
	Dispatches               int      `json:"dispatches"`
	DispatchesRemaining      int      `json:"dispatchesRemaining"`
	CoderSessionLimit        int      `json:"coderSessionLimit"`
	CoderSessions            int      `json:"coderSessions"`
	CoderSessionsRemaining   int      `json:"coderSessionsRemaining"`
	ReviewerSessionLimit     int      `json:"reviewerSessionLimit"`
	ReviewerSessions         int      `json:"reviewerSessions"`
	ReviewerSessionsRemain   int      `json:"reviewerSessionsRemaining"`
	MaxNanoAIU               float64  `json:"maxNanoAiu"`
	ConsumedNanoAIU          float64  `json:"consumedNanoAiu"`
	NanoAIURemaining         float64  `json:"nanoAiuRemaining"`
	CreditsStatus            string   `json:"creditsStatus"`
	ElapsedLimitMilliseconds int64    `json:"elapsedLimitMilliseconds"`
	ElapsedMilliseconds      int64    `json:"elapsedMilliseconds"`
	ElapsedRemainingMillis   int64    `json:"elapsedRemainingMilliseconds"`
	RepairLimit              int      `json:"repairLimit"`
	Repairs                  int      `json:"repairs"`
	ReviewDispatches         int      `json:"reviewDispatches"`
	ReReviews                int      `json:"reReviews"`
	Unknown                  []string `json:"unknown"`
}

// ResumeBasis binds the issuance audit to the approved flow without copying
// authorization text or replacing any execution record on restart.
type ResumeBasis struct {
	Version           int             `json:"version"`
	ContractHash      string          `json:"contractHash"`
	AuthorizationHash string          `json:"authorizationHash"`
	InstructionHash   string          `json:"instructionHash"`
	DispatchID        string          `json:"dispatchId"`
	StartedAt         time.Time       `json:"startedAt"`
	Deadline          time.Time       `json:"deadline"`
	Budget            core.FlowBudget `json:"budget"`
}

type budgetStopError struct{ reason string }

func (e *budgetStopError) Error() string { return "taxion: BUDGET_STOP: " + e.reason }

func flowForDispatch(graph core.TaskGraph, dispatchID string) (core.ReviewFlow, bool) {
	for _, flow := range graph.ReviewFlows {
		for _, id := range flow.DispatchIDs {
			if id == dispatchID {
				return flow, true
			}
		}
	}
	return core.ReviewFlow{}, false
}

func EvaluateTaskBudget(graph core.TaskGraph, id string, now time.Time) (TaskBudgetReport, error) {
	flow, ok := graph.ReviewFlow(id)
	if !ok {
		return TaskBudgetReport{}, fmt.Errorf("taxion: task budget requires an existing flow")
	}
	return taskBudgetReport(graph, flow, now), nil
}

func taskBudgetReport(graph core.TaskGraph, flow core.ReviewFlow, now time.Time) TaskBudgetReport {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	report := TaskBudgetReport{
		TaskID: flow.TaskID, FlowID: flow.ID, DispatchLimit: flow.Budget.MaxDispatches,
		CoderSessionLimit:    flow.Budget.MaxCoderSessions,
		ReviewerSessionLimit: flow.Budget.MaxReviewerSessions,
		MaxNanoAIU:           flow.Budget.MaxNanoAIU, CreditsStatus: "complete",
		ElapsedLimitMilliseconds: flow.Deadline.Sub(flow.StartedAt).Milliseconds(),
		RepairLimit:              flow.MaxRepairs, Repairs: flow.Repairs, Unknown: []string{},
	}
	sessions := map[string]map[string]bool{"CODER": {}, "REVIEWER": {}}
	for _, id := range flow.DispatchIDs {
		dispatch, exists := graph.GraphDispatch(id)
		if !exists {
			report.Unknown = append(report.Unknown, "flow references a missing Dispatch")
			report.Dispatches++
			continue
		}
		report.Dispatches++
		if dispatch.Recipient == "REVIEWER" {
			report.ReviewDispatches++
		}
		if dispatch.RuntimeSessionID != "" {
			if sessions[dispatch.Recipient] == nil {
				sessions[dispatch.Recipient] = map[string]bool{}
			}
			sessions[dispatch.Recipient][dispatch.RuntimeSessionID] = true
			if len(dispatch.RuntimeUsage) == 0 && !active(dispatch.Status) {
				report.CreditsStatus = "unknown"
			}
			for _, usage := range dispatch.RuntimeUsage {
				if usage.TotalNanoAIU == nil || math.IsNaN(*usage.TotalNanoAIU) ||
					math.IsInf(*usage.TotalNanoAIU, 0) || *usage.TotalNanoAIU < 0 ||
					usage.AICreditsStatus == nil || *usage.AICreditsStatus != "complete" {
					report.CreditsStatus = "unknown"
					continue
				}
				report.ConsumedNanoAIU += *usage.TotalNanoAIU
			}
		}
	}
	report.CoderSessions = len(sessions["CODER"])
	report.ReviewerSessions = len(sessions["REVIEWER"])
	report.ReReviews = max(report.ReviewDispatches-1, 0)
	report.DispatchesRemaining = max(report.DispatchLimit-report.Dispatches, 0)
	report.CoderSessionsRemaining = max(report.CoderSessionLimit-report.CoderSessions, 0)
	report.ReviewerSessionsRemain = max(report.ReviewerSessionLimit-report.ReviewerSessions, 0)
	if report.CreditsStatus == "unknown" {
		report.NanoAIURemaining = 0
		report.Unknown = append(report.Unknown, "nano-AIU usage is incomplete; remaining budget is zero")
	} else {
		report.NanoAIURemaining = max(report.MaxNanoAIU-report.ConsumedNanoAIU, 0)
	}
	elapsed := now.Sub(flow.StartedAt).Milliseconds()
	report.ElapsedMilliseconds = max(elapsed, 0)
	report.ElapsedRemainingMillis = max(report.ElapsedLimitMilliseconds-report.ElapsedMilliseconds, 0)
	if flow.Budget.MaxDispatches == 0 || flow.Budget.MaxCoderSessions == 0 ||
		flow.Budget.MaxReviewerSessions == 0 || flow.Budget.MaxNanoAIU <= 0 {
		report.Unknown = append(report.Unknown, "approved budget limits are unavailable")
		report.DispatchesRemaining = 0
		report.CoderSessionsRemaining = 0
		report.ReviewerSessionsRemain = 0
		report.NanoAIURemaining = 0
	}
	return report
}

func flowBudgetStop(report TaskBudgetReport, role string) string {
	switch {
	case report.ElapsedRemainingMillis == 0:
		return "elapsed budget reached"
	case report.DispatchesRemaining == 0:
		return "Task Dispatch ceiling reached"
	case role == "CODER" && report.CoderSessionsRemaining == 0:
		return "Coder session ceiling reached"
	case role == "REVIEWER" && report.ReviewerSessionsRemain == 0:
		return "Reviewer session ceiling reached"
	case report.CreditsStatus != "complete":
		return "nano-AIU usage is unknown; remaining budget is zero"
	case report.NanoAIURemaining <= 0:
		return "nano-AIU ceiling reached"
	default:
		return ""
	}
}

func requireBudgetRoom(graph core.TaskGraph, flow core.ReviewFlow, role string) error {
	report := taskBudgetReport(graph, flow, time.Now().UTC())
	if reason := flowBudgetStop(report, role); reason != "" {
		return &budgetStopError{reason: reason}
	}
	if role == "REVIEWER" && report.ReReviews >= report.RepairLimit+1 {
		return &budgetStopError{reason: "re-review ceiling reached"}
	}
	return nil
}

func requireSessionBudget(graph core.TaskGraph, flow core.ReviewFlow, role string) error {
	report := taskBudgetReport(graph, flow, time.Now().UTC())
	switch {
	case report.ElapsedRemainingMillis == 0:
		return &budgetStopError{reason: "elapsed budget reached"}
	case role == "CODER" && report.CoderSessionsRemaining == 0:
		return &budgetStopError{reason: "Coder session ceiling reached"}
	case role == "REVIEWER" && report.ReviewerSessionsRemain == 0:
		return &budgetStopError{reason: "Reviewer session ceiling reached"}
	case report.CreditsStatus != "complete":
		return &budgetStopError{reason: "nano-AIU usage is unknown; remaining budget is zero"}
	case report.NanoAIURemaining <= 0:
		return &budgetStopError{reason: "nano-AIU ceiling reached"}
	default:
		return nil
	}
}

func activeBudgetStop(report TaskBudgetReport) string {
	switch {
	case report.ElapsedRemainingMillis == 0:
		return "elapsed budget reached"
	case report.CreditsStatus != "complete":
		return "nano-AIU usage is unknown; remaining budget is zero"
	case report.NanoAIURemaining <= 0:
		return "nano-AIU ceiling reached"
	default:
		return ""
	}
}

func stopFlowForBudget(db *store.Store, id, reason string) error {
	return db.UpdateWorkspace(func(state *store.Workspace) error {
		flow, ok := state.Graph.ReviewFlow(id)
		if !state.HasGraph || !ok || flow.Status != "RUNNING" {
			return fmt.Errorf("taxion: budget stop requires a running flow")
		}
		if err := holdReviewFlowForStop(&state.Graph, &flow, reason); err != nil {
			return err
		}
		flow.StopCode, flow.Status, flow.Reason = "BUDGET_STOP", "STOPPED", reason
		flow.StopCause = flowStopCause(flow)
		storeFlow(state, flow)
		return appendBudgetAudit(state, flow, "BUDGET_STOP", taskBudgetReport(state.Graph, flow, time.Now().UTC()))
	})
}

func appendBudgetAudit(state *store.Workspace, flow core.ReviewFlow, kind string, report TaskBudgetReport) error {
	contractHash, err := core.CanonicalSHA256(flow.Contract)
	if err != nil {
		return err
	}
	authorizationHash, err := core.CanonicalSHA256(flow.Authorization)
	if err != nil {
		return err
	}
	last, ok := state.Graph.GraphDispatch(flow.DispatchIDs[len(flow.DispatchIDs)-1])
	if !ok {
		return fmt.Errorf("taxion: budget audit requires its saved Dispatch")
	}
	report.ResumeBasis = &ResumeBasis{
		Version: 1, ContractHash: contractHash, AuthorizationHash: authorizationHash,
		InstructionHash: last.InstructionHash, DispatchID: last.ID,
		StartedAt: flow.StartedAt, Deadline: flow.Deadline, Budget: flow.Budget,
	}
	payload, err := json.Marshal(report)
	if err != nil {
		return err
	}
	var sequence uint64 = 1
	for _, prior := range state.Records.Budgets {
		if prior.FlowID == flow.ID {
			sequence++
		}
	}
	state.Records.Budgets = append(state.Records.Budgets, store.BudgetAudit{
		FlowID: flow.ID, TaskID: flow.TaskID, Sequence: sequence, Kind: kind,
		Payload: string(payload), At: time.Now().UTC(),
	})
	hash := sha256.Sum256(payload)
	state.Records.Budgets[len(state.Records.Budgets)-1].Hash = hex.EncodeToString(hash[:])
	return nil
}
