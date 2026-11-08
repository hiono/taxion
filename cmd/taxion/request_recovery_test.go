package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/store"
)

func TestRequestRecoveryLegacyCancelPreservesCheckpointUsageAndReleasesStore(t *testing.T) {
	_, args := requestFixture(t)
	original := "retain the stopped planning attempt"
	var initialOutput bytes.Buffer
	err := requestCommand(append(args, original), strings.NewReader("/plan\n"), &initialOutput,
		func(context.Context, *store.Store, config.Config, core.GraphTask, string, float64, string, string) (controller.QuestionResult, error) {
			return controller.QuestionResult{}, errors.New("original legacy caller interrupted after the native answer")
		}, nil)
	if err == nil {
		t.Fatal("fixture did not preserve the interrupted request")
	}
	db, before, err := loadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	staleLease, err := db.LockOperation()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := staleLease.Claim(store.Operation{ID: before.Records.Operation.ID, Owner: "stopped-original-owner"}, true); err != nil {
		t.Fatal(err)
	}
	if err := staleLease.Close(); err != nil {
		t.Fatal(err)
	}
	before, err = db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	operation := before.Records.Operation
	var checkpoint requestCheckpoint
	if err := json.Unmarshal(operation.Checkpoint, &checkpoint); err != nil {
		t.Fatal(err)
	}
	answer := requestFixtureAnswer([]string{"Which exact checks?"})
	answerBytes, err := json.Marshal(answer)
	if err != nil {
		t.Fatal(err)
	}
	question, _ := json.Marshal(map[string]any{"conversation": []map[string]string{{"role": "human", "text": original}}})
	input, _ := json.Marshal(map[string]any{
		"question": string(question), "context": map[string]any{"planner": map[string]any{"task": checkpoint.Task}},
	})
	now := time.Now().UTC()
	inputTokens, outputTokens := int64(21), int64(8)
	turn := runtime.SavedTurn{
		SessionID: "saved-native", Input: string(input), InputHash: core.SHA256Hex(input),
		MessageID: "original-input", TurnID: "original-turn", Payload: string(answerBytes),
		SubmittedAt: now.Add(-2 * time.Second), FinishedAt: now.Add(-time.Second),
		HistoryHash: "sha256:" + strings.Repeat("b", 64),
		Events:      []runtime.Event{{Type: runtime.EventOutput, Payload: string(answerBytes)}},
		UsageTotals: []core.DispatchUsage{{
			Model: "model", InputTokens: &inputTokens, OutputTokens: &outputTokens,
		}},
	}
	oldObserver := observeSavedPlannerTurn
	observations := 0
	t.Cleanup(func() { observeSavedPlannerTurn = oldObserver })
	observeSavedPlannerTurn = func(binding plannerAttemptEvidence, cli, base string, confirmed bool) (runtime.SavedTurn, error) {
		observations++
		if binding.SessionID != turn.SessionID || binding.Worktree != checkpoint.Task.Worktree || !confirmed {
			t.Fatal("native observation lost its exact saved binding")
		}
		return turn, nil
	}
	recoveryArgs := []string{"--id", operation.ID, "--actor", "human", "--source", "actual-stop-approval",
		"--reason", "reconcile and release only the stopped request", "--evidence", "specific-original-process-tree-stopped",
		"--session", turn.SessionID, "--input-hash", turn.InputHash, "--associate-legacy", "--confirm-stopped",
		"--accept-uncertainty", "--resolution", "cancel"}
	var planningSequence uint64
	for _, record := range before.Records.Requests {
		if record.RequestID == operation.ID && record.Kind == "planning" {
			planningSequence++
		}
	}
	legacyPayload := `{"legacyObservation":true}`
	legacyRecords := []store.RequestRecord{
		{RequestID: operation.ID, Kind: "planning", Sequence: planningSequence + 1, At: now,
			Payload: legacyPayload, Hash: core.SHA256Hex([]byte(legacyPayload))},
		{RequestID: operation.ID, Kind: "planning", Sequence: planningSequence + 2, At: now.Add(-time.Second),
			Payload: legacyPayload, Hash: core.SHA256Hex([]byte(legacyPayload))},
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		state.Records.Requests = append(append([]store.RequestRecord{}, before.Records.Requests...), legacyRecords...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := requestRecoveryCommand(append(recoveryArgs, "--preview"), &bytes.Buffer{}); err == nil || observations != 0 {
		t.Fatal("legacy association bypassed ambiguous receipt ordering", err, observations)
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		state.Records.Requests = before.Records.Requests
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	originalEvents := turn.Events
	for _, activity := range []string{runtime.EventPermission, runtime.EventCompaction, "unknown-advisory-activity"} {
		turn.Events = append(append([]runtime.Event{}, originalEvents...), runtime.Event{Type: activity})
		if err := requestRecoveryCommand(append(recoveryArgs, "--preview"), &bytes.Buffer{}); err == nil {
			t.Fatal("public recovery accepted prohibited saved advisory activity", activity)
		}
	}
	turn.Events = originalEvents
	var preview bytes.Buffer
	if err := requestRecoveryCommand(append(recoveryArgs, "--preview"), &preview); err != nil {
		t.Fatal(err)
	}
	var report requestRecoveryPreview
	if err := json.Unmarshal(preview.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.UsageStatus != "unknown" {
		t.Fatal("incomplete native usage aggregate was treated as complete")
	}
	unchanged, err := db.LoadWorkspace()
	if err != nil || !reflect.DeepEqual(before, unchanged) {
		t.Fatal("read-only legacy preview changed saved state", err)
	}
	if err := requestRecoveryCommand(append(recoveryArgs, "--apply", "--expected-hash", "sha256:wrong"), &bytes.Buffer{}); err == nil {
		t.Fatal("stale preview authority was accepted")
	}
	applyArgs := append(recoveryArgs, "--apply", "--expected-hash", report.Hash)
	if err := requestRecoveryCommand(applyArgs, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	after, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if after.Records.Operation.State != "CANCELLED" || after.Records.Operation.Owner != "" ||
		after.Records.Operation.Deadline != operation.Deadline ||
		after.Records.Operation.MaxNanoAIU != operation.MaxNanoAIU ||
		after.Records.Operation.PlanningNanoAIU < operation.PlanningNanoAIU ||
		after.Records.Operation.PlanningUsageState != "unknown" || !reflect.DeepEqual(before.Graph, after.Graph) {
		t.Fatal("reconciliation reset resources, changed the product graph or retained the admission hold")
	}
	if _, ok, err := priorRequestRecovery(after, operation.ID, report.Hash, requestRecoveryInput{
		OperationID: operation.ID, Actor: "human", Source: "actual-stop-approval",
		Reason: "reconcile and release only the stopped request", Evidence: []string{"specific-original-process-tree-stopped"},
		SessionID: turn.SessionID, InputHash: turn.InputHash, AssociateLegacy: true,
		StoppedConfirmed: true, AcceptUncertainty: true, Resolution: "cancel",
	}); err != nil || !ok {
		t.Fatal("reconciliation did not retain its exact append-only authority", err)
	}
	if err := requestRecoveryCommand(applyArgs, &bytes.Buffer{}); err != nil {
		t.Fatal("exact completed apply was not idempotent", err)
	}
	repeated, err := db.LoadWorkspace()
	if err != nil || !reflect.DeepEqual(after, repeated) {
		t.Fatal("idempotent apply changed checkpoint or cumulative usage", err)
	}
	reachedExecution := false
	notStarted := errors.New("fixture deliberately does not execute the issued model")
	err = requestCommand(append(args, "start a separately approved follow-on"), strings.NewReader("/plan\n/approve\n"),
		io.Discard, fixturePlan(t, requestFixtureAnswer([]string{})),
		func(context.Context, []string, io.Writer) error {
			current, err := db.LoadWorkspace()
			if err != nil {
				return err
			}
			reachedExecution = len(current.Graph.ReviewFlows) == 1 &&
				current.Graph.ReviewFlows[0].Status == "RUNNING" && len(current.Graph.Dispatches) == 1 &&
				current.Graph.Dispatches[0].Status == "ISSUED"
			return notStarted
		})
	if !reachedExecution || !errors.Is(err, notStarted) {
		t.Fatal("public same-store request could not pass approval and Flow start after reconciliation", err)
	}
}

func TestRequestRecoveryCancelRetainsPerCallUsageWithIncompleteNativeTotals(t *testing.T) {
	for _, mode := range []string{"with-terminal", "without-terminal"} {
		for _, partial := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/partial-calls-%t", mode, partial), func(t *testing.T) {
				testRequestRecoveryCancelUsage(t, mode == "with-terminal", partial)
			})
		}
	}
}

func testRequestRecoveryCancelUsage(t *testing.T, terminalPresent, partialCalls bool) {
	t.Helper()
	_, args := requestFixture(t)
	var initialOutput bytes.Buffer
	if err := requestCommand(append(args, "preserve a stopped request"), strings.NewReader("/plan\n"),
		&initialOutput, func(context.Context, *store.Store, config.Config, core.GraphTask, string, float64, string, string) (controller.QuestionResult, error) {
			return controller.QuestionResult{}, errors.New("stop after the Planner send")
		}, nil); err == nil {
		t.Fatal("fixture did not preserve the interrupted request")
	}
	db, before, err := loadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	lease, err := db.LockOperation()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lease.Claim(store.Operation{ID: before.Records.Operation.ID, Owner: "stopped-owner"}, true); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	before, err = db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	operation := *before.Records.Operation
	var checkpoint requestCheckpoint
	if err := json.Unmarshal(operation.Checkpoint, &checkpoint); err != nil {
		t.Fatal(err)
	}
	task, ok := before.Graph.GraphTask(checkpoint.Task.ID)
	if !ok {
		t.Fatal("planning task is missing")
	}
	taskHash, err := core.CanonicalSHA256(task)
	if err != nil {
		t.Fatal(err)
	}
	selectionHash, err := core.CanonicalSHA256(checkpoint.Selection)
	if err != nil {
		t.Fatal(err)
	}
	planner, _, err := observeRecoveredAdvice(db, checkpoint, operation.Kind, plannerAttemptEvidence{})
	if err != nil {
		t.Fatal(err)
	}
	sourceHash, err := controller.PlannerQuestionSourceHash(planner)
	if err != nil {
		t.Fatal(err)
	}

	answer := requestFixtureAnswer([]string{"Which checks?"})
	payload, err := json.Marshal(answer)
	if err != nil {
		t.Fatal(err)
	}
	inputTokens, outputTokens, amount := int64(21), int64(8), 3.5
	complete := "complete"
	base := time.Now().UTC()
	sequence := uint64(0)
	for _, record := range before.Records.Requests {
		if record.RequestID == operation.ID && record.Kind == "planning" {
			if record.Sequence > sequence {
				sequence = record.Sequence
			}
			if !base.After(record.At) {
				base = record.At.Add(time.Millisecond)
			}
		}
	}
	inputHash := core.SHA256Hex([]byte("bound planner input"))
	turn := runtime.SavedTurn{
		SessionID: "saved-native", InputHash: inputHash, MessageID: "message", TurnID: "turn",
		Payload: string(payload), SubmittedAt: base.Add(3 * time.Millisecond),
		FinishedAt: base.Add(4 * time.Millisecond), HistoryHash: "sha256:" + strings.Repeat("c", 64),
		Events: []runtime.Event{{Type: runtime.EventOutput, Payload: string(payload)}},
		UsageTotals: []core.DispatchUsage{{
			Model: "model", InputTokens: &inputTokens, OutputTokens: &outputTokens,
		}},
	}
	initial := plannerAttemptEvidence{
		Version: 1, Phase: "attempt", AttemptID: "attempt-usage", OperationID: operation.ID,
		TaskID: checkpoint.Task.ID, TaskHash: taskHash, ConfigHash: selectionHash,
		SelectionHash: selectionHash, Worktree: checkpoint.Task.Worktree, InputHash: inputHash,
		SourceHash: sourceHash, Deadline: operation.Deadline.UTC().Format(time.RFC3339Nano),
	}
	session := initial
	session.Phase, session.SessionID = "session", turn.SessionID
	send := session
	send.Phase, send.PromptHash = "send-intent", turn.InputHash
	terminal := plannerTerminalEvidence{
		Version: 1, AttemptID: initial.AttemptID, SessionID: turn.SessionID, InputHash: inputHash,
		PromptHash: send.PromptHash, SourceHash: sourceHash, OutputHash: core.SHA256Hex(payload),
		Terminal: runtime.EventTurnFinished, ObservedAt: turn.FinishedAt.Add(time.Millisecond),
		Answer: answer, Usage: []core.DispatchUsage{{
			Model: "model", InputTokens: &inputTokens, OutputTokens: &outputTokens,
			TotalNanoAIU: &amount, AICreditsStatus: &complete,
		}},
	}
	if partialCalls {
		known, native, zeroCost := int64(10), int64(11), 0.0
		nativeInput, nativeOutput := inputTokens*2, outputTokens*2
		second := terminal.Usage[0]
		second.TotalNanoAIU = &zeroCost
		terminal.Usage[0].CacheReadTokens = &known
		terminal.Usage = append(terminal.Usage, second)
		turn.UsageTotals[0].InputTokens, turn.UsageTotals[0].OutputTokens = &nativeInput, &nativeOutput
		turn.UsageTotals[0].CacheReadTokens, turn.UsageTotals[0].TotalNanoAIU = &native, &amount
		turn.UsageTotals[0].AICreditsStatus = &complete
	}
	for index := len(terminal.Usage) - 1; index >= 0; index-- {
		data, err := json.Marshal(terminal.Usage[index])
		if err != nil {
			t.Fatal(err)
		}
		turn.Events = append([]runtime.Event{{Type: runtime.EventUsage, Payload: string(data)}}, turn.Events...)
	}
	var receipts []store.RequestRecord
	evidence := []any{initial, session, send}
	if terminalPresent {
		evidence = append(evidence, terminal)
	}
	for index, evidence := range evidence {
		data, err := json.Marshal(evidence)
		if err != nil {
			t.Fatal(err)
		}
		at := []time.Time{base, base.Add(time.Millisecond), base.Add(2 * time.Millisecond), base.Add(6 * time.Millisecond)}[index]
		receipts = append(receipts, store.RequestRecord{
			RequestID: operation.ID, Kind: "planning", Sequence: sequence + uint64(index+1),
			Payload: string(data), Hash: core.SHA256Hex(data), At: at,
		})
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		state.Records.Requests = append(state.Records.Requests, receipts...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	oldObserver := observeSavedPlannerTurn
	t.Cleanup(func() { observeSavedPlannerTurn = oldObserver })
	observeSavedPlannerTurn = func(binding plannerAttemptEvidence, _, _ string, confirmed bool) (runtime.SavedTurn, error) {
		if binding.AttemptID != initial.AttemptID || !confirmed {
			t.Fatal("recovery lost the exact stopped attempt binding")
		}
		return turn, nil
	}
	recoveryArgs := []string{
		"--id", operation.ID, "--actor", "human", "--source", "actual-stop-approval",
		"--reason", "reconcile only the stopped request", "--evidence", "specific-process-stop",
		"--confirm-stopped", "--accept-uncertainty", "--resolution", "cancel",
	}
	originalOutput := turn.UsageTotals[0].OutputTokens
	mismatchedTokens := *originalOutput - 1
	turn.UsageTotals[0].OutputTokens = &mismatchedTokens
	if err := requestRecoveryCommand(append(recoveryArgs, "--preview"), &bytes.Buffer{}); err == nil {
		t.Fatal("cancellation accepted a known conflict in native usage totals")
	}
	turn.UsageTotals[0].OutputTokens = originalOutput
	if !terminalPresent {
		usagePayload := turn.Events[0].Payload
		turn.Events[0].Payload = "private-invalid-usage"
		if err := requestRecoveryCommand(append(recoveryArgs, "--preview"), &bytes.Buffer{}); err == nil ||
			strings.Contains(err.Error(), "private-invalid-usage") {
			t.Fatal("invalid saved usage was accepted or disclosed", err)
		}
		turn.Events[0].Payload = usagePayload
	}
	if partialCalls && terminalPresent {
		if delay := time.Until(turn.FinishedAt); delay > 0 {
			time.Sleep(delay)
		}
		turn.ShutdownAt = time.Now().UTC()
		continueArgs := append([]string(nil), recoveryArgs[:len(recoveryArgs)-3]...)
		continueArgs = append(continueArgs, "--resolution", "continue", "--preview")
		err := requestRecoveryCommand(continueArgs, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "incomplete/conflicting usage") {
			t.Fatal("ordinary continuation did not reject partial usage at the shared admission check", err)
		}
	}
	var preview bytes.Buffer
	if err := requestRecoveryCommand(append(recoveryArgs, "--preview"), &preview); err != nil {
		t.Fatal(err)
	}
	var report requestRecoveryPreview
	if err := json.Unmarshal(preview.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.UsageStatus != "unknown" {
		t.Fatal("incomplete native totals were treated as complete")
	}
	if err := requestRecoveryCommand(append(recoveryArgs, "--apply", "--expected-hash", report.Hash), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	after, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if after.Records.Operation.State != "CANCELLED" ||
		after.Records.Operation.PlanningNanoAIU != operation.PlanningNanoAIU+amount ||
		after.Records.Operation.PlanningUsageState != "unknown" {
		t.Fatal("cancellation failed to retain known per-call usage and aggregate uncertainty")
	}
}

func TestRecoveredQuestionUsageMustMatchNativeShutdownTotals(t *testing.T) {
	input, output, amount := int64(21), int64(8), 3.5
	complete := "complete"
	calls := []core.DispatchUsage{{
		Model: "model", InputTokens: &input, OutputTokens: &output,
		TotalNanoAIU: &amount, AICreditsStatus: &complete,
	}}
	totals := []core.DispatchUsage{{
		Model: "model", InputTokens: &input, OutputTokens: &output,
		TotalNanoAIU: &amount, AICreditsStatus: &complete,
	}}
	if !matchesQuestionUsage(calls, totals) {
		t.Fatal("matching live and native usage was rejected")
	}
	mismatchedAmount := 4.5
	totals[0].TotalNanoAIU = &mismatchedAmount
	if matchesQuestionUsage(calls, totals) {
		t.Fatal("conflicting native cost totals were accepted")
	}
	if !questionUsageTotalsConflict(calls, totals) {
		t.Fatal("known native cost conflict was treated as missing aggregate data")
	}
	totals[0].TotalNanoAIU = nil
	if matchesQuestionUsage(calls, totals) {
		t.Fatal("missing native cost totals were accepted")
	}
	if questionUsageTotalsConflict(calls, totals) {
		t.Fatal("incomplete but non-conflicting native totals were rejected")
	}
	if questionUsageTotalsConflict(calls, nil) {
		t.Fatal("missing native aggregate totals were treated as a conflict")
	}
	totals[0].TotalNanoAIU = &amount
	totals[0].OutputTokens = nil
	if matchesQuestionUsage(calls, totals) {
		t.Fatal("incomplete native token totals were accepted")
	}
	totals[0].InputTokens = &input
	totals[0].OutputTokens = &output
	mismatchedTokens := output + 1
	totals[0].OutputTokens = &mismatchedTokens
	if !questionUsageTotalsConflict(calls, totals) {
		t.Fatal("known native token conflict was treated as missing aggregate data")
	}
	if !questionUsageTotalsConflict(nil, totals) {
		t.Fatal("native totals without durable per-call usage were accepted")
	}
}

func TestRecoveredQuestionPartialUsageCannotHideNativeConflict(t *testing.T) {
	for _, dimension := range []string{"cache-read", "cache-write", "native-cost"} {
		t.Run(dimension, func(t *testing.T) {
			input, output, totalInput, totalOutput := int64(21), int64(8), int64(42), int64(16)
			known, smaller, larger := int64(10), int64(9), int64(11)
			knownCost, smallerCost, largerCost := 3.5, 3.0, 4.0
			calls := []core.DispatchUsage{
				{Model: "model", InputTokens: &input, OutputTokens: &output},
				{Model: "model", InputTokens: &input, OutputTokens: &output},
			}
			totals := []core.DispatchUsage{{
				Model: "model", InputTokens: &totalInput, OutputTokens: &totalOutput,
			}}
			switch dimension {
			case "cache-read":
				calls[0].CacheReadTokens, totals[0].CacheReadTokens = &known, &smaller
			case "cache-write":
				calls[0].CacheWriteTokens, totals[0].CacheWriteTokens = &known, &smaller
			case "native-cost":
				calls[0].TotalNanoAIU, totals[0].TotalNanoAIU = &knownCost, &smallerCost
			}
			if matchesQuestionUsage(calls, totals) || !questionUsageTotalsConflict(calls, totals) {
				t.Fatal("missing per-call usage hid a known aggregate conflict")
			}
			switch dimension {
			case "cache-read":
				totals[0].CacheReadTokens = &larger
			case "cache-write":
				totals[0].CacheWriteTokens = &larger
			case "native-cost":
				totals[0].TotalNanoAIU = &largerCost
			}
			if matchesQuestionUsage(calls, totals) || questionUsageTotalsConflict(calls, totals) {
				t.Fatal("missing per-call usage was inferred as zero or accepted as a complete match")
			}
		})
	}
}

func TestRequestRecoveryApplyIsIdempotentOnlyForExactDecision(t *testing.T) {
	request := requestRecoveryInput{
		OperationID: "request-1", Actor: "human", Source: "decision-1",
		Reason: "adopt saved answer", Evidence: []string{"terminal-receipt", "native-history"},
	}

	requestHash, err := core.CanonicalSHA256(request)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(requestRecoveryApplication{
		Version: 1, AttemptID: "attempt-1", PreviewHash: "sha256:preview",
		RequestHash: requestHash, Decision: request,
	})
	if err != nil {
		t.Fatal(err)
	}
	state := store.Workspace{Records: store.Records{Requests: []store.RequestRecord{{
		RequestID: request.OperationID, Kind: "planning", Payload: string(payload),
		Hash: core.SHA256Hex(payload),
	}}}}
	if _, ok, err := priorRequestRecovery(state, request.OperationID, "sha256:preview", request); err != nil || !ok {
		t.Fatal("exact repeated apply was not recognized", err)
	}
	request.Reason = "different decision"
	if _, ok, err := priorRequestRecovery(state, request.OperationID, "sha256:preview", request); err != nil || ok {
		t.Fatal("changed recovery decision was treated as idempotent", err)
	}
}

func TestLegacyPlannerAssociationRequiresOriginalSourceAndExactTask(t *testing.T) {
	original := "finish the approved documentation check"
	checkpoint := requestCheckpoint{Task: core.GraphTask{ID: "request-1-planning"},
		RequestHash: "sha256:" + core.SHA256Hex([]byte(original))}
	for _, test := range []struct {
		task, source string
		accepted     bool
	}{
		{checkpoint.Task.ID, original, true},
		{"request-2-planning", original, false},
		{checkpoint.Task.ID, original + " and execute", false},
		{checkpoint.Task.ID, checkpoint.RequestHash, false},
	} {
		question, err := json.Marshal(map[string]any{"conversation": []map[string]string{{"role": "human", "text": test.source}}})
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(map[string]any{
			"question": string(question),
			"context":  map[string]any{"planner": map[string]any{"task": map[string]string{"id": test.task}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if legacyPlannerInputMatches(string(data), checkpoint) != test.accepted {
			t.Fatal("legacy association lost the original source/Task binding")
		}
	}
	quoted, _ := json.Marshal(map[string]any{
		"question": `{"conversation":[{"role":"human","text":"` + original + `"}]}`,
		"context":  map[string]string{"id": checkpoint.Task.ID},
	})
	if legacyPlannerInputMatches(string(quoted), checkpoint) {
		t.Fatal("quoted Task identity outside the native Planner context was accepted")
	}
}

func TestRecoveredTerminalIsDerivedOnlyFromTheBoundNativeInput(t *testing.T) {
	binding := plannerAttemptEvidence{AttemptID: "a", SessionID: "s", PromptHash: "input"}
	turn := runtime.SavedTurn{
		SessionID: "s", InputHash: "input", MessageID: "m", TurnID: "t", HistoryHash: "history",
		Payload: `{"recommendation":"HUMAN_REQUIRED","confirmed":[],"assumptions":[],"unconfirmed":[],"questions":["Which checks?"],"proposal":null}`,
	}
	terminal, err := terminalFromSavedPlanner(binding, turn, false)
	if err != nil || terminal.Version != 2 || !terminal.ObservedAt.IsZero() ||
		terminal.Answer.Proposal != nil || terminal.Answer.Recommendation != "HUMAN_REQUIRED" {
		t.Fatal("native association fabricated a historical receipt or changed advice", err)
	}
	turn.InputHash = "different input"
	if _, err := terminalFromSavedPlanner(binding, turn, false); err == nil {
		t.Fatal("unbound input was accepted")
	}
	turn.InputHash, turn.Payload = binding.PromptHash, "invalid saved answer"
	if _, err := terminalFromSavedPlanner(binding, turn, false); err == nil {
		t.Fatal("invalid saved advice was adopted")
	}
	terminal, err = terminalFromSavedPlanner(binding, turn, true)
	if err != nil || !terminal.InvalidAnswer || terminal.Answer.Recommendation != "" ||
		terminal.OutputHash != core.SHA256Hex([]byte(turn.Payload)) {
		t.Fatal("uncertainty cancellation lost the unaccepted native answer identity", err)
	}
}

func TestRequestRecoveryUncertaintyRequiresStoppedCancellationConsent(t *testing.T) {
	request := requestRecoveryInput{
		OperationID: "request-1", Actor: "human", Source: "actual-decision", Reason: "release stopped operation",
		Evidence: []string{"specific-process-stop"}, SessionID: "native", InputHash: strings.Repeat("a", 64),
		AssociateLegacy: true, StoppedConfirmed: true, AcceptUncertainty: true, Resolution: "cancel",
	}
	if err := validateRequestRecoveryInput(request); err != nil {
		t.Fatal(err)
	}
	request.Resolution = "continue"
	if err := validateRequestRecoveryInput(request); err == nil {
		t.Fatal("uncertainty acceptance permitted continuation")
	}
	request.Resolution, request.StoppedConfirmed = "cancel", false
	if err := validateRequestRecoveryInput(request); err == nil {
		t.Fatal("cancellation accepted without stopped-process evidence")
	}
}

func TestRequestRecoveryCanUseNativeQuiescenceWithoutCleanupReceipt(t *testing.T) {
	now := time.Now().UTC()
	initial := plannerAttemptEvidence{
		Version: 1, Phase: "attempt", AttemptID: "attempt-1", OperationID: "request-1",
		TaskID: "request-1-planning", TaskHash: "task-hash", ConfigHash: strings.Repeat("a", 64),
		SelectionHash: "selection-hash", Worktree: "/work/request", InputHash: "input-hash",
		SourceHash: "source-hash", Deadline: now.Add(time.Hour).Format(time.RFC3339Nano),
	}
	session := initial
	session.Phase, session.SessionID = "session", "session-1"
	send := session
	send.Phase, send.PromptHash = "send-intent", "prompt-hash"
	terminal := plannerTerminalEvidence{
		Version: 1, AttemptID: initial.AttemptID, SessionID: session.SessionID,
		InputHash: initial.InputHash, PromptHash: send.PromptHash, SourceHash: initial.SourceHash,
		OutputHash: "output-hash", Terminal: runtime.EventTurnFinished, ObservedAt: now.Add(3 * time.Second),
	}
	records := []store.RequestRecord{}
	for index, value := range []any{initial, session, send, terminal} {
		payload, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, store.RequestRecord{
			RequestID: initial.OperationID, Kind: "planning", Sequence: uint64(index + 1),
			Payload: string(payload), Hash: core.SHA256Hex(payload), At: now.Add(time.Duration(index) * time.Second),
		})
	}
	state := store.Workspace{Records: store.Records{Requests: records}}
	withoutTerminal := state
	withoutTerminal.Records.Requests = records[:3]
	bound, missing, _, _, err := requestRecoveryRecords(withoutTerminal, initial.OperationID)
	if err != nil || bound.PromptHash != send.PromptHash || missing.AttemptID != "" {
		t.Fatal("durable sent attempt without a terminal receipt cannot reach native reconciliation", err)
	}
	binding, _, cleanup, sessionAt, err := requestRecoveryRecords(state, initial.OperationID)
	if err != nil || binding.PromptHash != send.PromptHash || cleanup.AttemptID != "" ||
		!sessionAt.Equal(records[1].At) {
		t.Fatal("terminal receipt without cleanup record could not be reconciled against native shutdown", err)
	}

	records = append(records, store.RequestRecord{
		RequestID: initial.OperationID, Kind: "planning", Sequence: 5,
		Payload: `{"attemptId":"attempt-1","sessionId":"session-1","stopped":false}`,
		Hash:    core.SHA256Hex([]byte(`{"attemptId":"attempt-1","sessionId":"session-1","stopped":false}`)),
		At:      now.Add(4 * time.Second),
	})
	state.Records.Requests = records
	_, _, cleanup, _, err = requestRecoveryRecords(state, initial.OperationID)
	if err != nil || cleanup.Stopped {
		t.Fatal("failed cleanup receipt masked independently verifiable native shutdown", err)
	}
	next := initial
	next.AttemptID = "attempt-2"
	nextSession := next
	nextSession.Phase, nextSession.SessionID = "session", "session-2"
	nextSend := nextSession
	nextSend.Phase, nextSend.PromptHash = "send-intent", "prompt-2"
	for index, value := range []any{next, nextSession, nextSend} {
		payload, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		state.Records.Requests = append(state.Records.Requests, store.RequestRecord{
			RequestID: initial.OperationID, Kind: "planning", Sequence: uint64(index + 6),
			Payload: string(payload), Hash: core.SHA256Hex(payload), At: now.Add(time.Duration(index+5) * time.Second),
		})
	}
	if _, _, _, _, err := requestRecoveryRecords(state, initial.OperationID); err == nil {
		t.Fatal("an unresolved earlier paid attempt was ignored")
	}
	stopped := `{"attemptId":"attempt-1","sessionId":"session-1","stopped":true}`
	state.Records.Requests[4].Payload = stopped
	state.Records.Requests[4].Hash = core.SHA256Hex([]byte(stopped))
	binding, terminal, cleanup, _, err = requestRecoveryRecords(state, initial.OperationID)
	if err != nil || binding.AttemptID != next.AttemptID || binding.PromptHash != nextSend.PromptHash ||
		terminal.AttemptID != "" || cleanup.AttemptID != "" {
		t.Fatal("latest sent attempt did not replace the resolved earlier attempt", err)
	}
	state.Records.Requests[6].Sequence, state.Records.Requests[7].Sequence = 8, 7
	if _, _, _, _, err := requestRecoveryRecords(state, initial.OperationID); err == nil {
		t.Fatal("send-before-session receipt ordering was accepted")
	}
}

func TestRecoveredPlannerUsageCorrectionsRetainKnownFloor(t *testing.T) {
	state := store.Workspace{}
	operation := store.Operation{ID: "request-1", PlanningNanoAIU: 10, PlanningUsageState: "unknown"}
	usage := func(amount float64, status string) []core.DispatchUsage {
		return []core.DispatchUsage{{Model: "model", TotalNanoAIU: &amount, AICreditsStatus: &status}}
	}
	for _, update := range []struct {
		amount float64
		status string
		total  float64
	}{
		{3, "unknown", 13},
		{5, "complete", 15},
		{2, "unknown", 15},
	} {
		calls := usage(update.amount, update.status)
		if err := recordRecoveredPlannerUsage(&state, &operation, "attempt-1", calls, update.amount, update.status); err != nil {
			t.Fatal(err)
		}
		if operation.PlanningNanoAIU != update.total || operation.PlanningUsageState != "unknown" {
			t.Fatal("usage correction reduced the known floor or erased cumulative uncertainty")
		}
	}
	before := operation
	if err := recordRecoveredPlannerUsage(&state, &operation, "attempt-1", usage(4, "complete"), 4, "complete"); err == nil ||
		!reflect.DeepEqual(operation, before) {
		t.Fatal("conflicting lower complete usage was accepted or mutated accounting")
	}
}
