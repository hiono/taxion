package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/store"
)

func freshReadyFixture(t *testing.T) (graphRequest, string) {
	t.Helper()
	oldStore := storePath
	dir, task := authorizeWorkspace(t)
	t.Cleanup(func() { storePath = oldStore })
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "user-config"))
	configPath := filepath.Join(dir, ".taxion", "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(strings.ReplaceAll(string(data), "path: hiono/taxion", "path: owner/repo")), 0600); err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	task.Reviewer = "REVIEWER"
	task.Issue = "NOT_REQUIRED: synthetic fresh authorization regression"
	task.AuthorizationID = "intake-confirmation:" + task.ID
	proposal, err := core.CanonicalSHA256("synthetic-fresh-ready-proposal")
	if err != nil {
		t.Fatal(err)
	}
	approval, err := json.Marshal(config.Approval{Kind: config.ApprovalKind, TaskID: task.ID,
		Reason: "existing exact synthetic configuration", Allowed: []config.Config{resolved.Config}})
	if err != nil {
		t.Fatal(err)
	}
	task.Decisions = []core.TaskDecision{
		{ID: task.AuthorizationID, Source: "human:synthetic",
			Text: fmt.Sprintf(`{"taskId":%q,"expectedHash":"sha256:%s"}`, task.ID, proposal)},
		{ID: core.ConfigurationApprovalID(resolved.Config.Execution.ApprovalRef, task.ID),
			Source: "human:synthetic", Text: string(approval)},
	}
	if err := updateWorkspace(func(state *store.Workspace) error {
		state.HasGraph, state.Graph = true, core.TaskGraph{Version: 1}
		if err := state.Graph.Track(task, "human", "synthetic intake"); err != nil {
			return err
		}
		return state.Graph.Transition(task.ID, core.Ready, "human", "synthetic confirmed contract", core.TransitionProof{})
	}); err != nil {
		t.Fatal(err)
	}
	state, err := packetWorkspaceSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	task, _ = state.Graph.GraphTask(task.ID)
	request := graphRequest{Task: task, Actor: "human", Reason: "approve missing fresh bundle"}
	return request, authorizeManifest(t, task)
}

func freshManifest(t *testing.T, request graphRequest) string {
	t.Helper()
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	return file
}

func freshPreview(t *testing.T, file string) string {
	t.Helper()
	output, err := runReauthorizationCommand(t, []string{"authorize", "--manifest", file})
	if err != nil {
		t.Fatal(err)
	}
	var preview struct {
		Token string `json:"confirmationToken"`
	}
	if err := json.Unmarshal([]byte(output), &preview); err != nil || preview.Token == "" {
		t.Fatal("missing fresh preview token", output, err)
	}
	return preview.Token
}

func TestTaskAuthorizeReusesFormalIntakeConfirmationWithoutAnotherToken(t *testing.T) {
	request, _ := freshReadyFixture(t)
	resolved, err := resolveConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	db, err := openWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	task := request.Task
	task.ID, task.Status, task.AuthorizationID, task.Decisions = "actual-confirmation", core.Draft, "", nil
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		state.Graph.ApprovalSchemaVersion = core.CurrentApprovalSchemaVersion
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	task, err = controller.ProposeIntake(db, controller.IntakeRequest{
		Task: task, Request: "Implement this contract.", Actor: "human", Source: "human:request"})
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := controller.IntakeHash(task)
	task, err = controller.ConfirmIntake(db, controller.IntakeConfirmation{
		TaskID: task.ID, ExpectedHash: hash, Actor: "human", Source: "human:confirmed-contract",
		Reason: "Approve the displayed contract once.", Selection: resolved.Config})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := packetWorkspaceSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	file := freshManifest(t, graphRequest{Task: task, Actor: "operator", Reason: "Use the already approved contract."})
	output, err := runReauthorizationCommand(t, []string{"authorize", "--manifest", file})
	if err != nil {
		t.Fatal("formal confirmation required a second token", output, err)
	}
	var report approvalGateReport
	if err := json.Unmarshal([]byte(output), &report); err != nil || !report.Reused ||
		report.ApprovalID != task.AuthorizationID {
		t.Fatal("original derived approval was not reused", output, err)
	}
	after, err := packetWorkspaceSnapshot()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("readback changed the original approval provenance", err)
	}
}

