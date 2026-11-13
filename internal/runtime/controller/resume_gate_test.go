package controller

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

func resumeGateFixture(t *testing.T, timeout ...int64) (*store.Store, string, ReviewFlowRequest, core.ReviewFlow) {
	return resumeGateFixtureWithPackets(t, true, timeout...)
}

func resumeGateFixtureWithPackets(t *testing.T, packets bool, timeout ...int64) (*store.Store, string, ReviewFlowRequest, core.ReviewFlow) {
	t.Helper()
	resolved := packetConfigurationFixture(t)
	proposal, err := core.CanonicalSHA256("synthetic-resume-proposal")
	if err != nil {
		t.Fatal(err)
	}
	proposal = "sha256:" + proposal
	approval := config.Approval{Kind: config.ApprovalKind, TaskID: "implementation",
		Reason: "synthetic exact config", Allowed: []config.Config{resolved.Config},
		AdditionalTaskIDs: []string{"flow-review-0", "flow-review-1"}}
	db, ledger, request := reviewFlowFixture(t,
		core.TaskDecision{ID: "intake-confirmation:implementation", Source: "human:synthetic",
			Text: `{"taskId":"implementation","expectedHash":"` + proposal + `"}`},
		core.TaskDecision{ID: core.ConfigurationApprovalID(resolved.Config.Execution.ApprovalRef, "implementation"),
			Source: "human:synthetic", Text: string(mustFlowJSON(t, approval))})
	request.Configuration, err = core.SelectApprovalConfiguration(mustFlowJSON(t, resolved.Config), nil)
	if err != nil {
		t.Fatal(err)
	}
	err = db.UpdateWorkspace(func(state *store.Workspace) error {
		task, _ := state.Graph.GraphTask(request.TaskID)
		bundle, err := core.ComposeReauthorizationApprovalBundleWithProposalHash("resume-approval", proposal, task,
			request.Configuration, "human", "human:synthetic", "approve synthetic resume", time.Now().UTC())
		if err != nil {
			return err
		}
		if err := state.Graph.AppendApproval(bundle); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if packets {
		err = db.UpdateWorkspace(func(state *store.Workspace) error {
			task, _ := state.Graph.GraphTask(request.TaskID)
			for _, bundle := range state.Graph.ApprovalsForTask(request.TaskID) {
				if err := bundle.Verify(task, request.Configuration); err != nil {
					t.Fatalf("synthetic approval fixture: %v", err)
				}
			}
			auth, err := PreviewPacketAuthorization(*state, request.TaskID, "human", "human:synthetic", "sequential packets", nil, request.Configuration)
			if err != nil {
				return err
			}
			return AppendPacketAuthorization(state, auth, request.Configuration)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	ready, _ := db.LoadWorkspace()
	report, err := ContractGateReport(ready.Graph, request.TaskID, request.Configuration)
	if err != nil || report.Verdict != GateVerdictPass {
		t.Fatalf("READY start gate changed: %+v %v", report, err)
	}
	if len(timeout) != 0 {
		request.TimeoutSeconds = timeout[0]
	}
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	return db, ledger, request, flow
}

func TestResumeContractGateReadOnlyRestartAndConcurrent(t *testing.T) {
	db, ledger, request, flow := resumeGateFixture(t)
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if start, err := ContractGateReport(state.Graph, flow.TaskID, request.Configuration); err != nil ||
		start.Verdict == GateVerdictPass || start.BlockingConditions[0].Code != "task-not-ready" {
		t.Fatal("ACTIVE was admitted by the new-start gate", start, err)
	}
	before := mustFlowJSON(t, state)
	database := filepath.Join(filepath.Dir(ledger), "workspace.db")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	storeBytes, err := os.ReadFile(database)
	if err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(flow.Contract.Worktree, ".git", "index")
	indexBefore, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	readonly, err := store.OpenReadOnly(database)
	if err != nil {
		t.Fatal(err)
	}
	defer readonly.Close()
	restarted, err := readonly.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			report, err := ResumeContractGateReport(t.Context(), restarted, flow.ID, request.Configuration)
			if err != nil || report.Verdict != GateVerdictPass || report.StateChanged ||
				report.Hashes.Bundle == "" || report.NextAuthorizedTransition == "" {
				t.Errorf("valid saved workflow rejected: %+v %v", report, err)
			}
		}()
	}
	wait.Wait()
	after, err := readonly.LoadWorkspace()
	if err != nil || !bytes.Equal(before, mustFlowJSON(t, after)) {
		t.Fatal("gate changed persistent records", err)
	}
	afterBytes, err := os.ReadFile(database)
	if err != nil || !bytes.Equal(storeBytes, afterBytes) {
		t.Fatal("gate changed database bytes", err)
	}
	indexAfter, err := os.ReadFile(index)
	if err != nil || !bytes.Equal(indexBefore, indexAfter) {
		t.Fatal("read-only gate refreshed the Git index", err)
	}
}

func TestResumeRunsSavedDispatchThroughSequentialHandoff(t *testing.T) {
	for _, packets := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary-approval", true: "packet-lifecycle"}[packets], func(t *testing.T) {
			testResumeRunsSavedDispatchThroughSequentialHandoff(t, packets)
		})
	}
}

