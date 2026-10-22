package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/store"
)

func TestReadyTaskReauthorizationAppendsConfigurationApproval(t *testing.T) {
	for _, test := range []struct {
		name       string
		withBundle bool
		reference  string
	}{
		{"legacy-zero-bundles", false, "valid"},
		{"existing-bundle", true, "valid"},
		{"missing-reference", false, "missing"},
		{"missing-confirmation", false, "absent"},
		{"conflicting-reference", false, "conflicting"},
		{"ambiguous-reference", false, "ambiguous"},
		{"incorrect-v071-bundle", true, "incorrect-bundle"},
	} {
		t.Run(test.name, func(t *testing.T) {
			testReadyTaskReauthorization(t, test.withBundle, test.reference)
		})
	}
}

func testReadyTaskReauthorization(t *testing.T, withBundle bool, reference string) {
	dir := t.TempDir()
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCwd) })
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "user-config"))
	oldStorePath := storePath
	storePath = filepath.Join(dir, ".taxion", "ledger.json")
	t.Cleanup(func() { storePath = oldStorePath })
	if err := os.MkdirAll(filepath.Dir(storePath), 0o700); err != nil {
		t.Fatal(err)
	}
	writeReauthorizationConfig(t, false)
	legacyResolution, err := resolveConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	legacySelection := legacyResolution.Config
	task := core.GraphTask{
		ID: "legacy-ready", Title: "preserve approved task", Repo: legacySelection.Repository.Path,
		Type: "IMPLEMENTATION", Status: core.Draft, Issue: "Issue #109", CreatedFrom: "human:legacy",
		Owner: "CODER", Reviewer: "REVIEWER", Objective: "keep the approved contract",
		Scope: []string{"src"}, Acceptance: []string{"validation passes"},
		RequiredEvidence: []string{"validation report"}, ExitCondition: "validation passes",
		NextAction: "resume existing task", ValidationCommands: []string{"go test ./..."},
		Worktree: filepath.Join(dir, "task-worktree"), Branch: "legacy/ready",
		BaselineSHA: strings.Repeat("a", 40), FileAccess: core.DefaultFileAccessPolicy(),
	}
	decisionID := core.ConfigurationApprovalID(legacySelection.Execution.ApprovalRef, task.ID)
	task.AuthorizationID = "intake-confirmation:" + task.ID
	legacyApproval, err := json.Marshal(config.Approval{
		Kind: config.ApprovalKind, TaskID: task.ID, Reason: "existing v0.6.x approval",
		Allowed: []config.Config{legacySelection},
	})
	if err != nil {
		t.Fatal(err)
	}
	const immutableProposal = "sha256:b02845111a47d4525e30b52aa2cb2ba38fc42923340da33994eebff206597b45"
	const incorrectV071Basis = "a873ef1bf4e213a16710eb36eac2235aa0fe681d535c3f5e66de1ff74f0f8cd9"
	task.Decisions = []core.TaskDecision{
		{ID: "intake-confirmation:" + task.ID, Source: "human:legacy",
			Text: `{"taskId":"legacy-ready","expectedHash":"` + immutableProposal + `"}`},
		{ID: decisionID, Source: "human:legacy", Text: string(legacyApproval)},
	}
	switch reference {
	case "missing":
		task.Decisions[0].Text = `{"taskId":"legacy-ready"}`
	case "absent":
		task.Decisions = task.Decisions[1:]
		task.AuthorizationID = decisionID
	case "conflicting":
		task.Decisions = append(task.Decisions, core.TaskDecision{
			ID: "intake-confirmation:other-task", Source: "human:legacy",
			Text: `{"taskId":"legacy-ready","expectedHash":"sha256:` + incorrectV071Basis + `"}`,
		})
	case "ambiguous":
		task.Decisions[0].Text = `{"taskId":"legacy-ready","expectedHash":"` + immutableProposal +
			`","expectedHash":"sha256:` + incorrectV071Basis + `"}`
	}
	legacyConfiguration, err := approvalConfigurationFor(legacySelection, legacyResolution.Sources)
	if err != nil {
		t.Fatal(err)
	}
	if err := (core.Ledger{Tasks: []core.Task{{ID: task.ID, Title: task.Title, Repo: task.Repo}}}).Save(storePath); err != nil {
		t.Fatal(err)
	}
	var priorBundle core.ApprovalBundle
	if err := updateWorkspace(func(state *store.Workspace) error {
		state.Graph = core.TaskGraph{Version: 1}
		state.HasGraph = true
		if err := state.Graph.Track(task, "human", "read back legacy READY Task"); err != nil {
			return err
		}
		if err := state.Graph.Transition(task.ID, core.Ready, "human", "preserve READY state", core.TransitionProof{}); err != nil {
			return err
		}
		stored, ok := state.Graph.GraphTask(task.ID)
		if !ok {
			t.Fatal("tracked Task disappeared")
		}
		if !withBundle {
			return nil
		}
		proposalHash := immutableProposal
		if reference == "incorrect-bundle" {
			proposalHash = incorrectV071Basis
		}
		var err error
		priorBundle, err = core.ComposeApprovalBundleWithProposalHash(
			"legacy-approval-bundle", proposalHash, stored, legacyConfiguration,
			"human", "human:legacy", "existing v0.6.x authorization", time.Now().UTC(),
		)
		if err != nil {
			return err
		}
		return state.Graph.AppendApproval(priorBundle)
	}); err != nil {
		t.Fatal(err)
	}

	writeReauthorizationConfig(t, true)
	resolved, err := resolveConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	db, err := openWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.CheckConfiguration(db, task.ID, resolved.Config); err == nil {
		db.Close()
		t.Fatal("legacy endpoint approval unexpectedly passed the task-bound check")
	}
	db.Close()

	before, _, _, err := loadGraphReadOnly()
	if err != nil {
		t.Fatal(err)
	}
	beforeTask, ok := before.GraphTask(task.ID)
	if !ok {
		t.Fatal("READY Task disappeared before preview")
	}
	if !withBundle && len(before.Approvals) != 0 {
		t.Fatal("legacy fixture must start with zero approval bundles")
	}
	if reference != "valid" {
		for _, args := range [][]string{
			{"reauthorize", "--task", task.ID},
			{"reauthorize", "--task", task.ID, "--confirm", incorrectV071Basis},
		} {
			if _, err := runReauthorizationCommand(t, args); err == nil {
				t.Fatal("invalid immutable proposal reference was accepted")
			}
			after, _, _, err := loadGraphReadOnly()
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("invalid proposal reference wrote workspace state: %v", err)
			}
		}
		return
	}
	previewOutput, err := runReauthorizationCommand(t, []string{"reauthorize", "--task", task.ID})
	if err != nil {
		t.Fatal(err)
	}
	var preview taskReauthorizationPreview
	if err := json.Unmarshal([]byte(previewOutput), &preview); err != nil {
		t.Fatalf("preview was not JSON: %v (%s)", err, previewOutput)
	}
	if preview.Status != "awaiting-confirmation" || preview.State != core.Ready ||
		preview.ProposalHash != immutableProposal || preview.RecordHash == "" ||
		preview.ConfirmationToken == "" || !slices.Contains(preview.RedactedDriftPaths, "endpoints.apiBase") {
		t.Fatalf("preview omitted the approval basis or endpoint drift: %+v", preview)
	}
	recordHash, err := core.CanonicalSHA256(beforeTask)
	if err != nil || preview.RecordHash != recordHash || preview.ProposalHash == preview.RecordHash {
		t.Fatal("preview conflated immutable proposal and canonical record hashes")
	}
	d := preview.Dimensions
	basis := map[string]any{
		"bundleVersion": preview.BundleVersion, "canonicalizationVersion": preview.CanonicalizationVersion,
		"proposalHash": immutableProposal, "baselineDigest": d.Baseline, "scopeDigest": d.Scope,
		"acceptanceDigest": d.Acceptance, "budgetDigest": d.Budget, "permissionsDigest": d.Permission,
		"securityDigest": d.Security, "configurationDigest": d.Configuration,
	}
	basisJSON, err := json.Marshal(basis)
	if err != nil || preview.ConfirmationToken != fmt.Sprintf("%x", sha256.Sum256(basisJSON)) {
		t.Fatal("confirmation token does not independently match the immutable full basis")
	}
	for _, wrongProposal := range []string{incorrectV071Basis, recordHash} {
		basis["proposalHash"] = wrongProposal
		wrongJSON, err := json.Marshal(basis)
		if err != nil {
			t.Fatal(err)
		}
		wrongToken := fmt.Sprintf("%x", sha256.Sum256(wrongJSON))
		if _, err := runReauthorizationCommand(t, []string{"reauthorize", "--task", task.ID, "--confirm", wrongToken}); err == nil {
			t.Fatal("incorrect v0.7.1 proposal basis was accepted")
		}
		after, _, _, err := loadGraphReadOnly()
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("incorrect v0.7.1 basis wrote workspace state: %v", err)
		}
	}
	if strings.Contains(previewOutput, "credentialRef") ||
		strings.Contains(previewOutput, "gh:active") || strings.Contains(previewOutput, "copilot:logged-in") {
		t.Fatalf("preview exposed configuration values: %s", previewOutput)
	}
	ambiguous := beforeTask
	ambiguous.Decisions = slices.Clone(beforeTask.Decisions)
	ambiguous.Decisions[len(ambiguous.Decisions)-1].Text = "{"
	if _, _, err := priorTaskConfigurationApproval(ambiguous, mustApprovalConfiguration(t, resolved)); err == nil {
		t.Fatal("ambiguous legacy approval was accepted")
	}
	afterPreview, _, _, err := loadGraphReadOnly()
	if err != nil || !reflect.DeepEqual(before, afterPreview) {
		t.Fatalf("preview wrote workspace state: %v", err)
	}
	configPath := filepath.Join(".taxion", "config.yaml")
	configText, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(configText), `projectId: "42"`, `projectId: "43"`, 1)
	if changed == string(configText) {
		t.Fatal("failed to prepare endpoint drift")
	}
	if err := os.WriteFile(configPath, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runReauthorizationCommand(t, []string{"reauthorize", "--task", task.ID, "--confirm", preview.ConfirmationToken}); err == nil {
		t.Fatal("confirmation accepted configuration drift after preview")
	}
	if err := os.WriteFile(configPath, configText, 0o600); err != nil {
		t.Fatal(err)
	}
	afterDrift, _, _, err := loadGraphReadOnly()
	if err != nil || !reflect.DeepEqual(before, afterDrift) {
		t.Fatalf("configuration drift wrote workspace state: %v", err)
	}
	if _, err := runReauthorizationCommand(t, []string{"reauthorize", "--task", task.ID, "--confirm", "wrong"}); err == nil {
		t.Fatal("reauthorization accepted a mismatched confirmation token")
	}
	afterMismatch, _, _, err := loadGraphReadOnly()
	if err != nil || !reflect.DeepEqual(before, afterMismatch) {
		t.Fatalf("mismatched confirmation changed workspace state: %v", err)
	}

	writeReauthorizationConfig(t, true)
	if _, err := runReauthorizationCommand(t, []string{"reauthorize", "--task", task.ID, "--confirm", preview.ConfirmationToken}); err != nil {
		t.Fatalf("confirmed reauthorization failed: %v", err)
	}
	after, _, _, err := loadGraphReadOnly()
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := after.GraphTask(task.ID)
	if !ok || stored.Status != core.Ready || len(stored.Decisions) != len(beforeTask.Decisions)+1 ||
		!slices.Equal(stored.Decisions[:len(beforeTask.Decisions)], beforeTask.Decisions) {
		t.Fatal("reauthorization changed state or prior decisions")
	}
	contractOnly := stored
	contractOnly.Decisions = beforeTask.Decisions
	if !reflect.DeepEqual(contractOnly, beforeTask) {
		t.Fatal("reauthorization changed the Task contract")
	}
	if len(after.ReviewFlows) != 0 || len(after.Dispatches) != 0 {
		t.Fatal("reauthorization created a Flow or Dispatch")
	}
	if len(after.Approvals) != len(before.Approvals)+1 ||
		withBundle && !reflect.DeepEqual(after.Approvals[0], priorBundle) {
		t.Fatal("reauthorization replaced or removed the prior approval")
	}
	var addedApproval config.Approval
	if err := json.Unmarshal([]byte(stored.Decisions[len(stored.Decisions)-1].Text), &addedApproval); err != nil ||
		addedApproval.DecisionID != stored.Decisions[len(stored.Decisions)-1].ID ||
		addedApproval.AuthorizationRef == "" || addedApproval.ApprovalRef != legacySelection.Execution.ApprovalRef {
		t.Fatal("reauthorization did not append a unique configuration authorization reference")
	}
	reauthorized := after.Approvals[len(after.Approvals)-1]
	if reauthorized.ProposalHash != immutableProposal || reauthorized.RecordHash == preview.RecordHash {
		t.Fatal("proposal hash was not preserved or the authorized record was not extended")
	}
	currentConfiguration, err := approvalConfigurationFor(resolved.Config, resolved.Sources)
	if err != nil {
		t.Fatal(err)
	}
	if err := reauthorized.Verify(stored, currentConfiguration); err != nil {
		t.Fatalf("appended bundle failed verification: %v", err)
	}
	db, err = openWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.CheckConfiguration(db, task.ID, resolved.Config); err != nil {
		db.Close()
		t.Fatalf("task-bound configuration check did not pass: %v", err)
	}
	db.Close()

	repeatOutput, err := runReauthorizationCommand(t, []string{"reauthorize", "--task", task.ID, "--confirm", preview.ConfirmationToken})
	if err != nil || !strings.Contains(repeatOutput, `"status": "already-authorized"`) {
		t.Fatalf("same-bundle retry was not idempotent: %v (%s)", err, repeatOutput)
	}
	afterRepeat, _, _, err := loadGraphReadOnly()
	if err != nil || !reflect.DeepEqual(after, afterRepeat) {
		t.Fatalf("same-bundle retry appended duplicate state: %v", err)
	}

	writeReauthorizationConfig(t, true)
	configPath = filepath.Join(".taxion", "config.yaml")
	configText, err = os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	changed = strings.Replace(string(configText), `projectId: "42"`, `projectId: "43"`, 1)
	if changed == string(configText) {
		t.Fatal("failed to prepare endpoint drift")
	}
	if err := os.WriteFile(configPath, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	driftOutput, err := runReauthorizationCommand(t, []string{"reauthorize", "--task", task.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(driftOutput, "endpoints.projectId") {
		t.Fatalf("single endpoint change was not reported as drift: %s", driftOutput)
	}
}

func mustApprovalConfiguration(t *testing.T, resolution config.Resolution) core.ApprovalConfiguration {
	t.Helper()
	selection, err := approvalConfigurationFor(resolution.Config, resolution.Sources)
	if err != nil {
		t.Fatal(err)
	}
	return selection
}

func writeReauthorizationConfig(t *testing.T, explicitEndpoints bool) {
	t.Helper()
	text := `version: 1
project: {id: taxion}
repository:
  provider: gitlab
  host: gitlab.example.com
  path: hiono/taxion
  account: hiono
  credentialRef: scm:active
runtime:
  provider: copilot
  account: test
  credentialRef: copilot:active
  contractRef: contract:v1
execution:
  requester: human
  worker: local
  approvalRef: legacy-config-approval
`
	if explicitEndpoints {
		text += `endpoints:
  apiBase: https://gitlab.example.com/api/v4
  webBase: https://gitlab.example.com
  httpsCloneBase: https://gitlab.example.com
  sshHost: gitlab.example.com
  sshPort: "22"
  projectId: "42"
`
	}
	if err := os.WriteFile(filepath.Join(".taxion", "config.yaml"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runReauthorizationCommand(t *testing.T, args []string) (string, error) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = writer
	runErr := taskCmd(args)
	os.Stdout = stdout
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	return string(output), runErr
}