func TestFreshReadyAuthorizationLifecycleAndRestart(t *testing.T) {
	request, _ := freshReadyFixture(t)
	file := freshManifest(t, request)
	before, err := packetWorkspaceSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	files := snapshotFiles(t, filepath.Dir(storePath))
	token := freshPreview(t, file)
	if !reflect.DeepEqual(files, snapshotFiles(t, filepath.Dir(storePath))) {
		t.Fatal("preview wrote store bytes")
	}
	if _, err := runReauthorizationCommand(t, []string{"authorize", "--manifest", file, "--confirm", "incorrect"}); err == nil {
		t.Fatal("wrong token accepted")
	}
	if !reflect.DeepEqual(files, snapshotFiles(t, filepath.Dir(storePath))) {
		t.Fatal("refusal wrote store bytes")
	}
	cmd := freshProcess(t, file, token)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fresh process authorization: %v %s", err, output)
	}
	after, err := packetWorkspaceSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	configuration, err := approvalConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Graph.Approvals) != 1 {
		t.Fatal("expected exactly one appended bundle")
	}
	bundle := after.Graph.Approvals[0]
	if err := bundle.Verify(request.Task, configuration); err != nil {
		t.Fatal(err)
	}
	after.Graph.Approvals = before.Graph.Approvals
	after.Graph.ApprovalSchemaVersion = before.Graph.ApprovalSchemaVersion
	if !reflect.DeepEqual(before, after) {
		t.Fatal("bundle creation changed Task, decisions, lifecycle, records or ledger")
	}
	files = snapshotFiles(t, filepath.Dir(storePath))
	for i := 0; i < 2; i++ {
		if output, err := freshProcess(t, file, token).CombinedOutput(); err != nil {
			t.Fatalf("restart idempotency: %v %s", err, output)
		}
	}
	if !reflect.DeepEqual(files, snapshotFiles(t, filepath.Dir(storePath))) {
		t.Fatal("idempotent authorization wrote store bytes")
	}
	state, err := packetWorkspaceSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	gate, err := controller.ContractGateReport(state.Graph, request.Task.ID, configuration)
	if err != nil || gate.Verdict != controller.GateVerdictPass {
		t.Fatal("fresh bundle is not usable by start gate", gate, err)
	}
	resolved, err := resolveConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	db, err := openWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := controller.CheckConfiguration(db, request.Task.ID, resolved.Config); err != nil {
		t.Fatal(err)
	}
	auth, err := controller.PreviewPacketAuthorization(state, request.Task.ID, "human", "human:synthetic", "canonical lifecycle", nil, configuration)
	if err != nil {
		t.Fatal("fresh bundle is not usable by packet lifecycle", err)
	}
	if err := db.UpdateWorkspace(func(current *store.Workspace) error {
		return controller.AppendPacketAuthorization(current, auth, configuration)
	}); err != nil {
		t.Fatal(err)
	}
	repairs := 0
	flow, err := controller.StartReviewFlow(db, storePath, controller.ReviewFlowRequest{
		ID: "synthetic-fresh-flow", TaskID: request.Task.ID, Repository: request.Task.Repo,
		ReviewRoot: filepath.Join(t.TempDir(), "reviews"), MaxRepairs: &repairs, MaxNanoAIU: 1,
		TimeoutSeconds: 60, Configuration: configuration,
		Authorization: core.TaskDecision{ID: "synthetic-start", Source: "human:synthetic", Text: "issue only; no runtime"},
	})
	if err != nil || flow.Status != "RUNNING" {
		t.Fatal("workflow start did not accept fresh bundle", flow, err)
	}
}

