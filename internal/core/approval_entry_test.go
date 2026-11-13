package core

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func readyContract(t *testing.T) (TaskGraph, GraphTask, ApprovalBundle, ApprovalConfiguration) {
	t.Helper()
	task := validDraftTask("contract")
	task.Status = Ready
	configuration := approvalConfiguration(t, explicitSelection)
	bundle, err := ComposeApprovalBundle("approval-1", task, task, configuration, "human", "human:1", "contract gate", time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	graph := TaskGraph{Version: 1, Tasks: []GraphTask{task}, Approvals: []ApprovalBundle{bundle}}
	return graph, task, bundle, configuration
}

func TestCheckBundleAtEntryAuthorizesEveryStage(t *testing.T) {
	graph, task, bundle, configuration := readyContract(t)
	for _, stage := range ApprovalEntryStages() {
		approved, err := graph.CheckBundleAtEntry(task.ID, stage, configuration)
		if err != nil {
			t.Fatalf("%s: %v", stage, err)
		}
		if approved.BundleHash != bundle.BundleHash || approved.ID != bundle.ID {
			t.Fatalf("%s: wrong bundle %+v", stage, approved)
		}
	}
}

func TestCheckBundleAtEntryDriftsProbablyEveryDimension(t *testing.T) {
	changed := []struct {
		dimension string
		mutate    func(*GraphTask)
	}{
		{"baseline", func(task *GraphTask) { task.BaselineSHA = strings.Repeat("b", 40) }},
		{"scope", func(task *GraphTask) { task.Scope = append(task.Scope, "web") }},
		{"acceptance", func(task *GraphTask) { task.Acceptance = append(task.Acceptance, "benchmark") }},
		{"budget", func(task *GraphTask) {
			task.BudgetGuidance = &BudgetGuidancePolicy{Mode: "flow", Source: "default", AllowedModes: []string{"off", "flow"}}
		}},
		{"permissions", func(task *GraphTask) { task.Owner = "REVIEWER" }},
		{"security", func(task *GraphTask) { task.Risk = "high" }},
	}
	for _, test := range changed {
		graph, task, _, configuration := readyContract(t)
		test.mutate(&task)
		graph.Tasks[0] = task
		_, err := graph.CheckBundleAtEntry(task.ID, EntryCoderDispatch, configuration)
		var failure *EntryCheckError
		if !errors.As(err, &failure) {
			t.Fatalf("%s: expected entry failure, got %v", test.dimension, err)
		}
		found := false
		for _, drift := range failure.Drifts {
			if drift.Dimension == test.dimension && drift.AuthorizationImpact == ImpactReapproval {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: drift missing: %+v", test.dimension, failure.Drifts)
		}
	}
}

func TestCheckBundleAtEntryReportsConfigurationDrift(t *testing.T) {
	graph, task, _, configuration := readyContract(t)
	drifted := approvalConfiguration(t, strings.Replace(explicitSelection, "https://gitlab.example.com\",\"webBase", "https://api2.example.com\",\"webBase", 1))
	_, err := graph.CheckBundleAtEntry(task.ID, EntryStartReviewFlow, drifted)
	var failure *EntryCheckError
	if !errors.As(err, &failure) || failure.Code != "no-matching-bundle" {
		t.Fatalf("configuration drift was not rejected: %v", err)
	}
	found := false
	for _, drift := range failure.Drifts {
		if drift.Dimension == "configuration" && len(drift.Fields) == 1 && strings.Contains(drift.Fields[0], "apiBase") {
			found = true
		}
	}
	if !found {
		t.Fatalf("configuration drift path missing: %+v", failure.Drifts)
	}
	if strings.Contains(err.Error(), "ghp_") || strings.Contains(err.Error(), "token") {
		t.Fatalf("entry failure leaked a credential: %q", err.Error())
	}
	_ = configuration
}

func TestCheckBundleAtEntryRejectsTamperedAndMissingBundles(t *testing.T) {
	graph, task, bundle, configuration := readyContract(t)
	bundle.BundleHash = strings.Repeat("0", 64)
	graph.Approvals[0] = bundle
	if _, err := graph.CheckBundleAtEntry(task.ID, EntryRepairDispatch, configuration); err == nil {
		t.Fatal("tampered bundle authorized an entry point")
	}
	graph.Approvals = nil
	if _, err := graph.CheckBundleAtEntry(task.ID, EntryReReviewDispatch, configuration); err == nil {
		t.Fatal("missing bundle authorized an entry point")
	}
}

func TestCheckBundleAtEntryRequiresReadyTask(t *testing.T) {
	graph, task, _, configuration := readyContract(t)
	task.Status = Draft
	graph.Tasks[0] = task
	var failure *EntryCheckError
	if _, err := graph.CheckBundleAtEntry(task.ID, EntryCoderDispatch, configuration); !errors.As(err, &failure) || failure.Code != "task-not-ready" {
		t.Fatalf("DRAFT task passed the dispatch entry check: %v", err)
	}
}