func testResumeRunsSavedDispatchThroughSequentialHandoff(t *testing.T, packets bool) {
	db, ledger, request, flow := resumeGateFixtureWithPackets(t, packets)
	before, _ := db.LoadWorkspace()
	original, _ := before.Graph.GraphDispatch(flow.DispatchIDs[0])
	blocked, err := RunReviewFlow(t.Context(), db, ledger, flow.ID, func(core.GraphDispatch) (runtime.Identity, error) {
		return nil, errors.New("worker executable temporarily unavailable")
	}, io.Discard, request.Configuration)
	afterPreparation, _ := db.LoadWorkspace()
	if err == nil || blocked.Status != "RUNNING" || blocked.RunnerID != "" || blocked.HandoffHeld() ||
		!reflect.DeepEqual(before.Graph, afterPreparation.Graph) ||
		len(afterPreparation.Records.Budgets) != len(before.Records.Budgets)+1 {
		t.Fatal("preparation failure invalidated issued authority or execution state", blocked, err)
	}
	resolved := packetConfigurationFixture(t)
	var roles []string
	factory := func(dispatch core.GraphDispatch) (runtime.Identity, error) {
		if err := BindConfiguration(db, dispatch.ID, resolved); err != nil {
			return nil, err
		}
		roles = append(roles, dispatch.Recipient)
		worker := &scriptedRuntime{fakeRuntime: fakeRuntime{
			role: dispatch.Recipient, hash: "config:" + dispatch.ID,
			worktree: dispatch.Instruction.Worktree, sessionID: "synthetic:" + dispatch.ID,
		}, dispatch: dispatch, events: make(chan runtime.Event, 4)}
		worker.script = func() error {
			result := core.DispatchResult{
				DispatchID: dispatch.ID, TaskID: dispatch.TaskID, Recipient: dispatch.Recipient,
				Repository: dispatch.Repository, Status: core.DispatchSucceeded,
				StartedSHA: dispatch.ExpectedStartSHA(), OutputSHA: dispatch.ExpectedStartSHA(),
				ChangedFiles: []string{}, Evidence: []string{"test-report"}, NextTransition: core.WaitingForReview,
			}
			if dispatch.Recipient == "CODER" {
				root := dispatch.Instruction.Worktree
				if err := os.MkdirAll(filepath.Join(root, "internal"), 0755); err != nil {
					return err
				}
				if err := os.WriteFile(filepath.Join(root, "internal/app.go"), []byte("package app\n"), 0600); err != nil {
					return err
				}
				gitTest(t, root, "add", "internal/app.go")
				gitTest(t, root, "commit", "-m", "feat(app): satisfy synthetic acceptance", "-m", "intent(app): validate saved workflow continuation")
				result.OutputSHA = gitTest(t, root, "rev-parse", "HEAD")
				result.ChangedFiles = []string{"internal/app.go"}
			} else {
				changed := false
				result.WorktreeChanged, result.ReviewVerdict = &changed, "APPROVE"
				result.Findings = []core.DispatchFinding{}
				result.Acceptance = []core.DispatchAcceptance{{Criterion: "application tested", Status: "PASS", Evidence: []string{"test-report"}}}
			}
			worker.events <- runtime.Event{Type: runtime.EventUsage, Payload: `{"model":"synthetic","totalNanoAIU":1,"aiCreditsStatus":"complete"}`}
			worker.events <- runtime.Event{Type: runtime.EventOutput, Payload: string(mustFlowJSON(t, result))}
			worker.events <- runtime.Event{Type: runtime.EventTurnFinished}
			return nil
		}
		return &configuredScriptedRuntime{scriptedRuntime: worker, selection: mustFlowJSON(t, resolved.Config)}, nil
	}
	complete, err := ResumeReviewFlow(t.Context(), db, ledger, flow.ID, factory, io.Discard, request.Configuration)
	if err != nil || complete.Status != "COMPLETED" || !reflect.DeepEqual(roles, []string{"CODER", "REVIEWER"}) {
		t.Fatal("saved Dispatch did not continue through real non-model validation and Reviewer", complete, roles, err)
	}
	after, _ := db.LoadWorkspace()
	saved, _ := after.Graph.GraphDispatch(original.ID)
	if saved.InstructionHash != original.InstructionHash || len(after.Graph.Dispatches) != 2 ||
		complete.StartedAt != flow.StartedAt || complete.Deadline != flow.Deadline || complete.Budget != flow.Budget ||
		!reflect.DeepEqual(before.Graph.Approvals, after.Graph.Approvals[:len(before.Graph.Approvals)]) ||
		packets && len(after.Records.Packets) != 3 || !packets && len(after.Records.Packets) != 0 {
		t.Fatal("resume recreated a Dispatch, changed limits or original approval, or lost sequential packets")
	}
}

