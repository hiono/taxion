package controller

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
)

func gateTestTask() core.GraphTask {
	return core.GraphTask{
		ID: "gate-task", Title: "Gate", Repo: "owner/repo", Type: "IMPLEMENTATION",
		Status: core.Ready, Owner: "CODER", Reviewer: "REVIEWER",
		Objective: "ship", Scope: []string{"src"}, Acceptance: []string{"tests pass"},
		RequiredEvidence: []string{"test-report"}, ExitCondition: "tests pass",
		Branch: "work", BaselineSHA: strings.Repeat("a", 40), Worktree: "/repo/.taxion/worktrees/gate-task",
	}
}

func gateTestConfiguration(t *testing.T) core.ApprovalConfiguration {
	t.Helper()
	configuration, err := core.SelectApprovalConfiguration(
		json.RawMessage(`{"model":"fixed"}`), map[string]string{"model": "user"})
	if err != nil {
		t.Fatal(err)
	}
	return configuration
}

func TestContractGateClassifiesApprovalBasis(t *testing.T) {
	task := gateTestTask()
	graph := core.TaskGraph{Version: 1, Tasks: []core.GraphTask{task}}

	report, err := ContractGateReport(graph, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict != GateVerdictPass || report.Hashes.Task == "" || report.StateChanged {
		t.Fatalf("verified legacy Task did not pass the contract gate: %+v", report)
	}

	graph.ApprovalSchemaVersion = core.CurrentApprovalSchemaVersion
	report, err = ContractGateReport(graph, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict != GateVerdictBlocked || GateExitCode(report.Verdict) != 2 {
		t.Fatalf("missing approval bundle was not blocked: %+v", report)
	}
	if !hasGateCondition(report, "missing-approval-bundle") || report.NextRequiredApproval == "" {
		t.Fatalf("missing bundle report lacks a next approval: %+v", report)
	}

	configuration := gateTestConfiguration(t)
	bundle, err := core.ComposeApprovalBundle("approval-1", task, task, configuration,
		"human:1", "issue:#112", "approve", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	graph.Approvals = []core.ApprovalBundle{bundle}
	report, err = ContractGateReport(graph, task.ID, configuration)
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict != GateVerdictPass || report.Hashes.Bundle != bundle.BundleHash ||
		report.Hashes.Proposal != bundle.ProposalHash || report.Hashes.Record != bundle.RecordHash {
		t.Fatalf("matching bundle did not pass: %+v", report)
	}

	drifted := graph
	drifted.Tasks = []core.GraphTask{task}
	drifted.Tasks[0].BaselineSHA = strings.Repeat("b", 40)
	report, err = ContractGateReport(drifted, task.ID, configuration)
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict != GateVerdictBlocked || len(report.Drifts) == 0 ||
		report.AuthorizationImpact == "" || GateExitCode(report.Verdict) != 2 {
		t.Fatalf("baseline drift did not require reapproval: %+v", report)
	}

	invalid := graph
	invalid.Approvals = []core.ApprovalBundle{bundle}
	invalid.Approvals[0].BundleVersion = core.ApprovalBundleVersion + 1
	report, err = ContractGateReport(invalid, task.ID, configuration)
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict != GateVerdictFail || GateExitCode(report.Verdict) != 1 {
		t.Fatalf("incompatible bundle version did not fail closed: %+v", report)
	}
}

func TestContractGateFailsClosedWithoutCurrentConfiguration(t *testing.T) {
	task := gateTestTask()
	configuration := gateTestConfiguration(t)
	bundle, err := core.ComposeApprovalBundle("approval-1", task, task, configuration,
		"human:1", "issue:#112", "approve", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	graph := core.TaskGraph{
		Version: 1, Tasks: []core.GraphTask{task},
		ApprovalSchemaVersion: core.CurrentApprovalSchemaVersion,
		Approvals:             []core.ApprovalBundle{bundle},
	}
	report, err := ContractGateReport(graph, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict != GateVerdictBlocked {
		t.Fatalf("bundle without current configuration must not pass: %+v", report)
	}
}

func TestContractGateStatusAllowlist(t *testing.T) {
	for _, test := range []struct {
		status core.TaskStatus
		want   string
	}{
		{status: core.Ready, want: GateVerdictPass},
		{status: core.Active, want: GateVerdictBlocked},
		{status: core.Approved, want: GateVerdictBlocked},
		{status: core.Superseded, want: GateVerdictFail},
		{status: core.Cancelled, want: GateVerdictFail},
	} {
		task := gateTestTask()
		task.Status = test.status
		report, err := ContractGateReport(core.TaskGraph{Version: 1, Tasks: []core.GraphTask{task}}, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if report.Verdict != test.want {
			t.Fatalf("status %s: verdict %s, want %s", test.status, report.Verdict, test.want)
		}
	}
}

func TestGateReportJSONAndRedaction(t *testing.T) {
	task := gateTestTask()
	task.Status = core.Blocked
	report, err := ContractGateReport(core.TaskGraph{Version: 1, Tasks: []core.GraphTask{task}}, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, key := range []string{`"version":1`, `"gate":"contract"`, `"verdict":"blocked"`, `"stateChanged":false`, `"blockingConditions":[{`} {
		if !strings.Contains(text, key) {
			t.Fatalf("gate report JSON lacks %s: %s", key, text)
		}
	}
	redacted := newGateReport(GateContract, task.ID)
	redacted.block("test", "token "+"ghp_"+strings.Repeat("c", 24))
	encoded, err = json.Marshal(redacted)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "ghp_"+strings.Repeat("c", 24)) {
		t.Fatalf("gate report leaked a credential: %s", encoded)
	}
}

func hasGateCondition(report GateReport, code string) bool {
	for _, condition := range report.BlockingConditions {
		if condition.Code == code {
			return true
		}
	}
	return false
}

func TestExecutionAndPublicationGatesReadSavedEvidence(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	before, err := core.CanonicalJSON(state.Graph)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := ExecutionGateReport(state.Graph, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Verdict != GateVerdictBlocked || !hasGateCondition(execution, "quality-gate-unmet") ||
		execution.Hashes.Evidence == "" || execution.StateChanged {
		t.Fatalf("pending flow did not block execution: %+v", execution)
	}
	publication, err := PublicationGateReport(state.Graph, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if publication.Gate != GatePublication || publication.Verdict != GateVerdictBlocked {
		t.Fatalf("pending flow did not block publication: %+v", publication)
	}
	after, err := core.CanonicalJSON(state.Graph)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("gate reports mutated the saved Task Graph")
	}
}

func TestExecutionGateFailsForStoppedAndHeldFlows(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	stopped := gateDeepCopy(t, state.Graph)
	stopped.ReviewFlows[0].Status = "STOPPED"
	report, err := ExecutionGateReport(stopped, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict != GateVerdictFail || !hasGateCondition(report, "flow-stopped") {
		t.Fatalf("stopped flow did not fail the execution gate: %+v", report)
	}

	held := gateDeepCopy(t, state.Graph)
	held.ReviewFlows[0].HandoffChanges = []core.HandoffChange{{
		ID: "hold-1", Action: "HOLD", Actor: "human:1", Source: "console", Reason: "review first", At: time.Now(),
	}}
	report, err = ExecutionGateReport(held, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict != GateVerdictBlocked || !hasGateCondition(report, "handoff-held") {
		t.Fatalf("held handoff did not block the execution gate: %+v", report)
	}
}

func gateDeepCopy(t *testing.T, graph core.TaskGraph) core.TaskGraph {
	t.Helper()
	encoded, err := core.CanonicalJSON(graph)
	if err != nil {
		t.Fatal(err)
	}
	var copy core.TaskGraph
	if err := core.DecodeGraphInput(strings.NewReader(string(encoded)), &copy); err != nil {
		t.Fatal(err)
	}
	return copy
}

func TestGateExitCodesAreStable(t *testing.T) {
	for verdict, code := range map[string]int{
		GateVerdictPass: 0, GateVerdictFail: 1, GateVerdictBlocked: 2, "unexpected": 1,
	} {
		if got := GateExitCode(verdict); got != code {
			t.Fatalf("verdict %q mapped to %d, want %d", verdict, got, code)
		}
	}
}

func TestContractGateRejectsUnknownTask(t *testing.T) {
	if _, err := ContractGateReport(core.TaskGraph{}, "missing"); err == nil {
		t.Fatal("contract gate accepted an unknown Task")
	}
}
