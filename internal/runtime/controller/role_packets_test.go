package controller

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

const packetProposal = "sha256:b02845111a47d4525e30b52aa2cb2ba38fc42923340da33994eebff206597b45"

func packetConfigurationFixture(t *testing.T) config.Resolution {
	t.Helper()
	resolved, err := config.Resolve([]config.Layer{{Source: "user", Input: []byte(`version: 1
project: {id: demo}
repository: {provider: github, host: github.com, path: owner/repo, account: scm-user, credentialRef: scm}
runtime: {provider: copilot, account: ai-user, credentialRef: ai, contractRef: contract}
execution: {requester: human, worker: local, approvalRef: configuration-approval}
`)}})
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func packetFlowFixture(t *testing.T, outputMode ...string) (*store.Store, string, ReviewFlowRequest, store.PacketAuthorization) {
	t.Helper()
	mode := ""
	if len(outputMode) != 0 {
		mode = outputMode[0]
	}
	resolved := packetConfigurationFixture(t)
	approval := config.Approval{Kind: config.ApprovalKind, TaskID: "implementation",
		Reason: "Exact parent-only configuration", Allowed: []config.Config{resolved.Config}}
	db, ledger, request := reviewFlowFixtureWithOutputMode(t, mode, core.TaskDecision{
		ID: "intake-confirmation:implementation", Source: "human:legacy",
		Text: `{"taskId":"implementation","expectedHash":"` + packetProposal + `"}`,
	}, core.TaskDecision{
		ID:     core.ConfigurationApprovalID(resolved.Config.Execution.ApprovalRef, "implementation"),
		Source: "human:configuration", Text: string(mustFlowJSON(t, approval)),
	})
	var err error
	request.Configuration, err = core.SelectApprovalConfiguration(mustFlowJSON(t, resolved.Config), nil)
	if err != nil {
		t.Fatal(err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	task, _ := state.Graph.GraphTask(request.TaskID)
	bundle, err := core.ComposeReauthorizationApprovalBundleWithProposalHash("packet-approval",
		packetProposal, task, request.Configuration, "human", "human:legacy", "exact configuration", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateWorkspace(func(current *store.Workspace) error {
		return current.Graph.AppendApproval(bundle)
	}); err != nil {
		t.Fatal(err)
	}
	state, err = db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	auth, err := PreviewPacketAuthorization(state, request.TaskID, "human", "human:recovery",
		"Recover unavailable packet artifacts", []store.UnavailablePacket{
			{Role: "CODER", Hash: strings.Repeat("c", 64), Evidence: "External manifest Coder artifact was not preserved."},
			{Role: "VALIDATOR", Hash: strings.Repeat("d", 64), Evidence: "External manifest requires evidence that does not yet exist."},
			{Role: "REVIEWER", Hash: strings.Repeat("e", 64), Evidence: "External manifest requires evidence that does not yet exist."},
		}, request.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	return db, ledger, request, auth
}

func authorizePacketFixture(t *testing.T, db *store.Store, request ReviewFlowRequest, auth store.PacketAuthorization) {
	t.Helper()
	for i := 0; i < 2; i++ {
		if err := db.UpdateWorkspace(func(current *store.Workspace) error {
			return AppendPacketAuthorization(current, auth, request.Configuration)
		}); err != nil {
			t.Fatal(err)
		}
	}
	state, err := db.LoadWorkspace()
	if err != nil || len(state.Records.PacketAuthorizations) != 1 {
		t.Fatalf("append is not idempotent: %v", err)
	}
}

func TestPacketLifecycleSequentialRecovery(t *testing.T) {
	for _, mode := range []string{core.OutputModeCommit, core.OutputModeWorktreeTreeSnapshot} {
		t.Run(mode, func(t *testing.T) { testPacketLifecycleSequentialRecovery(t, mode) })
	}
}

func testPacketLifecycleSequentialRecovery(t *testing.T, mode string) {
	db, ledger, request, auth := packetFlowFixture(t, mode)
	before, _ := db.LoadWorkspace()
	authorizePacketFixture(t, db, request, auth)
	after, _ := db.LoadWorkspace()
	if !reflect.DeepEqual(before.Graph, after.Graph) || !reflect.DeepEqual(before.Ledger, after.Ledger) ||
		len(after.Records.Packets) != 0 || auth.ProposalHash != packetProposal {
		t.Fatal("packet authorization changed Task, proposal, ApprovalBundle or execution")
	}
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	resolved := packetConfigurationFixture(t)
	preflightBefore, _ := db.LoadWorkspace()
	if err := CheckReviewFlowConfiguration(db, flow.ID, resolved.Config, nil); err != nil {
		t.Fatal("lifecycle approval was not accepted before Coder", err)
	}
	preflightAfter, _ := db.LoadWorkspace()
	if !reflect.DeepEqual(preflightBefore, preflightAfter) {
		t.Fatal("configuration preflight persisted future approvals, packets or resources")
	}
	issued, _ := db.LoadWorkspace()
	if len(issued.Records.Packets) != 1 || len(issued.Records.PacketAudits) != 1 {
		t.Fatal("future packets were required or fabricated before Coder")
	}
	coderPacket, err := issued.Records.ReadPacket(issued.Records.PacketAudits[0].PacketHash)
	if err != nil || coderPacket.Readback().PreviousEvidenceHash != "" {
		t.Fatalf("initial Coder predecessor is not empty: %v", err)
	}
	coder := executeFlowDispatch(t, db, flow, "", true, resolved)
	completed, _ := db.LoadWorkspace()
	if len(completed.Records.Packets) != 1 {
		t.Fatal("Validator or Reviewer packet was fabricated before handoff")
	}
	if err := preparePacketValidator(db, flow.ID, ""); err != nil {
		t.Fatal(err)
	}
	prepared, _ := db.LoadWorkspace()
	if len(prepared.Records.Packets) != 2 || len(prepared.Records.PacketAudits) != 2 ||
		prepared.Records.PacketAudits[1].Validation != "" {
		t.Fatal("Validator was not persisted before non-model verification")
	}
	coderEvidence, _ := core.CanonicalSHA256(coder.Result)
	validator, err := prepared.Records.ReadPacket(prepared.Records.PacketAudits[1].PacketHash)
	if err != nil || validator.Readback().PreviousEvidenceHash != coderEvidence {
		t.Fatalf("Validator does not use actual Coder evidence: %v", err)
	}
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID, request.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	review, _ := db.LoadWorkspace()
	if len(review.Records.Packets) != 3 || len(review.Records.PacketAudits) != 4 {
		t.Fatalf("sequential packets/evidence missing: %+v", review.Records.PacketAudits)
	}
	validation, reviewer := review.Records.PacketAudits[2], review.Records.PacketAudits[3]
	packet, err := review.Records.ReadPacket(reviewer.PacketHash)
	if err != nil || packet.Readback().PreviousEvidenceHash != validation.EvidenceHash {
		t.Fatalf("Reviewer does not use actual validation evidence: %v", err)
	}
	var report GateReport
	if err := json.Unmarshal([]byte(validation.Validation), &report); err != nil ||
		report.Version != GateReportVersion || report.Verdict != GateVerdictPass ||
		report.Hashes.Evidence != coderEvidence || report.StateChanged {
		t.Fatalf("not a real versioned gate report: %+v %v", report, err)
	}
	if err := CheckRolePacket(db, flow.DispatchIDs[1]); err != nil {
		t.Fatal(err)
	}
	if err := CheckReviewFlowConfiguration(db, flow.ID, resolved.Config, nil); err != nil {
		t.Fatal("derived Reviewer preflight rejected lifecycle configuration", err)
	}
	executeFlowDispatch(t, db, flow, "APPROVE", true, resolved)
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID, request.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	if flow.Status != "COMPLETED" {
		t.Fatalf("packet lifecycle did not complete: %+v", flow)
	}
	final, _ := db.LoadWorkspace()
	if !reflect.DeepEqual(before.Graph.Approvals, final.Graph.Approvals[:len(before.Graph.Approvals)]) {
		t.Fatal("packet lifecycle modified ApprovalBundles")
	}
	for _, artifact := range final.Records.Packets {
		if _, err := final.Records.ReadPacket(artifact.Hash); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLifecycleReviewerConfigurationPreflightFailsClosed(t *testing.T) {
	for _, mode := range []string{"missing", "configuration", "unknown-version", "tampered", "contract", "proposal", "bundle", "review-id"} {
		t.Run(mode, func(t *testing.T) {
			db, ledger, request, auth := packetFlowFixture(t)
			if mode != "missing" {
				authorizePacketFixture(t, db, request, auth)
			}
			flow, err := StartReviewFlow(db, ledger, request)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := db.LoadWorkspace()
			selected := packetConfigurationFixture(t).Config
			if mode == "configuration" {
				selected.Runtime.Account = "different-account"
			}
			if mode == "missing" || mode == "configuration" {
				if err := CheckReviewFlowConfiguration(db, flow.ID, selected, nil); err == nil {
					t.Fatal("missing lifecycle/configuration drift passed preflight")
				}
			} else {
				// Invalid persisted records are exercised on a copy; store validation
				// already prevents saving these malformed authorizations.
				state, _ := db.LoadWorkspace()
				switch mode {
				case "unknown-version":
					state.Records.PacketAuthorizations[0].Version = 99
				case "tampered":
					state.Records.PacketAuthorizations[0].Hash = strings.Repeat("a", 64)
				case "contract":
					state.Graph.Tasks[0].Objective += " changed"
				case "proposal":
					state.Records.PacketAuthorizations[0].ProposalHash = strings.Repeat("b", 64)
				case "bundle":
					state.Records.PacketAuthorizations[0].BundleHash = strings.Repeat("b", 64)
				}
				reviewID := flow.ID + "-review-0"
				if mode == "review-id" {
					reviewID = "another-flow-review-0"
				}
				if _, _, err := lifecycleReviewerConfiguration(&state, flow, reviewID, selected); err == nil {
					t.Fatal("invalid lifecycle delegation passed preflight")
				}
			}
			after, _ := db.LoadWorkspace()
			if !reflect.DeepEqual(before, after) {
				t.Fatal("rejected preflight changed Task, approvals, packets or execution")
			}
		})
	}
}

func TestPacketLifecycleAuthorizationFailClosed(t *testing.T) {
	db, _, request, auth := packetFlowFixture(t)
	before, _ := db.LoadWorkspace()
	for _, test := range []struct {
		name string
		edit func(*store.Workspace, *store.PacketAuthorization, *core.ApprovalConfiguration)
	}{
		{"token-basis", func(_ *store.Workspace, a *store.PacketAuthorization, _ *core.ApprovalConfiguration) {
			a.Hash = strings.Repeat("f", 64)
		}},
		{"state-drift", func(s *store.Workspace, _ *store.PacketAuthorization, _ *core.ApprovalConfiguration) {
			s.Graph.Tasks[0].Status = core.Draft
		}},
		{"contract-drift", func(s *store.Workspace, _ *store.PacketAuthorization, _ *core.ApprovalConfiguration) {
			s.Graph.Tasks[0].Objective += " changed"
		}},
		{"configuration-drift", func(_ *store.Workspace, _ *store.PacketAuthorization, c *core.ApprovalConfiguration) {
			*c = approvalTestConfiguration(t, "https://other.example.test")
		}},
		{"credential", func(_ *store.Workspace, a *store.PacketAuthorization, _ *core.ApprovalConfiguration) {
			a.Reason = "Authorization: Bearer private-test-value"
		}},
		{"unknown-version", func(_ *store.Workspace, a *store.PacketAuthorization, _ *core.ApprovalConfiguration) { a.Version = 99 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := db.UpdateWorkspace(func(s *store.Workspace) error {
				approved, configuration := auth, request.Configuration
				test.edit(s, &approved, &configuration)
				return AppendPacketAuthorization(s, approved, configuration)
			})
			if err == nil {
				t.Fatal("invalid authorization accepted")
			}
			after, _ := db.LoadWorkspace()
			if !reflect.DeepEqual(before, after) {
				t.Fatal("failed packet authorization wrote workspace state")
			}
		})
	}
}

func TestPacketLifecycleGitDriftCannotProduceReviewerEvidence(t *testing.T) {
	db, ledger, request, auth := packetFlowFixture(t)
	authorizePacketFixture(t, db, request, auth)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	executeFlowDispatch(t, db, flow, "", true)
	if err := preparePacketValidator(db, flow.ID, ""); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(flow.Contract.Worktree, "internal", "app.go"), []byte("changed after receipt"), 0600); err != nil {
		t.Fatal(err)
	}
	stopped, err := AdvanceReviewFlow(db, ledger, flow.ID, request.Configuration)
	if !errors.Is(err, ErrFlowStopped) || stopped.Status != "STOPPED" {
		t.Fatalf("Git drift did not stop validation: %+v %v", stopped, err)
	}
	state, err := db.LoadWorkspace()
	if err != nil || len(state.Records.Packets) != 2 || len(state.Graph.Dispatches) != 1 {
		t.Fatalf("failed validation created a future packet or Dispatch: %v", err)
	}
	for _, audit := range state.Records.PacketAudits {
		if audit.Validation != "" || audit.Role == "REVIEWER" {
			t.Fatal("failed validation fabricated successful evidence")
		}
	}
}

func TestPacketLifecycleMissingReviewerBlocksWorktreeAndRuntime(t *testing.T) {
	db, ledger, request, auth := packetFlowFixture(t)
	authorizePacketFixture(t, db, request, auth)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	executeFlowDispatch(t, db, flow, "", true)
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID, request.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	// Simulate an imported legacy workspace without the saved Reviewer artifact.
	state.Records.PacketAudits = state.Records.PacketAudits[:3]
	state.Records.Packets = state.Records.Packets[:2]
	imported, err := store.Open(filepath.Join(t.TempDir(), "imported.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer imported.Close()
	if _, err := imported.InitializeWorkspace(state); err != nil {
		t.Fatal(err)
	}
	reviewer := flow.DispatchIDs[1]
	if _, err := PrepareReview(imported, reviewer, flow.Contract.Worktree); err == nil {
		t.Fatal("missing packet permitted Reviewer worktree preparation")
	}
	called := false
	_, err = StepReviewFlow(t.Context(), imported, ledger, flow.ID,
		func(core.GraphDispatch) (runtime.Identity, error) {
			called = true
			return nil, errors.New("runtime must not be constructed")
		}, &strings.Builder{}, request.Configuration)
	if err == nil || called {
		t.Fatal("missing packet reached runtime construction")
	}
	if _, err := os.Stat(flow.ReviewRoot); !os.IsNotExist(err) {
		t.Fatalf("missing packet created a worktree: %v", err)
	}
}

func TestPacketLifecycleDoesNotDelegateRepairApproval(t *testing.T) {
	db, ledger, request, auth := packetFlowFixture(t)
	authorizePacketFixture(t, db, request, auth)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	executeFlowDispatch(t, db, flow, "", true)
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID, request.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	executeFlowDispatch(t, db, flow, "REQUEST_CHANGES", true)
	before, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	_, err = AdvanceReviewFlow(db, ledger, flow.ID, request.Configuration)
	if err == nil {
		t.Fatal("Reviewer lifecycle approval authorized an unrelated repair entry")
	}
	after, loadErr := db.LoadWorkspace()
	if loadErr != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("failed repair check changed packet or Task state: %v", loadErr)
	}
}

func TestPacketLifecycleMissingTamperedAndMismatchedArtifacts(t *testing.T) {
	db, ledger, request, auth := packetFlowFixture(t)
	authorizePacketFixture(t, db, request, auth)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		edit func(*store.Workspace)
	}{
		{"missing", func(s *store.Workspace) { s.Records.Packets = nil }},
		{"tampered", func(s *store.Workspace) {
			s.Records.Packets[0].Canonical = strings.Replace(s.Records.Packets[0].Canonical, `"role":"CODER"`, `"role":"REVIEWER"`, 1)
		}},
		{"unknown-schema", func(s *store.Workspace) {
			s.Records.Packets[0].Canonical = strings.Replace(s.Records.Packets[0].Canonical, `"packetVersion":1`, `"packetVersion":99`, 1)
		}},
		{"instruction-mismatch", func(s *store.Workspace) { s.Graph.Dispatches[0].InstructionHash = strings.Repeat("f", 64) }},
		{"missing-binding", func(s *store.Workspace) { s.Records.PacketAudits = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			state, _ := db.LoadWorkspace()
			test.edit(&state)
			if err := checkLifecyclePacket(&state, flow.DispatchIDs[0]); err == nil {
				t.Fatal("invalid packet was accepted before runtime entry")
			}
		})
	}
	executeFlowDispatch(t, db, flow, "", true)
	state, _ := db.LoadWorkspace()
	coder, _ := state.Graph.GraphDispatch(flow.DispatchIDs[0])
	if _, err := validatedPacketEvidence(&state, auth, coder); err == nil {
		t.Fatal("Reviewer accepted nonexistent validation stage")
	}
	for _, audit := range state.Records.PacketAudits {
		if audit.Role != "CODER" {
			t.Fatal("missing evidence generated future packets")
		}
	}
}

func TestPacketLifecycleManifestV1PreservesBareProposalReference(t *testing.T) {
	resolved := packetConfigurationFixture(t)
	approval := config.Approval{Kind: config.ApprovalKind, TaskID: "implementation",
		Reason: "Exact parent-only manifest configuration", Allowed: []config.Config{resolved.Config}}
	db, ledger, request := reviewFlowFixture(t, core.TaskDecision{
		ID:     core.ConfigurationApprovalID(resolved.Config.Execution.ApprovalRef, "implementation"),
		Source: "human:configuration", Text: string(mustFlowJSON(t, approval)),
	})
	request.Configuration, _ = core.SelectApprovalConfiguration(mustFlowJSON(t, resolved.Config), nil)
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	task, _ := state.Graph.GraphTask(request.TaskID)
	appendApprovalForTask(t, db, task, request.Configuration, "manifest-v1")
	state, err = db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	original := state.Graph.Approvals[0]
	if strings.HasPrefix(original.ProposalHash, "sha256:") {
		t.Fatal("fixture must retain the manifest's bare proposal reference")
	}
	auth, err := PreviewPacketAuthorization(state, request.TaskID, "human", "human:1",
		"enable sequential packet lifecycle", nil, request.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	authorizePacketFixture(t, db, request, auth)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	executeFlowDispatch(t, db, flow, "", true)
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID, request.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	review, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	derived := review.Graph.Approvals[len(review.Graph.Approvals)-1]
	if review.Graph.Approvals[0].BundleHash != original.BundleHash || derived.ProposalHash != original.ProposalHash {
		t.Fatal("v1 proposal identity was normalized or replaced")
	}
	executeFlowDispatch(t, db, flow, "APPROVE", true)
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID, request.Configuration)
	if err != nil || flow.Status != "COMPLETED" {
		t.Fatalf("v1 manifest lifecycle failed: %+v %v", flow, err)
	}
}
