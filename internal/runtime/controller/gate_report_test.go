package controller

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func gateTestTask() core.GraphTask {
	return core.GraphTask{
		ID: "gate-task", Title: "Gate", Repo: "owner/repo", Type: "IMPLEMENTATION",
		Status: core.Ready, Owner: "CODER", Reviewer: "REVIEWER",
		Objective: "ship", Scope: []string{"src"}, Acceptance: []string{"tests pass", core.ADRNotApplicableCriterion},
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

func TestContractGateAcceptsReauthorizationVersions(t *testing.T) {
	task := gateTestTask()
	task.Decisions = []core.TaskDecision{{
		ID: "intake-confirmation:" + task.ID, Source: "human",
		Text: `{"taskId":"gate-task","expectedHash":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`,
	}}
	proposal, err := core.ImmutableProposalHash(task)
	if err != nil {
		t.Fatal(err)
	}
	configuration := gateTestConfiguration(t)
	for _, version := range []int{core.ApprovalCanonicalizationVersion, core.ReauthorizationCanonicalizationVersion} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			compose := core.ComposeApprovalBundleWithProposalHash
			if version == core.ReauthorizationCanonicalizationVersion {
				compose = core.ComposeReauthorizationApprovalBundleWithProposalHash
			}
			bundle, err := compose("approval", proposal, task, configuration, "human", "human", "approve", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			for _, test := range []struct {
				name   string
				mutate func(*core.TaskGraph, *core.ApprovalConfiguration)
				want   string
				code   string
			}{
				{"matching", func(*core.TaskGraph, *core.ApprovalConfiguration) {}, GateVerdictPass, ""},
				{"configuration-drift", func(_ *core.TaskGraph, c *core.ApprovalConfiguration) {
					*c, err = core.SelectApprovalConfiguration(json.RawMessage(`{"model":"changed"}`), map[string]string{"model": "user"})
					if err != nil {
						t.Fatal(err)
					}
				}, GateVerdictBlocked, "reapproval-required"},
				{"unknown-canonicalization", func(g *core.TaskGraph, _ *core.ApprovalConfiguration) {
					g.Approvals[0].CanonicalizationVersion = 99
				}, GateVerdictFail, "invalid-authorization-schema"},
				{"unknown-bundle", func(g *core.TaskGraph, _ *core.ApprovalConfiguration) {
					g.Approvals[0].BundleVersion = 99
				}, GateVerdictFail, "invalid-authorization-schema"},
				{"tampered", func(g *core.TaskGraph, _ *core.ApprovalConfiguration) {
					g.Approvals[0].Reason = "ghp_" + strings.Repeat("c", 24)
				}, GateVerdictBlocked, "missing-approval-bundle"},
			} {
				t.Run(test.name, func(t *testing.T) {
					graph := core.TaskGraph{
						Version: 1, Tasks: []core.GraphTask{task},
						ApprovalSchemaVersion: core.CurrentApprovalSchemaVersion,
						Approvals:             []core.ApprovalBundle{bundle},
					}
					current := configuration
					test.mutate(&graph, &current)
					before, err := core.CanonicalSHA256(graph)
					if err != nil {
						t.Fatal(err)
					}
					report, err := ContractGateReport(graph, task.ID, current)
					if err != nil {
						t.Fatal(err)
					}
					if report.Verdict != test.want || report.StateChanged ||
						test.code != "" && !hasGateCondition(report, test.code) {
						t.Fatalf("unexpected gate report: %+v", report)
					}
					if test.name == "matching" && (report.NextRequiredApproval != "" ||
						report.AuthorizationImpact == core.ImpactReapproval || len(report.Drifts) != 0 ||
						report.Hashes.Bundle != bundle.BundleHash) {
						t.Fatalf("matching bundle required reapproval: %+v", report)
					}
					after, err := core.CanonicalSHA256(graph)
					if err != nil || before != after {
						t.Fatal("read-only gate changed the graph")
					}
					data, err := json.Marshal(report)
					if err != nil || strings.Contains(string(data), "ghp_"+strings.Repeat("c", 24)) {
						t.Fatal("gate report exposed a credential")
					}
				})
			}
		})
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

func TestADRGateBlocksUnboundRequirementsWithoutRewritingApproval(t *testing.T) {
	task := gateTestTask()
	task.Acceptance = []string{"tests pass"}
	graph := core.TaskGraph{Version: 1, Tasks: []core.GraphTask{task}}
	before, _ := core.CanonicalJSON(graph)
	report, err := ContractGateReport(graph, task.ID)
	if err != nil || report.Verdict != GateVerdictBlocked || !hasGateCondition(report, "adr-requirements-unbound") ||
		report.NextAuthorizedTransition != "" {
		t.Fatalf("missing ADR requirements passed: %+v %v", report, err)
	}
	after, _ := core.CanonicalJSON(graph)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("gate rewrote the old Task or approval")
	}
}

func TestADRContractGatePinsSourceAndRedactsIdentifiers(t *testing.T) {
	secret := "gho_" + strings.Repeat("Z", 40)
	spec := core.Specification{
		ID: "ADR-" + secret, Title: "Explicit denials", Source: "repository",
		Location: "docs/adr/decision.md", Version: "approved-1", RetrievedAt: time.Now().UTC(),
		Body: "Preserve immutable execution evidence.", Usage: "independent ADR review",
	}
	hash, err := core.SpecificationHash(spec)
	if err != nil {
		t.Fatal(err)
	}
	spec.Ingestion = &core.SpecificationApproval{Actor: "human", Source: "human:source", Hash: hash}
	spec.Copilot = &core.SpecificationApproval{Actor: "human", Source: "human:runtime-source", Hash: hash}
	criterion, _ := core.ADRComplianceCriterion(spec)
	task := gateTestTask()
	task.Acceptance, task.Specifications = []string{criterion}, []core.Specification{spec}
	graph := core.TaskGraph{Version: 1, Tasks: []core.GraphTask{task}}
	report, err := ContractGateReport(graph, task.ID)
	if err != nil || report.Verdict != GateVerdictPass || report.ADRCompliance.Status != "bound" {
		t.Fatalf("approved source did not bind: %+v %v", report, err)
	}
	data, _ := json.Marshal(report)
	if strings.Contains(string(data), secret) {
		t.Fatal("ADR gate disclosed a credential-like identifier")
	}
	graph.Tasks[0].Specifications[0].Body += " Unapproved replacement."
	report, err = ContractGateReport(graph, task.ID)
	if err != nil || !hasGateCondition(report, "adr-requirements-invalid") || report.Verdict != GateVerdictFail {
		t.Fatalf("ADR drift passed its old approval: %+v %v", report, err)
	}
}

func TestADRGatesRequireIndependentSameCandidateDecisions(t *testing.T) {
	for _, test := range []struct {
		verdict, status, gate string
	}{
		{"APPROVE", "pass", GateVerdictPass},
		{"REQUEST_CHANGES", "fail", GateVerdictFail},
		{"BLOCKED", "blocked", GateVerdictBlocked},
	} {
		t.Run(test.verdict, func(t *testing.T) {
			db, ledger, request := reviewFlowFixture(t, core.TaskDecision{
				ID: "synthetic-adr-not-applicable", Source: "human:synthetic",
				Text: "No ADR applies to the synthetic application; independent review must confirm the reason.",
			})
			flow, err := StartReviewFlow(db, ledger, request)
			if err != nil {
				t.Fatal(err)
			}
			state, _ := db.LoadWorkspace()
			pending, _ := ExecutionGateReport(state.Graph, flow.ID)
			if !hasGateCondition(pending, "adr-review-unavailable") {
				t.Fatal("Coder-only execution passed independent ADR review")
			}
			executeFlowDispatch(t, db, flow, "", true)
			flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
			if err != nil {
				t.Fatal(err)
			}
			executeFlowDispatch(t, db, flow, test.verdict, true)
			if test.verdict == "APPROVE" {
				flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
				if err != nil {
					t.Fatal(err)
				}
			}
			state, _ = db.LoadWorkspace()
			before, _ := core.CanonicalJSON(state.Graph)
			for _, gate := range []func(core.TaskGraph, string) (GateReport, error){ExecutionGateReport, PublicationGateReport} {
				report, err := gate(state.Graph, flow.ID)
				if err != nil || report.Verdict != test.gate || report.ADRCompliance == nil ||
					report.ADRCompliance.Status != test.status || report.StateChanged {
					t.Fatalf("ADR decision not enforced: %+v %v", report, err)
				}
			}
			drift := gateDeepCopy(t, state.Graph)
			drift.Tasks[0].TargetSHA = strings.Repeat("f", 40)
			report, _ := ExecutionGateReport(drift, flow.ID)
			if !hasGateCondition(report, "adr-review-basis-mismatch") {
				t.Fatal("ADR verdict carried over to a different candidate")
			}
			drift = gateDeepCopy(t, state.Graph)
			drift.Tasks[0].Acceptance = []string{"changed ADR scope"}
			report, _ = ExecutionGateReport(drift, flow.ID)
			if !hasGateCondition(report, "adr-review-basis-mismatch") {
				t.Fatal("ADR verdict carried over to a different approved scope")
			}
			after, _ := core.CanonicalJSON(state.Graph)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("ADR gate observation modified saved evidence")
			}
		})
	}
}

