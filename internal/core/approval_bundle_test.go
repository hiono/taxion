package core

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func approvedBundle(t *testing.T, task GraphTask) ApprovalBundle {
	t.Helper()
	bundle, err := ComposeApprovalBundle("approval-1", task, task, "human", "human:1", "contract gate", time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

func TestApprovalBundleCoversEveryStageWhenUnchanged(t *testing.T) {
	task := validDraftTask("contract")
	bundle := approvedBundle(t, task)
	if bundle.Version != ApprovalBundleVersion || bundle.ProposalHash != bundle.RecordHash {
		t.Fatalf("unexpected bundle: %+v", bundle)
	}
	if err := bundle.Verify(task); err != nil {
		t.Fatalf("unchanged contract required re-approval: %v", err)
	}
	if changed := bundle.Drift(task); len(changed) != 0 {
		t.Fatalf("unchanged contract drifted: %v", changed)
	}
	if err := bundle.CheckApprovalAdvance(task); err != nil {
		t.Fatalf("one approval did not cover every stage: %v", err)
	}
	for _, stage := range ApprovalStages() {
		if err := bundle.CheckApprovalFor(task, stage); err != nil {
			t.Fatalf("stage %s required re-approval: %v", stage, err)
		}
	}
	if err := bundle.CheckApprovalFor(task, ApprovalStage("unknown")); err == nil {
		t.Fatal("unknown stage was accepted")
	}
}

func TestApprovalBundleDriftNamesChangedDimension(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*GraphTask)
		want   string
	}{
		{"baseline", func(task *GraphTask) { task.BaselineSHA = strings.Repeat("b", 40) }, "baseline"},
		{"scope", func(task *GraphTask) { task.Scope = append(task.Scope, "extra") }, "scope"},
		{"acceptance", func(task *GraphTask) { task.Acceptance = append(task.Acceptance, "extra") }, "acceptance"},
		{"budget", func(task *GraphTask) {
			task.BudgetGuidance = &BudgetGuidancePolicy{Mode: "flow", Source: "default", AllowedModes: []string{"off", "flow"}}
		}, "budget"},
		{"permissions", func(task *GraphTask) { task.Owner = "REVIEWER" }, "permissions"},
		{"security", func(task *GraphTask) { task.Risk = "high" }, "security"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			task := validDraftTask("contract")
			bundle := approvedBundle(t, task)
			changed := validDraftTask("contract")
			testCase.mutate(&changed)
			drift := bundle.Drift(changed)
			if !slices.Contains(drift, testCase.want) {
				t.Fatalf("drift %v does not name %s", drift, testCase.want)
			}
			if err := bundle.CheckApprovalFor(changed, StagePreflight); err == nil {
				t.Fatal("changed dimension proceeded without re-approval")
			}
		})
	}
}

func TestApprovalBundleDriftSeparatesRecordHashFromDimensions(t *testing.T) {
	task := validDraftTask("contract")
	bundle := approvedBundle(t, task)
	changed := validDraftTask("contract")
	changed.Objective = "rewritten objective that no dimension covers"
	drift := bundle.Drift(changed)
	if len(drift) != 1 || drift[0] != "recordHash" {
		t.Fatalf("prose change drifted the wrong dimensions: %v", drift)
	}

	renamed := validDraftTask("contract")
	renamed.ID = "other"
	if drift := bundle.Drift(renamed); !slices.Contains(drift, "taskId") {
		t.Fatalf("identity change was not detected: %v", drift)
	}
}

func TestApprovalBundleKeepsProposalAndRecordHashDistinct(t *testing.T) {
	proposal := validDraftTask("contract")
	stored := validDraftTask("contract")
	stored.FileAccess = DefaultFileAccessPolicy()
	stored.BudgetGuidance = &BudgetGuidancePolicy{Mode: "flow", Source: "default", AllowedModes: []string{"off", "flow"}}
	bundle, err := ComposeApprovalBundle("approval-1", proposal, stored, "human", "human:1", "contract gate", time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if bundle.ProposalHash == bundle.RecordHash {
		t.Fatal("applied defaults did not separate proposal and record hash")
	}
	if err := bundle.Verify(stored); err != nil {
		t.Fatalf("stored contract was treated as drift: %v", err)
	}
	if changed := bundle.Drift(stored); len(changed) != 0 {
		t.Fatalf("applied defaults were reported as drift: %v", changed)
	}
}

func TestApprovalBundleRejectsTamperedHash(t *testing.T) {
	task := validDraftTask("contract")
	bundle := approvedBundle(t, task)
	bundle.BundleHash = "deadbeef"
	if err := bundle.Verify(task); err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("tampered bundle was accepted: %v", err)
	}
	bundle = approvedBundle(t, task)
	bundle.BundleHash = ""
	if err := bundle.Verify(task); err == nil {
		t.Fatal("bundle without a hash was accepted")
	}
	bundle = approvedBundle(t, task)
	bundle.Dimensions.Scope = "tampered"
	if err := bundle.Verify(task); err == nil {
		t.Fatal("bundle with a tampered dimension digest was accepted")
	}
}

func TestApprovalBundleHashIgnoresUncoveredFields(t *testing.T) {
	first := validDraftTask("contract")
	first.Objective = "objective A"
	second := validDraftTask("contract")
	second.Objective = "objective B"
	one := approvedBundle(t, first)
	two := approvedBundle(t, second)
	if one.RecordHash == two.RecordHash {
		t.Fatal("record hash ignored a contract change")
	}
	if one.Dimensions != two.Dimensions {
		t.Fatalf("dimension digests changed with an uncovered field: %+v vs %+v", one.Dimensions, two.Dimensions)
	}
}