func TestResumeAtEveryIssuedRoleBoundary(t *testing.T) {
	for _, packets := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary-approval", true: "packet-lifecycle"}[packets], func(t *testing.T) {
			db, ledger, request, flow := resumeGateFixtureWithPackets(t, packets)
			selected := packetConfigurationFixture(t)
			for i, verdict := range []string{"", "REQUEST_CHANGES", "", "APPROVE"} {
				state, err := db.LoadWorkspace()
				if err != nil {
					t.Fatal(err)
				}
				before := mustFlowJSON(t, state)
				report, err := ResumeContractGateReport(t.Context(), state, flow.ID, request.Configuration)
				if err != nil || report.Verdict != GateVerdictPass ||
					!bytes.Equal(before, mustFlowJSON(t, state)) {
					t.Fatalf("issued boundary %d rejected or changed: %+v %v", i, report, err)
				}
				executeFlowDispatch(t, db, flow, verdict, true, selected)
				flow, err = AdvanceReviewFlow(db, ledger, flow.ID, request.Configuration)
				if err != nil {
					t.Fatal(err)
				}
			}
			if flow.Status != "COMPLETED" || flow.Repairs != 1 {
				t.Fatal("saved role approvals did not complete repair and re-review", flow)
			}
		})
	}
}

