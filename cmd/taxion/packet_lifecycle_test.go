package main

import (
	"bytes"
	"encoding/json"
	"os"
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

func packetCLIWorkspace(t *testing.T) (core.GraphTask, string) {
	t.Helper()
	oldStore := storePath
	dir, task := authorizeWorkspace(t)
	t.Cleanup(func() { storePath = oldStore })
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "user-config"))
	task.Reviewer = "REVIEWER"
	task.AuthorizationID = "intake-confirmation:" + task.ID
	configFile := filepath.Join(dir, ".taxion", "config.yaml")
	configBytes, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configFile, bytes.ReplaceAll(configBytes, []byte("path: hiono/taxion"), []byte("path: owner/repo")), 0600); err != nil {
		t.Fatal(err)
	}
	configuration, err := approvalConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := resolveConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	approval, err := json.Marshal(config.Approval{Kind: config.ApprovalKind, TaskID: task.ID,
		Reason: "existing exact configuration", Allowed: []config.Config{resolution.Config}})
	if err != nil {
		t.Fatal(err)
	}
	const proposal = "sha256:b02845111a47d4525e30b52aa2cb2ba38fc42923340da33994eebff206597b45"
	task.Decisions = []core.TaskDecision{
		{ID: task.AuthorizationID, Source: "human:legacy", Text: `{"taskId":"` + task.ID + `","expectedHash":"` + proposal + `"}`},
		{ID: core.ConfigurationApprovalID(resolution.Config.Execution.ApprovalRef, task.ID), Source: "human:legacy", Text: string(approval)},
	}
	if err := updateWorkspace(func(state *store.Workspace) error {
		state.HasGraph, state.Graph = true, core.TaskGraph{Version: 1}
		if err := state.Graph.Track(task, "human", "legacy contract"); err != nil {
			return err
		}
		if err := state.Graph.Transition(task.ID, core.Ready, "human", "existing READY Task", core.TransitionProof{}); err != nil {
			return err
		}
		stored, _ := state.Graph.GraphTask(task.ID)
		bundle, err := core.ComposeReauthorizationApprovalBundleWithProposalHash("existing-bundle", proposal,
			stored, configuration, "human", "human:legacy", "preserve bundle", time.Now().UTC())
		if err != nil {
			return err
		}
		task = stored
		return state.Graph.AppendApproval(bundle)
	}); err != nil {
		t.Fatal(err)
	}
	request := packetLifecycleRequest{Version: 1, TaskID: task.ID, Actor: "human", Source: "human:recovery",
		Reason: "recover missing artifacts", Unavailable: []store.UnavailablePacket{
			{Role: "CODER", Hash: strings.Repeat("a", 64), Evidence: "Referenced artifact is not available."},
		}}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "packet-request.json")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	return task, file
}

