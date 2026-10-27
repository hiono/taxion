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
		{"conversation intake", []string{"JSON envelope", "DRAFT", "supersedes", "targetTreeSha", "taxion ready"}},
		{"conversation confirm", []string{"expectedHash", "selection", "configurationDecisionId", "additionalTaskIds", "replacesFlow",
			"workflow feedback-target", "updatedAt", "resultHash", "ApprovalBundle", "No combined DRAFT"}},
		{"workflow plan", []string{"JSON input", "READY", "stageBudget", "stagePlanHash", "provider-stop",
			"continuationChange", "savedOutputDecision", "SUSPENDED", "no combined DRAFT"}},
		{"workflow start", []string{"JSON input", "stageBudget", "stagePlanHash", "actual user approval", "not a model turn"}},
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
	} {
		_, example, ok := strings.Cut(test.text, "\n{")
		if !ok {
			t.Fatal("help omits a JSON example")
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
	}
}