func TestResumeArtifactReviewerDoesNotReplayInputValidation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "unprepared", true: "prepared"}[prepared], func(t *testing.T) {
			counter := filepath.Join(t.TempDir(), "validation-count")
			db, ledger, request, selected := artifactFixture(t, []string{"printf x >> " + counter}, false)
			flow, err := StartReviewFlow(db, ledger, request)
			if err != nil {
				t.Fatal(err)
			}
			flow, err = ValidateReviewFlowInput(t.Context(), db, ledger, flow.ID, request.Configuration)
			if err != nil {
				t.Fatal(err)
			}
			if prepared {
				if _, err := PrepareReview(db, flow.LastDispatchID(), flow.Contract.Worktree); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := db.LoadWorkspace()
			report, err := ResumeContractGateReport(t.Context(), before, flow.ID, request.Configuration)
			if err != nil || report.Verdict != GateVerdictPass {
				t.Fatalf("artifact Reviewer rejected: %+v %v", report, err)
			}
			after, _ := db.LoadWorkspace()
			if !reflect.DeepEqual(before, after) {
				t.Fatal("preview changed saved artifact validation")
			}
			if data, err := os.ReadFile(counter); err != nil || string(data) != "x" {
				t.Fatal("preview replayed input validation", string(data), err)
			}
			factory := func(d core.GraphDispatch) (runtime.Identity, error) {
				if err := BindConfiguration(db, d.ID, selected); err != nil {
					return nil, err
				}
				w := &scriptedRuntime{fakeRuntime: fakeRuntime{
					role: d.Recipient, hash: "resume-artifact", worktree: d.Instruction.Worktree, bound: true,
				}, events: make(chan runtime.Event, 3)}
				w.script = func() error {
					unchanged := false
					answer := core.DispatchResult{
						Status: core.DispatchSucceeded, StartedSHA: d.ExpectedStartSHA(), OutputSHA: d.ExpectedStartSHA(),
						Evidence: []string{"test-report"}, ChangedFiles: []string{}, WorktreeChanged: &unchanged,
						ReviewVerdict: "APPROVE", Findings: []core.DispatchFinding{}, NextTransition: core.Approved,
						Acceptance: []core.DispatchAcceptance{{Criterion: "application tested", Status: "PASS", Evidence: []string{"test-report"}}},
					}
					w.events <- runtime.Event{Type: runtime.EventUsage, Payload: `{"model":"synthetic","totalNanoAIU":1,"aiCreditsStatus":"complete"}`}
					w.events <- runtime.Event{Type: runtime.EventOutput, Payload: string(mustFlowJSON(t, answer))}
					w.events <- runtime.Event{Type: runtime.EventTurnFinished}
					return nil
				}
				return &configuredScriptedRuntime{scriptedRuntime: w, selection: mustFlowJSON(t, selected.Config)}, nil
			}
			complete, err := ResumeReviewFlow(t.Context(), db, ledger, flow.ID, factory, io.Discard, request.Configuration)
			if err != nil || complete.Status != "COMPLETED" || len(complete.DispatchIDs) != 1 {
				t.Fatal("saved artifact did not complete without a Coder", complete, err)
			}
			if data, err := os.ReadFile(counter); err != nil || string(data) != "xx" {
				t.Fatal("expected input validation once and result acceptance once", string(data), err)
			}
		})
	}
}

