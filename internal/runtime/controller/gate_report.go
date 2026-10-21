package controller

import (
	"errors"
	"fmt"
	"time"

	"github.com/hiono/taxion/internal/core"
)

// GateReportVersion is the machine-readable gate report schema version.
const GateReportVersion = 1

const (
	GateContract    = "contract"
	GateExecution   = "execution"
	GatePublication = "publication"

	GateVerdictPass    = "pass"
	GateVerdictFail    = "fail"
	GateVerdictBlocked = "blocked"
)

// GateHashes identifies the exact saved records a report observed. Empty
// values mean the gate could not derive that record.
type GateHashes struct {
	Task     string `json:"task,omitempty"`
	Proposal string `json:"proposal,omitempty"`
	Record   string `json:"record,omitempty"`
	Bundle   string `json:"bundle,omitempty"`
	Evidence string `json:"evidence,omitempty"`
}

// GateCondition is one reason a gate did not pass. Codes are stable; messages
// are redacted human explanations.
type GateCondition struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// GateReport is the versioned machine-readable result of one gate. A report is
// an observation: building it never changes Task, Dispatch, Flow or ledger
// state, and StateChanged is always false.
type GateReport struct {
	Version                  int                  `json:"version"`
	Gate                     string               `json:"gate"`
	TaskID                   string               `json:"taskId"`
	FlowID                   string               `json:"flowId,omitempty"`
	Verdict                  string               `json:"verdict"`
	Hashes                   GateHashes           `json:"hashes"`
	BlockingConditions       []GateCondition      `json:"blockingConditions"`
	Drifts                   []core.ApprovalDrift `json:"drifts,omitempty"`
	AuthorizationImpact      string               `json:"authorizationImpact,omitempty"`
	NextRequiredApproval     string               `json:"nextRequiredApproval,omitempty"`
	NextAuthorizedTransition string               `json:"nextAuthorizedTransition,omitempty"`
	StateChanged             bool                 `json:"stateChanged"`
	ObservedAt               time.Time            `json:"observedAt"`
}

func newGateReport(gate, taskID string) GateReport {
	return GateReport{
		Version: GateReportVersion, Gate: gate, TaskID: taskID,
		Verdict: GateVerdictPass, BlockingConditions: []GateCondition{},
		StateChanged: false, ObservedAt: time.Now().UTC(),
	}
}

func (r *GateReport) block(code, message string) {
	r.BlockingConditions = append(r.BlockingConditions, GateCondition{Code: code, Message: core.Redact(message)})
}

func (r *GateReport) fail(code, message string) {
	r.block(code, message)
	r.Verdict = GateVerdictFail
}

func (r *GateReport) blocked(code, message string) {
	r.block(code, message)
	if r.Verdict != GateVerdictFail {
		r.Verdict = GateVerdictBlocked
	}
}

// ContractGateReport observes whether the saved Task may enter execution under
// the current approval classification. It never approves anything.
func ContractGateReport(graph core.TaskGraph, taskID string) (GateReport, error) {
	task, ok := graph.GraphTask(taskID)
	if !ok {
		return GateReport{}, fmt.Errorf("taxion: contract gate requires an existing Task %q", taskID)
	}
	report := newGateReport(GateContract, taskID)
	hash, err := core.CanonicalSHA256(task)
	if err != nil {
		return GateReport{}, err
	}
	report.Hashes.Task = hash
	switch task.Status {
	case core.Completed, core.Rejected, core.Cancelled:
		report.fail("task-not-executable", "Task is already closed and cannot enter execution")
		return report, nil
	case core.Draft, core.Blocked, core.HumanDecisionRequired:
		report.blocked("task-not-ready", "Task must be READY or ACTIVE before execution entry")
	}
	switch graph.ApprovalRequirement() {
	case core.ApprovalVerifiedLegacy:
		if report.Verdict == GateVerdictPass {
			report.NextAuthorizedTransition = "execute within the verified legacy contract"
		}
		return report, nil
	case core.ApprovalInvalidOrAmbiguous:
		report.fail("invalid-authorization-schema", "saved approval schema is inconsistent or newer than this binary")
		return report, nil
	}
	bundle, ok := graph.NewestApproval(taskID)
	if !ok {
		report.blocked("missing-approval-bundle", "Task requires an approval bundle")
		report.NextRequiredApproval = "record a matching approval bundle for this Task"
		return report, nil
	}
	report.Hashes.Proposal = bundle.ProposalHash
	report.Hashes.Record = bundle.RecordHash
	report.Hashes.Bundle = bundle.BundleHash
	drifts, err := bundle.Drifts(task, bundle.Configuration)
	if err != nil {
		return GateReport{}, err
	}
	if len(drifts) != 0 {
		report.Drifts = drifts
		invalid := false
		for _, drift := range drifts {
			if drift.AuthorizationImpact == core.ImpactInvalid {
				invalid = true
			}
		}
		impact := core.ImpactReapproval
		if invalid {
			impact = core.ImpactInvalid
		}
		report.AuthorizationImpact = impact
		if invalid {
			report.fail("approval-invalid", "saved approval no longer matches the Task")
		} else {
			report.blocked("reapproval-required", "saved approval does not cover the current Task basis")
			report.NextRequiredApproval = "record a new approval bundle for the current proposal hash"
		}
		return report, nil
	}
	if err := bundle.Verify(task, bundle.Configuration); err != nil {
		report.fail("approval-invalid", "saved approval bundle does not verify")
		return report, nil
	}
	if report.Verdict == GateVerdictPass {
		report.NextAuthorizedTransition = "start or advance the review flow"
	}
	return report, nil
}