func TestADRGateViolationOutranksIncompleteDecision(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		task, _ := state.Graph.GraphTask(request.TaskID)
		task.Acceptance = []string{core.DocumentationAcceptance}
		for _, id := range []string{"ADR-one", "ADR-two"} {
			spec := core.Specification{
				ID: id, Title: id, Source: "repository", Location: "docs/adr/" + id + ".md",
				Version: "approved-1", RetrievedAt: time.Now().UTC(),
				Body: "Preserve execution evidence.", Usage: "independent ADR review",
			}
			hash, err := core.SpecificationHash(spec)
			if err != nil {
				return err
			}
			spec.Ingestion = &core.SpecificationApproval{Actor: "human", Source: "human:source", Hash: hash}
			spec.Copilot = &core.SpecificationApproval{Actor: "human", Source: "human:runtime-source", Hash: hash}
			criterion, err := core.ADRComplianceCriterion(spec)
			if err != nil {
				return err
			}
			task.Specifications = append(task.Specifications, spec)
			task.Acceptance = append(task.Acceptance, criterion)
		}
		task.Status = core.Draft
		state.Graph = core.TaskGraph{Version: 1}
		if err := state.Graph.Track(task, "human", "bind two synthetic ADR decisions"); err != nil {
			return err
		}
		return state.Graph.Transition(task.ID, core.Ready, "human", "approve both source criteria", core.TransitionProof{})
	}); err != nil {
		t.Fatal(err)
	}
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	executeFlowDispatch(t, db, flow, "", true)
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	executeFlowDispatch(t, db, flow, "REQUEST_CHANGES", true)
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	for _, order := range [][]string{{"BLOCKED", "FAIL"}, {"FAIL", "BLOCKED"}} {
		graph := gateDeepCopy(t, state.Graph)
		last := &graph.Dispatches[len(graph.Dispatches)-1]
		index := 0
		for i := range last.Result.Acceptance {
			if strings.HasPrefix(last.Result.Acceptance[i].Criterion, core.ADRCompliancePrefix) {
				last.Result.Acceptance[i].Status = order[index]
				index++
			}
		}
		report := newGateReport(GateExecution, flow.TaskID)
		checkADRReview(&report, graph, flow)
		if report.Verdict != GateVerdictFail || report.ADRCompliance.Status != "fail" ||
			!hasGateCondition(report, "adr-violation") {
			t.Fatalf("incomplete ADR hid a known violation for %v: %+v", order, report)
		}
	}
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