func TestResumePreparedReviewerPreviewDoesNotExecuteGitFilters(t *testing.T) {
	db, ledger, request, _ := artifactFixture(t, []string{"true"}, false)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	flow, err = ValidateReviewFlowInput(t.Context(), db, ledger, flow.ID, request.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err := PrepareReview(db, flow.LastDispatchID(), flow.Contract.Worktree)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "filter-ran")
	gitTest(t, dispatch.Instruction.Worktree, "config", "filter.resume.clean", "printf ran > "+marker+"; cat")
	attributes := gitTest(t, dispatch.Instruction.Worktree, "rev-parse", "--git-path", "info/attributes")
	if err := os.WriteFile(attributes, []byte("README.md filter=resume\n"), 0600); err != nil {
		t.Fatal(err)
	}
	index := gitTest(t, dispatch.Instruction.Worktree, "rev-parse", "--git-path", "index")
	indexBefore, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := db.LoadWorkspace()
	report, err := ResumeContractGateReport(t.Context(), before, flow.ID, request.Configuration)
	if err != nil || report.Verdict != GateVerdictFail ||
		report.BlockingConditions[0].Code != "resume-worktree-mismatch" {
		t.Fatal("filter-dependent observation was not rejected", report, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("preview executed a clean filter", err)
	}
	indexAfter, err := os.ReadFile(index)
	after, loadErr := db.LoadWorkspace()
	if err != nil || loadErr != nil || !bytes.Equal(indexBefore, indexAfter) || !reflect.DeepEqual(before, after) {
		t.Fatal("preview modified its index or saved state", err, loadErr)
	}
}

func TestResumeReusesReleasedUnstartedSession(t *testing.T) {
	db, _, request, flow := resumeGateFixtureWithPackets(t, false)
	selected := packetConfigurationFixture(t)
	dispatchID := flow.LastDispatchID()
	if err := BindConfiguration(db, dispatchID, selected); err != nil {
		t.Fatal(err)
	}
	worker := &configuredRuntime{fakeRuntime: &fakeRuntime{
		role: "CODER", hash: "released-session", worktree: flow.Contract.Worktree,
	}, selection: mustFlowJSON(t, selected.Config)}
	control, err := New(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Start(t.Context(), dispatchID); err != nil {
		t.Fatal(err)
	}
	if _, err := ChangeHandoff(db, handoffRequest("hold", "", "HOLD")); err != nil {
		t.Fatal(err)
	}
	if _, err := ChangeHandoff(db, handoffRequest("release", "hold", "RELEASE")); err != nil {
		t.Fatal(err)
	}
	state, _ := db.LoadWorkspace()
	report, err := ResumeContractGateReport(t.Context(), state, flow.ID, request.Configuration)
	if err != nil || report.Verdict != GateVerdictPass {
		t.Fatal("released session was mistaken for a previously sent execution", report, err)
	}
	if _, err := control.Start(t.Context(), dispatchID); err != nil || worker.starts != 1 || worker.resumes != 1 {
		t.Fatal("continuation replaced the original session instead of reattaching it", err)
	}
	if err := control.Send(t.Context(), dispatchID, "execute once"); err != nil {
		t.Fatal(err)
	}
	state, _ = db.LoadWorkspace()
	report, err = ResumeContractGateReport(t.Context(), state, flow.ID, request.Configuration)
	if err != nil || report.Verdict == GateVerdictPass {
		t.Fatal("already-sent execution was admitted as an unstarted session", report, err)
	}
}

func TestResumeContractGateRejectsInconsistentSavedState(t *testing.T) {
	tests := []struct {
		name string
		code string
		edit func(*store.Workspace)
	}{
		{"no-flow", "resume-flow-not-running", func(s *store.Workspace) { s.Graph.ReviewFlows = nil }},
		{"task-state", "resume-task-not-active", func(s *store.Workspace) { s.Graph.Tasks[0].Status = core.Ready }},
		{"runner", "resume-runner-claimed", func(s *store.Workspace) { s.Graph.ReviewFlows[0].RunnerID = "saved-runner" }},
		{"task-binding", "resume-binding-mismatch", func(s *store.Workspace) { s.Graph.Dispatches[0].TaskID = "unbound" }},
		{"acknowledged", "resume-dispatch-not-untouched", func(s *store.Workspace) { s.Graph.Dispatches[0].Acknowledged = true }},
		{"execution-started", "resume-dispatch-not-untouched", func(s *store.Workspace) { s.Graph.Dispatches[0].ExecutionStarted = true }},
		{"runtime-session", "resume-dispatch-not-untouched", func(s *store.Workspace) { s.Graph.Dispatches[0].RuntimeSessionID = "existing-session" }},
		{"contract", "resume-contract-mismatch", func(s *store.Workspace) { s.Graph.Tasks[0].Objective = "drift" }},
		{"instruction-hash", "resume-state-invalid", func(s *store.Workspace) { s.Graph.Dispatches[0].InstructionHash = strings.Repeat("0", 64) }},
		{"authorization", "resume-budget-binding", func(s *store.Workspace) { s.Graph.ReviewFlows[0].Authorization.Source = "changed-source" }},
		{"bundle", "resume-authorization-mismatch", func(s *store.Workspace) { s.Graph.Approvals = nil }},
		{"packet-missing", "resume-packet-invalid", func(s *store.Workspace) { s.Records.Packets = nil }},
		{"packet-binding", "resume-packet-invalid", func(s *store.Workspace) { s.Records.PacketAudits[0].PacketHash = strings.Repeat("f", 64) }},
		{"packet-tamper", "resume-packet-invalid", func(s *store.Workspace) { s.Records.Packets[0].Canonical = `{}` }},
		{"packet-version", "resume-packet-invalid", func(s *store.Workspace) {
			s.Records.Packets[0].Canonical = strings.Replace(s.Records.Packets[0].Canonical, `"packetVersion":1`, `"packetVersion":99`, 1)
		}},
		{"deadline-drift", "resume-budget-binding", func(s *store.Workspace) {
			s.Graph.ReviewFlows[0].Deadline = s.Graph.ReviewFlows[0].Deadline.Add(time.Second)
		}},
		{"budget-drift", "resume-budget-binding", func(s *store.Workspace) { s.Graph.ReviewFlows[0].Budget.MaxNanoAIU++ }},
		{"counter-drift", "resume-state-invalid", func(s *store.Workspace) { s.Graph.ReviewFlows[0].Repairs++ }},
		{"missing-audit", "resume-budget-binding", func(s *store.Workspace) { s.Records.Budgets = nil }},
		{"counter-audit", "resume-budget-binding", func(s *store.Workspace) {
			audit := &s.Records.Budgets[0]
			audit.Payload = strings.Replace(audit.Payload, `"dispatches":1`, `"dispatches":2`, 1)
			audit.Hash = core.SHA256Hex([]byte(audit.Payload))
		}},
		{"binding-version", "resume-budget-binding", func(s *store.Workspace) {
			audit := &s.Records.Budgets[0]
			audit.Payload = strings.Replace(audit.Payload, `"version":1`, `"version":99`, 1)
			audit.Hash = core.SHA256Hex([]byte(audit.Payload))
		}},
		{"legacy-audit", "resume-budget-binding", func(s *store.Workspace) {
			var report TaskBudgetReport
			audit := &s.Records.Budgets[0]
			if err := json.Unmarshal([]byte(audit.Payload), &report); err != nil {
				panic(err)
			}
			report.ResumeBasis = nil
			data, err := json.Marshal(report)
			if err != nil {
				panic(err)
			}
			audit.Payload, audit.Hash = string(data), core.SHA256Hex(data)
		}},
		{"other-operation", "resume-operation-conflict", func(s *store.Workspace) {
			s.Records.Operation = &store.Operation{ID: "other-operation", State: "RUNNING"}
		}},
		{"nonterminal-flow", "resume-state-invalid", func(s *store.Workspace) {
			other := s.Graph.ReviewFlows[0]
			other.ID, other.TaskID = "other-flow", "other-task"
			other.Contract.ID = other.TaskID
			task := s.Graph.Tasks[0]
			task.ID = other.TaskID
			s.Graph.Tasks = append(s.Graph.Tasks, task)
			dispatch := s.Graph.Dispatches[0]
			dispatch.ID, dispatch.TaskID = "other-dispatch", other.TaskID
			other.DispatchIDs = []string{dispatch.ID}
			s.Graph.ReviewFlows = append(s.Graph.ReviewFlows, other)
			s.Graph.Dispatches = append(s.Graph.Dispatches, dispatch)
		}},
	}
	db, _, request, flow := resumeGateFixture(t)
	original, _ := db.LoadWorkspace()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var state store.Workspace
			if err := json.Unmarshal(mustFlowJSON(t, original), &state); err != nil {
				t.Fatal(err)
			}
			test.edit(&state)
			before := mustFlowJSON(t, state)
			report, err := ResumeContractGateReport(t.Context(), state, flow.ID, request.Configuration)
			if err != nil || report.Verdict == GateVerdictPass || len(report.BlockingConditions) == 0 ||
				report.BlockingConditions[0].Code != test.code {
				t.Fatalf("rejection: %+v %v; want %s", report, err, test.code)
			}
			if report.StateChanged || !bytes.Equal(before, mustFlowJSON(t, state)) {
				t.Fatal("rejected gate changed its input")
			}
		})
	}
	after, _ := db.LoadWorkspace()
	if !reflect.DeepEqual(original, after) {
		t.Fatal("negative tests changed the store")
	}
}