func TestFreshReadyAuthorizationRejectsChangedInputs(t *testing.T) {
	for _, name := range []string{"manifest", "proposal", "configuration", "actor", "reason", "secret", "schema", "unknown", "stale-source", "stale-config", "duplicate-content"} {
		t.Run(name, func(t *testing.T) {
			request, _ := freshReadyFixture(t)
			file := freshManifest(t, request)
			token := freshPreview(t, file)
			switch name {
			case "manifest":
				request.Task.Objective = "different contract"
			case "proposal":
				request.Task.Decisions[0].Text = `{"taskId":"other","expectedHash":"invalid"}`
			case "configuration", "stale-config":
				path := filepath.Join(filepath.Dir(storePath), "config.yaml")
				body, _ := os.ReadFile(path)
				if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(body), "contractRef: enterprise", "contractRef: changed")), 0600); err != nil {
					t.Fatal(err)
				}
			case "actor":
				request.Actor = ""
			case "reason":
				request.Reason = ""
			case "secret":
				request.Reason = "ghp_" + strings.Repeat("z", 36)
			case "stale-source":
				if err := updateWorkspace(func(state *store.Workspace) error {
					state.Records.Requests = append(state.Records.Requests, store.RequestRecord{
						RequestID: "synthetic-concurrent-request", Kind: "planning", Sequence: 1,
						Payload: `{}`, Hash: core.SHA256Hex([]byte(`{}`)), At: time.Now().UTC(),
					})
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			case "duplicate-content":
				if _, err := runReauthorizationCommand(t, []string{"authorize", "--manifest", file, "--confirm", token}); err != nil {
					t.Fatal(err)
				}
				request.Reason = "different authorization request"
			}
			file = freshManifest(t, request)
			if name == "schema" || name == "unknown" {
				data, _ := os.ReadFile(file)
				if err := os.WriteFile(file, append([]byte(`{"version":99,`), data[1:]...), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before := snapshotFiles(t, filepath.Dir(storePath))
			output, err := runReauthorizationCommand(t, []string{"authorize", "--manifest", file, "--confirm", token})
			if err == nil {
				t.Fatal("invalid authorization was accepted")
			}
			var diagnostic struct {
				Code         string `json:"code"`
				StateChanged bool   `json:"stateChanged"`
			}
			if err := json.Unmarshal([]byte(output), &diagnostic); err != nil ||
				diagnostic.Code == "" || diagnostic.StateChanged {
				t.Fatal("refusal omitted structured unchanged-state diagnosis", output, err)
			}
			if !reflect.DeepEqual(before, snapshotFiles(t, filepath.Dir(storePath))) {
				t.Fatal("refusal changed persistent bytes")
			}
		})
	}
}

func TestFreshReadyAuthorizationRejectsSavedState(t *testing.T) {
	request, _ := freshReadyFixture(t)
	original, err := packetWorkspaceSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	configuration, err := approvalConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"state", "flow", "dispatch", "missing-config", "binding", "missing-proposal", "proposal-binding", "schema", "bundle"} {
		t.Run(name, func(t *testing.T) {
			data, _ := json.Marshal(original)
			var state store.Workspace
			if err := json.Unmarshal(data, &state); err != nil {
				t.Fatal(err)
			}
			candidate := request
			switch name {
			case "state":
				state.Graph.Tasks[0].Status = core.Active
			case "flow":
				state.Graph.ReviewFlows = []core.ReviewFlow{{ID: "prior-flow", TaskID: request.Task.ID}}
			case "dispatch":
				state.Graph.Dispatches = []core.GraphDispatch{{ID: "prior-dispatch", TaskID: request.Task.ID}}
			case "missing-config":
				state.Graph.Tasks[0].Decisions = state.Graph.Tasks[0].Decisions[:1]
			case "binding":
				state.Graph.Tasks[0].AuthorizationID = state.Graph.Tasks[0].Decisions[1].ID
			case "missing-proposal":
				state.Graph.Tasks[0].Decisions[0].Text = `{}`
			case "proposal-binding":
				state.Graph.Tasks[0].Decisions[0].Text = `{"taskId":"another-task","expectedHash":"invalid"}`
			case "schema":
				state.Graph.ApprovalSchemaVersion = 99
			case "bundle":
				bundle, err := core.ComposeReauthorizationApprovalBundleWithProposalHash("conflict", "different-proposal", request.Task,
					configuration, "human", "human:human", request.Reason, original.Graph.History[0].At)
				if err != nil {
					t.Fatal(err)
				}
				state.Graph.Approvals = []core.ApprovalBundle{bundle}
				state.Graph.ApprovalSchemaVersion = core.CurrentApprovalSchemaVersion
			}
			candidate.Task = state.Graph.Tasks[0]
			before, _ := core.CanonicalSHA256(state)
			if _, err := checkReadyAuthorization(state, candidate, configuration); err == nil {
				t.Fatal("invalid state was accepted")
			}
			after, _ := core.CanonicalSHA256(state)
			if before != after {
				t.Fatal("validation changed state")
			}
		})
	}
}

func freshProcess(t *testing.T, file, token string) *exec.Cmd {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestFreshReadyAuthorizationProcess$")
	cmd.Env = append(os.Environ(), "TAXION_SYNTHETIC_READY_STORE="+storePath,
		"TAXION_SYNTHETIC_READY_MANIFEST="+file, "TAXION_SYNTHETIC_READY_CONFIRM="+token)
	return cmd
}

func TestFreshReadyAuthorizationProcess(t *testing.T) {
	if path := os.Getenv("TAXION_SYNTHETIC_READY_STORE"); path != "" {
		storePath = path
		if err := taskAuthorizeCmd(os.Getenv("TAXION_SYNTHETIC_READY_MANIFEST"), os.Getenv("TAXION_SYNTHETIC_READY_CONFIRM")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFreshReadyAuthorizationConcurrentProcesses(t *testing.T) {
	request, _ := freshReadyFixture(t)
	file := freshManifest(t, request)
	token := freshPreview(t, file)
	first, second := freshProcess(t, file, token), freshProcess(t, file, token)
	var firstOutput, secondOutput bytes.Buffer
	first.Stdout, first.Stderr = &firstOutput, &firstOutput
	second.Stdout, second.Stderr = &secondOutput, &secondOutput
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	if err := second.Start(); err != nil {
		_ = first.Wait()
		t.Fatal(err)
	}
	err1, err2 := first.Wait(), second.Wait()
	if err1 != nil && err2 != nil {
		t.Fatal("neither concurrent authorization completed", err1, err2, firstOutput.String(), secondOutput.String())
	}
	state, err := packetWorkspaceSnapshot()
	if err != nil || len(state.Graph.Approvals) != 1 || len(state.Graph.ReviewFlows) != 0 || len(state.Graph.Dispatches) != 0 {
		t.Fatal("concurrent authorization did not append exactly one bundle", err)
	}
}
