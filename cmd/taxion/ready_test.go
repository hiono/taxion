package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/gitproof"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/runtime/controller"
)

func TestReadyIsStaticWithoutConfigurationWorkspaceOrModel(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("TAXION_RUNTIME_PROVIDER", "invalid-private-value")
	t.Setenv("COPILOT_HOME", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("PATH", "")
	t.Setenv("TAXION_TOKEN", "private-fixture-secret")
	old := storePath
	storePath = filepath.Join(t.TempDir(), "missing", "ledger.json")
	t.Cleanup(func() { storePath = old })
	var output bytes.Buffer
	if err := readyCmd(nil, &output); err != nil {
		t.Fatal(err)
	}
	if output.String() != readyPrompt || strings.Contains(output.String(), "private-fixture") {
		t.Fatal("bootstrap depended on or leaked environment data")
	}
	entries, err := os.ReadDir(".")
	if err != nil || len(entries) != 0 {
		t.Fatal("ready created local state", err)
	}
	if _, err := os.Stat(filepath.Dir(storePath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("ready created its store", err)
	}
	for _, args := range [][]string{{"ignored-request"}, {"--unknown"}} {
		if err := readyCmd(args, io.Discard); err == nil {
			t.Fatal("ready silently accepted unsupported input", args)
		}
	}
	if err := readyCmd([]string{"--help"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := readyCmd(nil, rejectingConfigWriter{}); err == nil {
		t.Fatal("ready ignored output failure")
	}
}

func TestReadyNavigationSeparatesSetupApprovalAndExecution(t *testing.T) {
	for _, text := range []string{
		"## Choose operations by purpose", "### Request and unapproved contract", "### Approve the exact hash and configuration",
		"## Execute an approved Flow", "## Continue according to results",
		"taxion doctor fix", "--dry-run", "do not run every command in order",
		"confirm makes the Task READY but starts no Dispatch or model",
	} {
		if !strings.Contains(readyPrompt, text) {
			t.Errorf("missing operation boundary: %s", text)
		}
	}
}

func TestReadyRecipeDrivesApprovedSameTreeReviewWithoutPlanner(t *testing.T) {
	root, _ := requestFixture(t)
	resolved, err := resolveConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := readyCmd(nil, &output); err != nil {
		t.Fatal(err)
	}
	examples := regexp.MustCompile("(?s)```json\\n(.*?)\\n```").FindAllSubmatch(output.Bytes(), -1)
	if len(examples) != 3 {
		t.Fatal("ready must provide intake, confirmation and budget examples")
	}
	var intake controller.IntakeRequest
	var confirmation controller.IntakeConfirmation
	var flow controller.ReviewFlowRequest
	for index, target := range []any{&intake, &confirmation, &flow} {
		if err := core.DecodeGraphInput(bytes.NewReader(examples[index][1]), target); err != nil {
			t.Fatal("ready recipe no longer matches CLI input", index, err)
		}
	}
	intake.Task.Repo = resolved.Config.Repository.Path
	intake.Task.Worktree = root
	intake.Task.Branch = gitTest(t, root, "branch", "--show-current")
	intake.Task.BaselineSHA = gitTest(t, root, "rev-parse", "HEAD")
	confirmation.Selection = resolved.Config
	confirmation.ConfigurationDecisionID = resolved.Config.Execution.ApprovalRef + "@task:" + intake.Task.ID
	flow.Repository = intake.Task.Repo
	flow.ReviewRoot = filepath.Join(t.TempDir(), "reviews")
	private := t.TempDir()
	write := func(name string, value any) string {
		t.Helper()
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(private, name)
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	output.Reset()
	if err := conversationCmd([]string{"intake", "--file", write("request.json", intake)}, &output); err != nil {
		t.Fatal(err)
	}
	var proposal struct {
		Task core.GraphTask `json:"task"`
		Hash string         `json:"hash"`
	}
	if err := json.Unmarshal(output.Bytes(), &proposal); err != nil || proposal.Task.Status != core.Draft {
		t.Fatal("recipe failed to retain unapproved intent", err)
	}
	if err := workflowCommand(t.Context(), []string{"start", "--file", write("flow.json", flow)}, io.Discard); err == nil {
		t.Fatal("unapproved recipe issued work")
	}
	confirmation.ExpectedHash = "sha256:wrong"
	if err := conversationCmd([]string{"confirm", "--file", write("confirmation.json", confirmation)}, io.Discard); err == nil {
		t.Fatal("recipe confirmed a different proposal")
	}
	confirmation.ExpectedHash = proposal.Hash
	if err := conversationCmd([]string{"confirm", "--file", write("confirmation.json", confirmation)}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := checkConfig([]string{"check", "--task", intake.Task.ID}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := workflowCommand(t.Context(), []string{"start", "--file", write("flow.json", flow)}, io.Discard); err != nil {
		t.Fatal(err)
	}
	db, state, err := loadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	first := state.Graph.Dispatches[0]
	if _, err := gitproof.ObserveStart(first); err != nil {
		t.Fatal(err)
	}
	if err := controller.CheckReviewFlowConfiguration(db, flow.ID, resolved.Config,
		map[string][]string{"CODER": {"view", "create", "edit", "apply_patch"}, "REVIEWER": {"view"}}); err != nil {
		t.Fatal("recipe omitted future Reviewer authorization", err)
	}
	var roles []string
	_, err = controller.RunReviewFlow(t.Context(), db, storePath, flow.ID,
		func(dispatch core.GraphDispatch) (runtime.Identity, error) {
			bound, bindErr := selectDispatchConfiguration(db, dispatch)
			if bindErr != nil {
				return nil, bindErr
			}
			dispatch = bound
			roles = append(roles, dispatch.Recipient)
			worker := &requestFixtureRuntime{
				role: dispatch.Recipient, id: dispatch.ID, worktree: dispatch.Instruction.Worktree,
				selection: dispatch.Configuration.Selection, events: make(chan runtime.Event, 3),
			}
			worker.send = func(string) error {
				result := core.DispatchResult{
					DispatchID: dispatch.ID, TaskID: dispatch.TaskID, Recipient: dispatch.Recipient,
					Repository: dispatch.Repository, Status: core.DispatchSucceeded,
					StartedSHA: dispatch.ExpectedStartSHA(), OutputSHA: dispatch.ExpectedStartSHA(),
					ChangedFiles: []string{}, Evidence: []string{"work-report"},
					Validations: []core.DispatchValidation{}, NextTransition: core.WaitingForReview,
				}
				path := filepath.Join(worker.worktree, "README.md")
				if worker.role == "CODER" {
					if err := os.WriteFile(path, []byte("Approved explanation.\n"), 0600); err != nil {
						return err
					}
					result.ChangedFiles = []string{"README.md"}
				} else {
					body, err := os.ReadFile(path)
					if err != nil || string(body) != "Approved explanation.\n" || worker.worktree == root {
						return errors.New("independent Reviewer did not receive the same output")
					}
					changed := false
					result.WorktreeChanged = &changed
					result.ReviewVerdict, result.NextTransition = "APPROVE", core.Approved
					result.Findings = []core.DispatchFinding{}
					result.Acceptance = []core.DispatchAcceptance{{
						Criterion: dispatch.Instruction.Acceptance[0], Status: "PASS", Evidence: []string{"work-report"},
					}}
				}
				body, err := json.Marshal(result)
				if err != nil {
					return err
				}
				worker.events <- runtime.Event{Type: runtime.EventUsage, Payload: `{"model":"fixture","inputTokens":10,"outputTokens":5,"totalNanoAIU":1,"aiCreditsStatus":"complete"}`}
				worker.events <- runtime.Event{Type: runtime.EventOutput, Payload: string(body)}
				worker.events <- runtime.Event{Type: runtime.EventTurnFinished}
				return nil
			}
			return &requestQuotaFixtureRuntime{worker}, nil
		}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	state, err = db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	saved, ok := state.Graph.ReviewFlow(flow.ID)
	if !ok || saved.Status != "COMPLETED" || strings.Join(roles, ",") != "CODER,REVIEWER" {
		t.Fatal("external operator recipe did not complete without Planner", saved.Status, roles)
	}
	coder, reviewer := state.Graph.Dispatches[0], state.Graph.Dispatches[1]
	if len(coder.Result.Validations) != 1 || coder.Result.Validations[0].Command != "git diff --check" ||
		coder.Result.Validations[0].ExitCode != 0 || coder.Result.Validations[0].StartedAt.IsZero() ||
		coder.Result.Validations[0].Worktree != root {
		t.Fatal("recipe did not preserve host-run validation evidence")
	}
	if coder.Result.OutputTreeSHA == "" || coder.Result.OutputTreeSHA != reviewer.Result.OutputTreeSHA ||
		gitTest(t, root, "rev-parse", "HEAD") != intake.Task.BaselineSHA {
		t.Fatal("recipe lost same-tree review or changed the baseline")
	}
}