func TestPacketAuthorizationCLIPreviewConfirmAndReadback(t *testing.T) {
	task, request := packetCLIWorkspace(t)
	before, err := packetWorkspaceSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	files := snapshotFiles(t, filepath.Dir(storePath))
	var legacy bytes.Buffer
	if err := rolePacketCommand([]string{"list", "--task", task.ID}, &legacy); err != nil ||
		!strings.Contains(legacy.String(), `"classification":"legacy-unverified"`) {
		t.Fatalf("legacy Task was silently treated as packet-verified: %v", err)
	}
	output, err := runReauthorizationCommand(t, []string{"authorize-packets", "--request", request})
	if err != nil {
		t.Fatal(err)
	}
	var preview struct {
		Authorization     store.PacketAuthorization `json:"authorization"`
		ConfirmationToken string                    `json:"confirmationToken"`
	}
	if err := json.Unmarshal([]byte(output), &preview); err != nil {
		t.Fatal(err)
	}
	basis := preview.Authorization
	basis.Hash = ""
	data, err := core.CanonicalJSON(basis)
	if err != nil || preview.ConfirmationToken != "sha256:"+core.SHA256Hex(data) ||
		preview.Authorization.ReviewerPolicy != "same-contract-reviewer/v1" {
		t.Fatal("preview token does not bind the full authorization basis")
	}
	if !reflect.DeepEqual(files, snapshotFiles(t, filepath.Dir(storePath))) {
		t.Fatal("preview changed workspace database/WAL/SHM")
	}
	if _, err := runReauthorizationCommand(t, []string{"authorize-packets", "--request", request, "--confirm", "wrong"}); err == nil {
		t.Fatal("wrong token accepted")
	}
	if !reflect.DeepEqual(files, snapshotFiles(t, filepath.Dir(storePath))) {
		t.Fatal("wrong token wrote workspace files")
	}
	for i := 0; i < 2; i++ {
		if _, err := runReauthorizationCommand(t, []string{"authorize-packets", "--request", request, "--confirm", preview.ConfirmationToken}); err != nil {
			t.Fatal(err)
		}
	}
	after, err := packetWorkspaceSnapshot()
	if err != nil || len(after.Records.PacketAuthorizations) != 1 || len(after.Records.Packets) != 0 ||
		!reflect.DeepEqual(before.Graph, after.Graph) || !reflect.DeepEqual(before.Ledger, after.Ledger) {
		t.Fatalf("authorization did not preserve Task, existing decisions, state and ApprovalBundle: %v", err)
	}
	db, err := openWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	selected, err := resolveConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.CheckConfiguration(db, task.ID, selected.Config); err != nil {
		t.Fatalf("packet authorization invalidated task-bound configuration: %v", err)
	}
	var list bytes.Buffer
	if err := rolePacketCommand([]string{"list", "--task", task.ID}, &list); err != nil ||
		!strings.Contains(list.String(), `"classification":"lifecycle-authorized"`) {
		t.Fatalf("lifecycle audit readback failed: %v", err)
	}
	packet, err := core.NewRoleHandoffPacket(core.RoleHandoffPacketFields{TaskID: task.ID,
		TaskHash: preview.Authorization.TaskHash, BaselineSHA: task.BaselineSHA,
		TargetTreeHash: preview.Authorization.InitialTree, ScopeDigest: preview.Authorization.ScopeDigest,
		AcceptanceDigest: preview.Authorization.Acceptance, Role: "CODER",
		RequiredOutputSchema: "taxion.dispatch-result/v1", ProhibitedActions: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateWorkspace(func(s *store.Workspace) error { return s.Records.SavePacket(packet) }); err != nil {
		t.Fatal(err)
	}
	var read bytes.Buffer
	if err := rolePacketCommand([]string{"read", "--expected-hash", packet.Hash()}, &read); err != nil {
		t.Fatal(err)
	}
	if _, err := core.ReadRoleHandoffPacket(read.Bytes(), packet.Hash()); err != nil {
		t.Fatal(err)
	}
}

func TestPacketAuthorizationCLIFailClosed(t *testing.T) {
	_, request := packetCLIWorkspace(t)
	data, err := os.ReadFile(request)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := packetWorkspaceSnapshot()
	for _, replacement := range []struct{ old, new string }{
		{`"version":1`, `"version":99`},
		{`"recover missing artifacts"`, `"Authorization: Bearer private-test-value-repeated-long"`},
		{`"role":"CODER"`, `"role":"UNKNOWN"`},
		{`"Referenced artifact is not available."`, `""`},
		{`"taskId":"contract"`, `"taskId":"missing"`},
	} {
		body := strings.Replace(string(data), replacement.old, replacement.new, 1)
		if err := os.WriteFile(request, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		output, err := runReauthorizationCommand(t, []string{"authorize-packets", "--request", request})
		if err == nil || strings.Contains(output+err.Error(), "private-test-value") {
			t.Fatalf("case %s: accepted=%v leaked=%v", replacement.old, err == nil, strings.Contains(output, "private-test-value"))
		}
		after, err := packetWorkspaceSnapshot()
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("invalid request changed workspace: %v", err)
		}
	}
}