// ExecutionGateReport observes whether a saved flow may hand its result to the
// next role or to publication. It reads only saved evidence.
func ExecutionGateReport(graph core.TaskGraph, flowID string) (GateReport, error) {
	flow, ok := graph.ReviewFlow(flowID)
	if !ok {
		return GateReport{}, fmt.Errorf("taxion: execution gate requires an existing review flow %q", flowID)
	}
	report := newGateReport(GateExecution, flow.TaskID)
	report.FlowID = flow.ID
	if flow.Status == "STOPPED" {
		report.fail("flow-stopped", "flow is stopped; saved findings require a decision")
		return report, nil
	}
	evaluation, err := EvaluateReviewFlow(graph, flow.ID)
	if err != nil {
		report.fail("evidence-unavailable", "saved flow evidence cannot be evaluated")
		return report, nil
	}
	evidence, err := core.CanonicalSHA256(evaluation)
	if err != nil {
		return GateReport{}, err
	}
	report.Hashes.Evidence = evidence
	switch flow.Status {
	case "COMPLETED":
		if evaluation.QualityGate != "RECORDED_PASS" {
			report.fail("quality-gate-unmet", "completed flow lacks same-target acceptance evidence")
		}
	case "RUNNING":
		if flow.HandoffHeld() {
			report.blocked("handoff-held", "review flow holds the handoff before the next role")
			report.NextRequiredApproval = "release the saved handoff for one explicit role turn"
		}
		if evaluation.QualityGate != "RECORDED_PASS" {
			report.blocked("quality-gate-unmet", "saved flow has not recorded same-target acceptance evidence")
		}
	default:
		report.fail("flow-unknown-state", "flow status is not recognized")
	}
	if report.Verdict == GateVerdictPass {
		report.NextAuthorizedTransition = "publication gate"
	}
	return report, nil
}

// PublicationGateReport observes whether a completed, approved flow may create
// an external publication request. Approval and delivery stay separate.
func PublicationGateReport(graph core.TaskGraph, flowID string) (GateReport, error) {
	execution, err := ExecutionGateReport(graph, flowID)
	if err != nil {
		return GateReport{}, err
	}
	report := newGateReport(GatePublication, execution.TaskID)
	report.FlowID = execution.FlowID
	report.Hashes = execution.Hashes
	report.BlockingConditions = append(report.BlockingConditions, execution.BlockingConditions...)
	report.Verdict = execution.Verdict
	if execution.Verdict != GateVerdictPass {
		report.NextRequiredApproval = execution.NextRequiredApproval
		return report, nil
	}
	task, ok := graph.GraphTask(execution.TaskID)
	if !ok {
		report.fail("unknown-task", "flow references a missing Task")
		return report, nil
	}
	if task.Status != core.Approved && task.Status != core.Completed {
		report.blocked("task-not-approved", "publication requires an approved or completed Task")
		report.NextRequiredApproval = "record the publication approval for the exact content hash"
		return report, nil
	}
	report.NextAuthorizedTransition = "approve or deliver the saved publication request"
	return report, nil
}

// GateExitCode maps a verdict to the stable CLI exit code: 0 pass, 1 fail,
// 2 blocked. Unknown verdicts fail closed.
func GateExitCode(verdict string) int {
	switch verdict {
	case GateVerdictPass:
		return 0
	case GateVerdictBlocked:
		return 2
	default:
		return 1
	}
}

// ErrGateNotPassed marks a report whose verdict is not pass.
var ErrGateNotPassed = errors.New("taxion: gate did not pass")