func TestResumeContractGateExpiredAndAcknowledgedAreByteStable(t *testing.T) {
	for _, kind := range []string{"expired", "acknowledged"} {
		t.Run(kind, func(t *testing.T) {
			db, ledger, request, flow := resumeGateFixture(t, 1)
			if kind == "acknowledged" {
				if err := db.UpdateWorkspace(func(s *store.Workspace) error {
					return s.Graph.TransitionDispatch(flow.DispatchIDs[0], core.DispatchAcknowledged, "controller", "acknowledged", true, false, nil)
				}); err != nil {
					t.Fatal(err)
				}
			} else {
				time.Sleep(time.Until(flow.Deadline) + time.Millisecond)
			}
			state, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			before := mustFlowJSON(t, state)
			report, err := ResumeContractGateReport(t.Context(), state, flow.ID, request.Configuration)
			if err != nil || report.Verdict != GateVerdictFail {
				t.Fatal("unsafe resume was accepted", report, err)
			}
			calls := 0
			if _, err := ResumeReviewFlow(t.Context(), db, ledger, flow.ID, func(core.GraphDispatch) (runtime.Identity, error) {
				calls++
				return nil, nil
			}, io.Discard, request.Configuration); err == nil || calls != 0 {
				t.Fatal("rejected resume started a runtime", err, calls)
			}
			after, _ := db.LoadWorkspace()
			if !bytes.Equal(before, mustFlowJSON(t, after)) {
				t.Fatal("rejected runner claim changed records")
			}
			database := filepath.Join(filepath.Dir(ledger), "workspace.db")
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			saved, err := os.ReadFile(database)
			if err != nil {
				t.Fatal(err)
			}
			readonly, err := store.OpenReadOnly(database)
			if err != nil {
				t.Fatal(err)
			}
			restarted, err := readonly.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			repeat, err := ResumeContractGateReport(t.Context(), restarted, flow.ID, request.Configuration)
			if err != nil || repeat.BlockingConditions[0].Code != report.BlockingConditions[0].Code {
				t.Fatal("restart changed refusal", repeat, err)
			}
			if err := readonly.Close(); err != nil {
				t.Fatal(err)
			}
			result, err := os.ReadFile(database)
			if err != nil || !bytes.Equal(saved, result) {
				t.Fatal("rejection changed SQLite bytes", err)
			}
		})
	}
}

