package main

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime/controller"
)

func TestJSONOperationHelpExplainsInputsWithoutWorkspace(t *testing.T) {
	previous := storePath
	directory := filepath.Join(t.TempDir(), "must-not-be-created")
	storePath = filepath.Join(directory, "ledger.json")
	t.Cleanup(func() { storePath = previous })
	for _, test := range []struct {
		command string
		fields  []string
	}{
		{"conversation intake", []string{"JSON envelope", "DRAFT", "supersedes", "targetTreeSha", "taxion ready", "contextSources", "taxion_read_context",
			"A receipt hash is NOT diagnostic content", "task.objective", "task graph BEFORE"}},
		{"conversation confirm", []string{"expectedHash", "selection", "configurationDecisionId", "additionalTaskIds", "replacesFlow",
			"workflow feedback-target", "updatedAt", "resultHash", "ApprovalBundle", "No combined DRAFT",
			"target to be OMITTED", "artifact-predecessor", "Both worktrees", "receipt"}},
		{"workflow plan", []string{"JSON input", "READY", "stageBudget", "stagePlanHash", "provider-stop",
			"continuationChange", "savedOutputDecision", "SUSPENDED", "workflow recover-review", "one preview/approval"}},
		{"workflow recover-review", []string{"expectedHash", "continueStoppedExecution", "runtime-stop receipt", "original Flow",
			"saved readable output", "atomically", "No model", "deadlineRecontract", "previousDeadline", "Preview first"}},
		{"workflow budget", []string{"approvedCeiling", "preservation", "cumulative", "executionAuthorized:false", "priorFlows"}},
		{"workflow terminate", []string{"Complete JSON", "flowId", "dispatchId", "RUNNING", "CODER", "BLOCKED", "reconcile-stopped"}},
		{"workflow reconcile-stopped", []string{"Complete reconciliation JSON", "noActiveExecutionConfirmed", "acceptDeliveryUncertainty",
			"accepted-coder-output", "expectedHash", "continueFromDispatchId", "deadline", "reportedChangedFiles", "CANCELLED", "WAITING_FOR_REVIEW"}},
		{"workflow extend", []string{"previousDeadline", "previousMaxNanoAiu", "expectedHash", "SAME session", "resource_approval_required"}},
		{"workflow start", []string{"JSON input", "stageBudget", "stagePlanHash", "actual user approval", "not a model turn"}},
		{"workflow validate-input", []string{"no model", "artifactSource", "unknown", "doctor --dispatch"}},
		{"workflow run", []string{"progress-monitor", "ten minutes", "no extra model"}},
		{"workflow recover", []string{"expectedRunnerId", "previousProcessStopped", "finalizeSavedResult",
			"validationDeadline", "host-validation", "REQUEST_CHANGES", "-cli", "-base-directory"}},
	} {
		t.Run(test.command, func(t *testing.T) {
			parts := strings.Fields(test.command)
			var output bytes.Buffer
			var err error
			if parts[0] == "conversation" {
				err = conversationCmd([]string{parts[1], "--help"}, &output)
			} else {
				err = workflowCommand(t.Context(), []string{parts[1], "--help"}, &output)
			}
			if !errors.Is(err, flag.ErrHelp) {
				t.Fatal("help tried to execute an operation", err)
			}
			for _, field := range test.fields {
				if !strings.Contains(output.String(), field) {
					t.Errorf("help omits %q", field)
				}
			}
		})
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("help accessed or created a workspace", err)
	}
}

func TestOperationHelpExamplesMatchRequestTypes(t *testing.T) {
	for _, test := range []struct {
		text  string
		value any
	}{
		{intakeConfirmationHelp, &controller.IntakeConfirmation{}},
		{stagePlanInputHelp, &controller.ReviewFlowRequest{}},
		{normalReviewerRecoveryHelp, &controller.DispatchReconciliationRequest{}},
	} {
		_, example, ok := strings.Cut(test.text, "\n{")
		if !ok {
			t.Fatal("help omits a JSON example")
		}
		_, planExample, found := strings.Cut(normalReviewerRecoveryHelp, "Complete plan/start JSON for the confirmed successor:\n")
		if !found {
			t.Fatal("normal Reviewer recovery omitted successor JSON")
		}
		planExample, _, _ = strings.Cut(planExample, "\nThe example date")
		if err := core.DecodeGraphInput(strings.NewReader(planExample), &controller.ReviewFlowRequest{}); err != nil {
			t.Fatal("normal Reviewer successor example differs from request schema", err)
		}
		_, confirmExample, found := strings.Cut(normalReviewerRecoveryHelp, "values; include the approved billing/budgetGuidance/fileAccess when present):\n")
		if !found {
			t.Fatal("normal Reviewer recovery omitted confirmation JSON")
		}
		confirmExample, _, _ = strings.Cut(confirmExample, "\nNo placeholder")
		if err := core.DecodeGraphInput(strings.NewReader(confirmExample), &controller.IntakeConfirmation{}); err != nil {
			t.Fatal("normal Reviewer confirmation example differs from request schema", err)
		}
		example, _, ok = strings.Cut(example, "\n}\n")
		if !ok {
			t.Fatal("help has an unterminated JSON example")
		}
		if err := core.DecodeGraphInput(strings.NewReader("{"+example+"\n}"), test.value); err != nil {
			t.Fatal("help example differs from its actual request schema", err)
		}
	}
	for _, text := range []string{readyPrompt, readySkillBody} {
		if !strings.Contains(text, "do not browse Taxion implementation sources with gh api") ||
			!strings.Contains(text, "missing operation contract") {
			t.Fatal("operator guidance routes missing help into source investigation")
		}
		for _, required := range []string{"receiptHash", "replacesFlow=FAILED_FLOW_ID", "OMITS target",
			"--controller-validation-submission", "continuationChange", "savedOutputDecision",
			"A receipt hash is NOT diagnostic content", "task.objective", "task graph BEFORE",
			"unperformed checks", "excerpts/truncation"} {
			if !strings.Contains(text, required) {
				t.Fatalf("zero-Dispatch recovery guidance omits %q", required)
			}
		}
	}
}
