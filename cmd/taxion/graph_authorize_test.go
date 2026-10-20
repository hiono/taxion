package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
)

func authorizeWorkspace(t *testing.T) (string, core.GraphTask) {
	t.Helper()
	dir := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	if err := os.Mkdir(".taxion", 0o700); err != nil {
		t.Fatal(err)
	}
	config := "version: 1\n" +
		"project:\n  id: taxion\n" +
		"repository:\n  provider: github\n  host: github.com\n  path: hiono/taxion\n  account: hiono\n  credentialRef: gh:active\n" +
		"runtime:\n  provider: copilot\n  account: bundle-account\n  credentialRef: copilot:logged-in\n  contractRef: enterprise\n" +
		"execution:\n  requester: human\n  worker: local\n  approvalRef: bundle-approval\n"
	if err := os.WriteFile(filepath.Join(".taxion", "config.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	storePath = filepath.Join(dir, ".taxion", "ledger.json")
	task := contractDraftTask(t)
	ledger := core.Ledger{Tasks: []core.Task{{ID: task.ID, Title: task.Title, Repo: task.Repo}}}
	if err := ledger.Save(storePath); err != nil {
		t.Fatal(err)
	}
	return dir, task
}

func authorizeManifest(t *testing.T, task core.GraphTask) string {
	t.Helper()
	request := map[string]any{"task": task, "actor": "human", "reason": "approve once"}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTaskAuthorizeStoresOneAppendOnlyBundle(t *testing.T) {
	_, task := authorizeWorkspace(t)
	file := authorizeManifest(t, task)
	if err := taskCmd([]string{"authorize", "--manifest", file}); err != nil {
		t.Fatalf("authorize failed: %v", err)
	}
	graph, _, _, err := loadGraphReadOnly()
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Approvals) != 1 {
		t.Fatalf("approvals = %d, want 1", len(graph.Approvals))
	}
	if len(graph.Dispatches) != 0 || len(graph.ReviewFlows) != 0 {
		t.Fatal("authorize advanced to Flow or Dispatch")
	}
	bundle := graph.Approvals[0]
	if bundle.ProposalHash == bundle.RecordHash {
		t.Fatal("defaults-only difference between proposal and record was not preserved")
	}
	if bundle.Configuration.Digest == "" || bundle.BundleVersion != core.ApprovalBundleVersion ||
		bundle.CanonicalizationVersion != core.ApprovalCanonicalizationVersion {
		t.Fatalf("bundle is incomplete: %+v", bundle)
	}
	stored, _ := graph.GraphTask(task.ID)
	if err := bundle.CheckApprovalAdvance(stored, bundle.Configuration); err != nil {
		t.Fatalf("one approval did not cover every stage: %v", err)
	}
	if _, ok := graph.MatchingApproval(task.ID, stored, bundle.Configuration); !ok {
		t.Fatal("stored bundle does not match its own dimensions")
	}

	proposal := bundle.ProposalHash
	if err := taskCmd([]string{"authorize", "--manifest", file}); err != nil {
		t.Fatalf("second authorize failed: %v", err)
	}
	graph, _, _, err = loadGraphReadOnly()
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Approvals) != 2 {
		t.Fatalf("approvals = %d, want 2", len(graph.Approvals))
	}
	if graph.Approvals[0].ProposalHash != proposal {
		t.Fatal("append-only approval overwrote an earlier record")
	}

	updated := task
	updated.Scope = append(append([]string{}, task.Scope...), "cmd/taxion/extra.go")
	changed := authorizeManifest(t, updated)
	if err := taskCmd([]string{"authorize", "--manifest", changed}); err != nil {
		t.Fatalf("authorize after scope change failed: %v", err)
	}
	graph, _, _, err = loadGraphReadOnly()
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Approvals) != 3 {
		t.Fatalf("approvals = %d, want 3", len(graph.Approvals))
	}
	stored, _ = graph.GraphTask(task.ID)
	if err := graph.Approvals[0].Verify(stored, graph.Approvals[0].Configuration); err == nil {
		t.Fatal("old bundle still matches after a scope change")
	}
	if _, ok := graph.MatchingApproval(task.ID, stored, graph.Approvals[0].Configuration); !ok {
		t.Fatal("newest valid bundle was not selected")
	}
	drifts, err := graph.Approvals[0].Drifts(stored, graph.Approvals[0].Configuration)
	if err != nil {
		t.Fatal(err)
	}
	scopeDrift := false
	for _, drift := range drifts {
		if drift.Dimension == "scope" && drift.AuthorizationImpact == core.ImpactReapproval && len(drift.Fields) != 0 {
			scopeDrift = true
		}
	}
	if !scopeDrift {
		t.Fatalf("scope change was not reported as re-approval: %+v", drifts)
	}
}

func TestTaskAuthorizeFailsClosed(t *testing.T) {
	_, task := authorizeWorkspace(t)
	broken := task
	broken.Title = ""
	if err := taskCmd([]string{"authorize", "--manifest", authorizeManifest(t, broken)}); err == nil {
		t.Fatal("invalid contract was authorized")
	}
	leaked := task
	leaked.Objective = "publish with ghp_" + strings.Repeat("q", 36)
	if err := taskCmd([]string{"authorize", "--manifest", authorizeManifest(t, leaked)}); err == nil {
		t.Fatal("contract with a credential-like value was authorized")
	}
	graph, _, _, err := loadGraphReadOnly()
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Approvals) != 0 || len(graph.Dispatches) != 0 || len(graph.ReviewFlows) != 0 {
		t.Fatal("failed authorization advanced state")
	}
}
