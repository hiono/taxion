package controller

import (
	"slices"

	"github.com/hiono/taxion/internal/core"
)

type ADRGateCheck struct {
	Status             string   `json:"status"`
	Criteria           []string `json:"criteria,omitempty"`
	ReviewerDispatchID string   `json:"reviewerDispatchId,omitempty"`
}

func checkADRContract(report *GateReport, task core.GraphTask) []string {
	required, err := core.ADRComplianceRequirements(task.Acceptance, task.Specifications)
	visible := make([]string, len(required))
	for i, criterion := range required {
		visible[i] = core.Redact(criterion)
	}
	report.ADRCompliance = &ADRGateCheck{Status: "blocked", Criteria: visible}
	if err != nil {
		report.ADRCompliance.Status = "fail"
		report.fail("adr-requirements-invalid", err.Error())
		return nil
	}
	if len(required) == 0 {
		report.blocked("adr-requirements-unbound", "bind applicable ADR snapshots or an explicit independently reviewed non-applicability criterion in the approved Task")
		return nil
	}
	if err := core.CheckSpecificationUse(task.Specifications, "runtime"); err != nil {
		report.blocked("adr-source-unavailable", err.Error())
		return nil
	}
	report.ADRCompliance.Status = "bound"
	return required
}

func checkADRReview(report *GateReport, graph core.TaskGraph, flow core.ReviewFlow) {
	required := checkADRContract(report, flow.Contract)
	if report.ADRCompliance.Status != "bound" {
		return
	}
	report.ADRCompliance.Status = "blocked"
	last, ok := graph.GraphDispatch(flow.LastDispatchID())
	if !ok || last.Recipient != "REVIEWER" || last.TaskID == flow.TaskID ||
		last.Relationship != core.RelationshipIndependent || last.Status != core.DispatchSucceeded ||
		last.Result == nil {
		report.blocked("adr-review-unavailable", "ADR compliance requires the latest formal independent Reviewer result, never a Coder declaration")
		return
	}
	report.ADRCompliance.ReviewerDispatchID = core.Redact(last.ID)
	task, found := graph.GraphTask(flow.TaskID)
	if !found || !flowContractMatches(graph, flow, task) || last.Repository != flow.Contract.Repo || last.Result.OutputSHA != task.TargetSHA ||
		last.Result.OutputTreeSHA != task.TargetTreeSHA {
		report.ADRCompliance.Status = "fail"
		report.fail("adr-review-basis-mismatch", "ADR review does not match the current approved candidate")
		return
	}
	reviewed, err := core.ADRComplianceRequirements(last.Instruction.Acceptance, last.Instruction.Specifications)
	if err != nil || !slices.Equal(reviewed, required) {
		report.ADRCompliance.Status = "fail"
		report.fail("adr-review-basis-mismatch", "independent Reviewer did not receive the exact approved ADR requirements")
		return
	}
	if err := last.ValidateResult(last.Status, last.Result); err != nil {
		report.ADRCompliance.Status = "fail"
		report.fail("adr-review-invalid", "saved independent ADR review evidence is invalid")
		return
	}
	incomplete := false
	for _, criterion := range required {
		found := false
		for _, decision := range last.Result.Acceptance {
			if decision.Criterion != criterion {
				continue
			}
			found = true
			switch decision.Status {
			case "FAIL":
				report.ADRCompliance.Status = "fail"
				report.fail("adr-violation", "independent Reviewer recorded an ADR violation")
				return
			case "BLOCKED":
				report.blocked("adr-review-incomplete", "independent Reviewer could not verify the required ADR decision")
				incomplete = true
			}
		}
		if !found {
			report.blocked("adr-review-incomplete", "independent Reviewer omitted a required ADR decision")
			incomplete = true
		}
	}
	if !incomplete {
		report.ADRCompliance.Status = "pass"
	}
}