func TestResumeContractGateRejectsGitDriftWithoutWrites(t *testing.T) {
	for _, kind := range []string{"branch", "head", "dirty", "mode", "filter", "assume-unchanged", "skip-worktree", "untracked", "endpoint", "baseline"} {
		t.Run(kind, func(t *testing.T) {
			db, _, request, flow := resumeGateFixture(t)
			root := flow.Contract.Worktree
			switch kind {
			case "branch":
				gitTest(t, root, "branch", "-m", "drift")
			case "head":
				gitTest(t, root, "commit", "--allow-empty", "-m", "drift")
			case "dirty":
				if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "assume-unchanged", "skip-worktree":
				gitTest(t, root, "update-index", "--"+kind, "README.md")
				if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "mode":
				gitTest(t, root, "config", "core.filemode", "false")
				if err := os.Chmod(filepath.Join(root, "README.md"), 0755); err != nil {
					t.Fatal(err)
				}
			case "filter":
				gitTest(t, root, "config", "filter.synthetic.clean", "printf ran > .git/resume-filter-ran; cat")
				if err := os.WriteFile(filepath.Join(root, ".git/info/attributes"), []byte("README.md filter=synthetic\n"), 0600); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(filepath.Join(root, "README.md"))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "README.md"), data, 0600); err != nil {
					t.Fatal(err)
				}
			case "untracked":
				if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte("new"), 0600); err != nil {
					t.Fatal(err)
				}
			case "endpoint":
				gitTest(t, root, "remote", "set-url", "origin", "https://other.example/owner/repo.git")
			case "baseline":
				gitTest(t, root, "replace", flow.Contract.BaselineSHA, flow.Contract.BaselineSHA)
			}
			state, _ := db.LoadWorkspace()
			before := mustFlowJSON(t, state)
			indexPath := filepath.Join(root, ".git", "index")
			indexBefore, err := os.ReadFile(indexPath)
			if err != nil {
				t.Fatal(err)
			}
			report, err := ResumeContractGateReport(t.Context(), state, flow.ID, request.Configuration)
			if _, err := os.Stat(filepath.Join(root, ".git/resume-filter-ran")); !os.IsNotExist(err) {
				t.Fatal("read-only observation invoked a Git clean filter", err)
			}
			if kind == "baseline" {
				// Replacement objects do not affect read-only identity checks.
				if err != nil || report.Verdict != GateVerdictPass {
					t.Fatal(report, err)
				}
			} else if err != nil || report.Verdict == GateVerdictPass {
				t.Fatal("Git drift was accepted", report, err)
			}
			after, _ := db.LoadWorkspace()
			if !bytes.Equal(before, mustFlowJSON(t, after)) {
				t.Fatal("Git rejection changed records")
			}
			indexAfter, err := os.ReadFile(indexPath)
			if err != nil || !bytes.Equal(indexBefore, indexAfter) {
				t.Fatal("Git observation changed index bytes", err)
			}
		})
	}
}
