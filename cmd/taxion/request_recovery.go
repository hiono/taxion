package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/runtime/copilot"
	"github.com/hiono/taxion/internal/store"
)

var errNoRecordedPlannerAttempt = errors.New("request recovery has no recorded Planner attempt")

type plannerAttemptEvidence struct {
	Version       int    `json:"version"`
	Phase         string `json:"phase"`
	AttemptID     string `json:"attemptId"`
	OperationID   string `json:"operationId"`
	TaskID        string `json:"taskId"`
	TaskHash      string `json:"taskHash"`
	ConfigHash    string `json:"configHash"`
	SelectionHash string `json:"selectionHash"`
	Worktree      string `json:"worktree"`
	SessionID     string `json:"sessionId,omitempty"`
	InputHash     string `json:"inputHash"`
	PromptHash    string `json:"promptHash,omitempty"`
	SourceHash    string `json:"sourceHash"`
	Deadline      string `json:"deadline,omitempty"`
}

type plannerTerminalEvidence struct {
	Version          int                       `json:"version"`
	AttemptID        string                    `json:"attemptId"`
	SessionID        string                    `json:"sessionId"`
	InputHash        string                    `json:"inputHash"`
	PromptHash       string                    `json:"promptHash"`
	SourceHash       string                    `json:"sourceHash"`
	OutputHash       string                    `json:"outputHash"`
	Terminal         string                    `json:"terminalEvent"`
	ObservedAt       time.Time                 `json:"observedAt"`
	Answer           controller.QuestionAnswer `json:"answer"`
	Usage            []core.DispatchUsage      `json:"usage"`
	UsageCheckpoints []runtime.UsageCheckpoint `json:"usageCheckpoints,omitempty"`
	InvalidAnswer    bool                      `json:"invalidAnswer,omitempty"`
}

type plannerCleanupEvidence struct {
	AttemptID        string                    `json:"attemptId"`
	SessionID        string                    `json:"sessionId"`
	Stopped          bool                      `json:"stopped"`
	Usage            []core.DispatchUsage      `json:"usage,omitempty"`
	UsageCheckpoints []runtime.UsageCheckpoint `json:"usageCheckpoints,omitempty"`
	PostStopError    bool                      `json:"postStopError,omitempty"`
}

func mergePlannerCleanupUsage(terminal *plannerTerminalEvidence, cleanup plannerCleanupEvidence) error {
	for _, call := range cleanup.Usage {
		if err := core.AppendUsage(&terminal.Usage, call); err != nil {
			return err
		}
	}
	for _, checkpoint := range cleanup.UsageCheckpoints {
		body, err := json.Marshal(checkpoint)
		if err != nil {
			return err
		}
		if _, _, err := runtime.DecodeUsageCheckpoint(string(body), terminal.SessionID, terminal.Usage); err != nil {
			return err
		}
		terminal.UsageCheckpoints = append(terminal.UsageCheckpoints, checkpoint)
	}
	_, err := core.SummarizeUsage(terminal.SessionID, terminal.Usage, terminal.UsageCheckpoints)
	return err
}

type requestRecoveryInput struct {
	OperationID       string   `json:"operationId"`
	Actor             string   `json:"actor"`
	Source            string   `json:"source"`
	Reason            string   `json:"reason"`
	Evidence          []string `json:"evidence"`
	SessionID         string   `json:"sessionId,omitempty"`
	InputHash         string   `json:"inputHash,omitempty"`
	AssociateLegacy   bool     `json:"associateLegacy,omitempty"`
	StoppedConfirmed  bool     `json:"stoppedConfirmed,omitempty"`
	AcceptUncertainty bool     `json:"acceptUncertainty,omitempty"`
	Resolution        string   `json:"resolution,omitempty"`
}

type requestRecoveryPreview struct {
	Version        int      `json:"version"`
	StateChanged   bool     `json:"stateChanged"`
	OperationID    string   `json:"operationId"`
	TaskID         string   `json:"taskId"`
	AttemptID      string   `json:"attemptId"`
	SessionID      string   `json:"sessionId"`
	HistoryHash    string   `json:"historyHash"`
	Recommendation string   `json:"recommendation"`
	Questions      []string `json:"questions"`
	ProposalTitle  string   `json:"proposalTitle,omitempty"`
	UsageStatus    string   `json:"usageStatus"`
	CleanupStatus  string   `json:"cleanupStatus"`
	BindingStatus  string   `json:"bindingStatus"`
	Uncertainties  []string `json:"uncertainties,omitempty"`
	Resolution     string   `json:"resolution"`
	Transitions    []string `json:"transitions"`
	Hash           string   `json:"hash"`
}

type requestRecoveryApplication struct {
	Version                int                        `json:"version"`
	AttemptID              string                     `json:"attemptId"`
	PreviewHash            string                     `json:"previewHash"`
	RequestHash            string                     `json:"requestHash"`
	Decision               requestRecoveryInput       `json:"decision"`
	AnswerHash             string                     `json:"answerHash"`
	HistoryHash            string                     `json:"historyHash"`
	AppliedAt              time.Time                  `json:"appliedAt"`
	OriginalCheckpointHash string                     `json:"originalCheckpointHash,omitempty"`
	RetainedCheckpoint     *requestCheckpoint         `json:"retainedCheckpoint,omitempty"`
	RetainedAnswer         *controller.QuestionAnswer `json:"retainedAnswer,omitempty"`
	Uncertainties          []string                   `json:"uncertainties,omitempty"`
}

var observeSavedPlannerTurn = readBoundPlannerTurn

func requestRecoveryCommand(args []string, output io.Writer) (returnErr error) {
	fs := flag.NewFlagSet("request recover", flag.ContinueOnError)
	id := fs.String("id", "", "saved request operation ID")
	actor := fs.String("actor", "", "decision actor")
	source := fs.String("source", "", "decision source")
	reason := fs.String("reason", "", "reconciliation reason")
	var evidence stringListFlag
	fs.Var(&evidence, "evidence", "quiescence and saved-evidence reference (repeatable)")
	preview := fs.Bool("preview", false, "read-only recovery preview")
	apply := fs.Bool("apply", false, "apply the exact preview hash")
	expectedHash := fs.String("expected-hash", "", "exact preview hash to apply")
	cli := fs.String("cli", "", "Copilot CLI executable used by the saved session")
	base := fs.String("base-directory", "", "Copilot state directory used by the saved session")
	sessionID := fs.String("session", "", "exact saved native session for an explicit legacy association")
	inputHash := fs.String("input-hash", "", "exact saved native user-input SHA-256 for a legacy association")
	associateLegacy := fs.Bool("associate-legacy", false, "approve a new association; do not claim a historical receipt exists")
	stoppedConfirmed := fs.Bool("confirm-stopped", false, "confirm the original process tree is stopped using the cited evidence")
	acceptUncertainty := fs.Bool("accept-uncertainty", false, "accept recorded historical configuration or usage uncertainty for cancellation only")
	resolution := fs.String("resolution", "continue", "continue the checkpoint or cancel after reconciliation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *id == "" || *preview == *apply || *apply && *expectedHash == "" ||
		*preview && *expectedHash != "" {
		return fmt.Errorf("usage: request recover --id ID --actor ACTOR --source SOURCE --reason REASON --evidence REF [--evidence REF] (--preview | --apply --expected-hash HASH)")
	}
	request := requestRecoveryInput{
		OperationID: *id, Actor: *actor, Source: *source, Reason: *reason, Evidence: append([]string(nil), evidence...),
		SessionID: *sessionID, InputHash: strings.TrimPrefix(*inputHash, "sha256:"),
		AssociateLegacy: *associateLegacy, StoppedConfirmed: *stoppedConfirmed,
		AcceptUncertainty: *acceptUncertainty, Resolution: *resolution,
	}
	if err := validateRequestRecoveryInput(request); err != nil {
		return err
	}
	var db *store.Store
	var state store.Workspace
	var err error
	if *preview {
		db, err = store.OpenReadOnly(workspaceDBPath())
		if err == nil {
			state, err = db.LoadWorkspace()
		}
	} else {
		db, state, err = loadWorkspace()
	}
	if err != nil {
		if db != nil {
			err = errors.Join(err, db.Close())
		}
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, db.Close()) }()
	if *apply {
		if application, ok, err := priorRequestRecovery(state, *id, *expectedHash, request); err != nil {
			return err
		} else if ok {
			return json.NewEncoder(output).Encode(struct {
				Gate    string                     `json:"gate"`
				Applied requestRecoveryApplication `json:"applied"`
			}{"APPLIED", application})
		}
	}
	operation := state.Records.Operation
	if operation == nil || operation.ID != *id || operation.Kind != "request" && operation.Kind != "question" {
		return fmt.Errorf("request recovery requires the matching saved request or side-question operation")
	}
	if operation.Owner != "" && !request.StoppedConfirmed {
		return fmt.Errorf("request recovery requires an unowned terminal attempt; resolve the current owner first")
	}
	var checkpoint requestCheckpoint
	if operation.Kind == "request" {
		if err := core.DecodeGraphInput(bytes.NewReader(operation.Checkpoint), &checkpoint); err != nil {
			return fmt.Errorf("request recovery checkpoint is invalid: %w", err)
		}
	} else {
		if request.AssociateLegacy {
			return fmt.Errorf("side-question recovery requires a durable sent-attempt binding; legacy association is request-only")
		}
		resolved, err := resolveConfiguration()
		if err != nil {
			return err
		}
		task, exists := state.Graph.GraphTask(operation.TaskID)
		if !exists {
			return fmt.Errorf("side-question Task is missing")
		}
		if _, err := controller.CheckConfiguration(db, operation.TaskID, resolved.Config); err != nil {
			return err
		}
		checkpoint = requestCheckpoint{Version: 2, OperationID: operation.ID,
			Task: task, Selection: resolved.Config, PlannerStarted: true}
	}
	if checkpoint.Selection.Runtime.Provider != "copilot" {
		return fmt.Errorf("request recovery requires the saved Copilot native session; this provider has no supported saved-turn reader")
	}
	binding, terminal, cleanup, sessionAt, err := requestRecoveryRecords(state, *id)
	if err != nil && !(request.AssociateLegacy && errors.Is(err, errNoRecordedPlannerAttempt)) {
		return err
	}
	if request.AssociateLegacy {
		if hasPlannerAttemptRecords(state, *id) {
			return fmt.Errorf("legacy association cannot replace existing attempt evidence")
		}
		binding.SessionID = request.SessionID
		binding.Worktree = checkpoint.Task.Worktree
		binding.ConfigHash, err = core.CanonicalSHA256(checkpoint.Selection)
		if err != nil {
			return err
		}
	}
	turn, err := observeSavedPlannerTurn(binding, *cli, *base, request.StoppedConfirmed)
	if err != nil {
		return err
	}
	planner, question, err := observeRecoveredAdvice(db, checkpoint, operation.Kind, binding)
	if err != nil {
		return err
	}
	if terminal.AttemptID != "" {
		if err := mergePlannerCleanupUsage(&terminal, cleanup); err != nil {
			return err
		}
	}
	if request.AssociateLegacy {
		binding, terminal, sessionAt, err = associateLegacyPlanner(state, checkpoint, *operation, request, turn, planner)
		if err != nil {
			return err
		}
	} else if terminal.AttemptID == "" {
		terminal, err = terminalFromSavedPlanner(binding, turn, request.AcceptUncertainty && request.Resolution == "cancel")
		if err != nil {
			return err
		}
		if question != nil && !(request.AcceptUncertainty && request.Resolution == "cancel") {
			if err := controller.ValidateQuestionAnswer(terminal.Answer, *question); err != nil {
				return err
			}
		}
	}
	report, answer, usageAmount, usageStatus, err := buildRequestRecoveryPreview(
		state, checkpoint, *operation, request, binding, terminal, cleanup, sessionAt, turn, planner)
	if err != nil {
		return err
	}
	report.UsageStatus = usageStatus
	if *preview {
		return json.NewEncoder(output).Encode(report)
	}
	if report.Hash != *expectedHash {
		return fmt.Errorf("request recovery state changed; approve the exact current --preview hash")
	}
	requestHash, err := core.CanonicalSHA256(request)
	if err != nil {
		return err
	}
	lease, err := db.LockOperation()
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, lease.Close()) }()
	claimed, err := lease.Claim(store.Operation{ID: *id, Owner: rand.Text()}, true)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, db.ReleaseOperation(claimed.ID, claimed.Owner)) }()
	confirmed, err := observeSavedPlannerTurn(binding, *cli, *base, request.StoppedConfirmed)
	if err != nil {
		return err
	}
	if confirmed.HistoryHash != turn.HistoryHash {
		return fmt.Errorf("native session changed during recovery; acceptance withheld, preview again")
	}
	planner, question, err = observeRecoveredAdvice(db, checkpoint, operation.Kind, binding)
	if err != nil {
		return err
	}
	if question != nil && !(request.AcceptUncertainty && request.Resolution == "cancel") {
		if err := controller.ValidateQuestionAnswer(terminal.Answer, *question); err != nil {
			return err
		}
	}
	err = db.UpdateWorkspace(func(current *store.Workspace) error {
		savedOperation := current.Records.Operation
		if savedOperation == nil || savedOperation.ID != *id || savedOperation.Owner != claimed.Owner ||
			savedOperation.State != "BLOCKED" && savedOperation.State != "USAGE_EXCEEDED" &&
				!(savedOperation.State == "RUNNING" && request.StoppedConfirmed) {
			return fmt.Errorf("request recovery operation ownership or state changed")
		}
		var savedCheckpoint requestCheckpoint
		if savedOperation.Kind == "request" {
			if err := core.DecodeGraphInput(bytes.NewReader(savedOperation.Checkpoint), &savedCheckpoint); err != nil {
				return err
			}
		} else {
			savedCheckpoint = checkpoint
		}
		fresh, freshTerminal, freshCleanup, freshSessionAt, err := requestRecoveryRecords(*current, *id)
		if err == nil && freshTerminal.AttemptID != "" {
			err = mergePlannerCleanupUsage(&freshTerminal, freshCleanup)
		}
		if request.AssociateLegacy {
			if err != nil && !errors.Is(err, errNoRecordedPlannerAttempt) {
				return err
			}
			if hasPlannerAttemptRecords(*current, *id) {
				return fmt.Errorf("legacy association evidence changed")
			}
			fresh, freshTerminal, freshSessionAt, err = associateLegacyPlanner(*current, savedCheckpoint, *savedOperation, request, turn, planner)
		} else if err == nil && freshTerminal.AttemptID == "" {
			freshTerminal, err = terminalFromSavedPlanner(fresh, turn, request.AcceptUncertainty && request.Resolution == "cancel")
		}
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(fresh, binding) || !reflect.DeepEqual(freshTerminal, terminal) ||
			!reflect.DeepEqual(freshCleanup, cleanup) || !freshSessionAt.Equal(sessionAt) {
			return fmt.Errorf("request recovery attempt binding changed")
		}
		next, _, _, _, err := buildRequestRecoveryPreview(*current, savedCheckpoint, *savedOperation,
			request, fresh, freshTerminal, freshCleanup, freshSessionAt, turn, planner)
		if err != nil {
			return err
		}
		if next.Hash != *expectedHash {
			return fmt.Errorf("request recovery state changed during apply; preview again")
		}
		if err := recordRecoveredPlannerUsage(current, savedOperation, binding.AttemptID, terminal.Usage, usageAmount, usageStatus, terminal.UsageCheckpoints...); err != nil {
			return err
		}
		minimal := minimalRequestAnswer(answer)
		if request.Resolution != "cancel" {
			savedCheckpoint.Answer = &minimal
		}
		savedCheckpoint.PlannerStarted = false
		data, err := json.Marshal(savedCheckpoint)
		if err != nil {
			return err
		}
		if core.Redact(string(data)) != string(data) {
			return fmt.Errorf("recovered Planner answer contains a credential-like value")
		}
		if savedOperation.Kind == "request" {
			savedOperation.Checkpoint = data
			savedOperation.CheckpointHash = core.SHA256Hex(data)
		}
		switch {
		case request.Resolution == "cancel":
			savedOperation.State = "CANCELLED"
		case savedOperation.Kind == "question":
			savedOperation.State = "COMPLETED"
		case !time.Now().Before(savedOperation.Deadline):
			savedOperation.State = "USAGE_EXCEEDED"
		case !savedOperation.ExternalBudgetOnly() &&
			(savedOperation.PlanningUsageState != "complete" ||
				savedOperation.PlanningNanoAIU >= savedOperation.MaxNanoAIU):
			savedOperation.State = "USAGE_EXCEEDED"
		case len(answer.Questions) != 0:
			savedOperation.State = "WAITING_FOR_ANSWER"
		default:
			savedOperation.State = "WAITING_FOR_APPROVAL"
		}
		audit := requestRecoveryApplication{
			Version: 1, AttemptID: binding.AttemptID, PreviewHash: report.Hash,
			RequestHash: requestHash, Decision: request,
			AnswerHash: terminal.OutputHash, HistoryHash: turn.HistoryHash, AppliedAt: time.Now().UTC(),
			OriginalCheckpointHash: operation.CheckpointHash,
			Uncertainties:          report.Uncertainties,
		}
		if !terminal.InvalidAnswer {
			audit.RetainedAnswer = &minimal
		}
		if operation.Kind == "request" {
			audit.RetainedCheckpoint = &checkpoint
		}
		payload, err := json.Marshal(audit)
		if err != nil {
			return err
		}
		appendRequestRecord(current, *id, "planning", payload)
		report.StateChanged = true
		return nil
	})
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(struct {
		Gate    string                 `json:"gate"`
		Applied requestRecoveryPreview `json:"applied"`
	}{"APPLIED", report})
}

func validateRequestRecoveryInput(request requestRecoveryInput) error {
	if request.Resolution != "" && request.Resolution != "continue" && request.Resolution != "cancel" {
		return fmt.Errorf("request recovery resolution must be continue or cancel")
	}
	if request.AssociateLegacy && (request.SessionID == "" || !request.StoppedConfirmed || len(request.InputHash) != 64) ||
		!request.AssociateLegacy && (request.SessionID != "" || request.InputHash != "") ||
		request.AcceptUncertainty && (!request.StoppedConfirmed || request.Resolution != "cancel") {
		return fmt.Errorf("legacy recovery requires exact session/input hash and stopped-process evidence; uncertainty can only be accepted for cancellation")
	}
	if request.AssociateLegacy {
		if decoded, err := hex.DecodeString(request.InputHash); err != nil || len(decoded) != 32 {
			return fmt.Errorf("legacy input hash must be an exact SHA-256 digest")
		}
	}
	for _, value := range append([]string{request.OperationID, request.Actor, request.Source, request.Reason}, request.Evidence...) {
		if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value {
			return fmt.Errorf("request recovery requires trimmed operation, actor, source, reason and evidence")
		}
	}
	if len(request.Evidence) == 0 {
		return fmt.Errorf("request recovery requires references to the saved execution and quiescence evidence")
	}
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if core.Redact(string(data)) != string(data) {
		return fmt.Errorf("request recovery decision contains a credential-like value")
	}
	return nil
}

func observeRecoveredAdvice(db *store.Store, checkpoint requestCheckpoint, kind string, binding plannerAttemptEvidence) (controller.PlannerContext, *controller.QuestionContext, error) {
	if kind == "question" {
		question, err := controller.ObserveQuestion(db, storePath, checkpoint.Task.Repo, checkpoint.Task.ID)
		if err != nil {
			return controller.PlannerContext{}, nil, err
		}
		if question.Observation != nil && question.Observation.ContentHash == binding.SourceHash {
			return controller.PlannerContext{Task: question.Task.Task, Observation: question.Observation}, &question, nil
		}
	}
	planner, err := controller.ObservePlannerContext(db, storePath, checkpoint.Task.Repo, checkpoint.Task.ID)
	return planner, nil, err
}

func readBoundPlannerTurn(binding plannerAttemptEvidence, cli, base string, stoppedConfirmed bool) (runtime.SavedTurn, error) {
	reader, err := copilot.NewForSavedQuestionSession(binding.SessionID, binding.ConfigHash, binding.Worktree, cli, base)
	if err != nil {
		return runtime.SavedTurn{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	turn, readErr := reader.ReadSavedQuestionTurn(ctx, binding.SessionID, stoppedConfirmed)
	return turn, errors.Join(readErr, reader.CloseSavedObservation())
}

func hasPlannerAttemptRecords(state store.Workspace, operationID string) bool {
	for _, record := range state.Records.Requests {
		if record.RequestID != operationID || record.Kind != "planning" {
			continue
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal([]byte(record.Payload), &fields) != nil {
			return true
		}
		if _, exists := fields["phase"]; exists {
			return true
		}
	}
	return false
}

func terminalFromSavedPlanner(binding plannerAttemptEvidence, turn runtime.SavedTurn, allowInvalidCancellation bool) (plannerTerminalEvidence, error) {
	if binding.SessionID != turn.SessionID || binding.PromptHash != turn.InputHash ||
		turn.HistoryHash == "" || turn.MessageID == "" || turn.TurnID == "" {
		return plannerTerminalEvidence{}, fmt.Errorf("saved native turn does not match the sent instruction")
	}
	answer, decodeErr := controller.DecodeQuestionAnswer(turn.Payload)
	invalid := decodeErr != nil
	if invalid {
		if !allowInvalidCancellation {
			return plannerTerminalEvidence{}, fmt.Errorf("saved answer has invalid shape or credential-like content; adoption withheld")
		}
		answer = controller.QuestionAnswer{}
	}
	var usage []core.DispatchUsage
	for _, event := range turn.Events {
		if event.Type != runtime.EventUsage {
			continue
		}
		var call core.DispatchUsage
		if err := core.DecodeGraphInput(strings.NewReader(event.Payload), &call); err != nil {
			return plannerTerminalEvidence{}, fmt.Errorf("saved native usage event is invalid")
		}
		if err := core.AppendUsage(&usage, call); err != nil {
			return plannerTerminalEvidence{}, err
		}
	}
	if len(usage) == 0 && len(turn.UsageCheckpoints) == 0 {
		usage = turn.UsageTotals
	}
	return plannerTerminalEvidence{
		Version: 2, AttemptID: binding.AttemptID, SessionID: binding.SessionID,
		InputHash: binding.InputHash, PromptHash: binding.PromptHash, SourceHash: binding.SourceHash,
		OutputHash: core.SHA256Hex([]byte(turn.Payload)), Terminal: runtime.EventTurnEnded,
		Answer: answer, Usage: usage, UsageCheckpoints: turn.UsageCheckpoints,
		InvalidAnswer: invalid,
	}, nil
}

func associateLegacyPlanner(state store.Workspace, checkpoint requestCheckpoint, operation store.Operation,
	request requestRecoveryInput, turn runtime.SavedTurn, planner controller.PlannerContext) (plannerAttemptEvidence, plannerTerminalEvidence, time.Time, error) {
	if !request.AssociateLegacy || !request.StoppedConfirmed || request.SessionID != turn.SessionID ||
		request.InputHash != turn.InputHash || !legacyPlannerInputMatches(turn.Input, checkpoint) {
		return plannerAttemptEvidence{}, plannerTerminalEvidence{}, time.Time{}, fmt.Errorf("legacy association requires the exact native input, original request source and planning Task identity")
	}
	if _, err := verifyRequestCheckpoint(state, operation, checkpoint); err != nil {
		return plannerAttemptEvidence{}, plannerTerminalEvidence{}, time.Time{}, err
	}
	task, exists := state.Graph.GraphTask(checkpoint.Task.ID)
	if !exists {
		return plannerAttemptEvidence{}, plannerTerminalEvidence{}, time.Time{}, fmt.Errorf("legacy planning Task is absent")
	}
	taskHash, err := core.CanonicalSHA256(task)
	if err != nil {
		return plannerAttemptEvidence{}, plannerTerminalEvidence{}, time.Time{}, err
	}
	selectionHash, err := core.CanonicalSHA256(checkpoint.Selection)
	if err != nil {
		return plannerAttemptEvidence{}, plannerTerminalEvidence{}, time.Time{}, err
	}
	sourceHash, err := controller.PlannerQuestionSourceHash(planner)
	if err != nil {
		return plannerAttemptEvidence{}, plannerTerminalEvidence{}, time.Time{}, err
	}
	binding := plannerAttemptEvidence{
		Version: 2, Phase: "legacy-association", AttemptID: "associated-" + turn.SessionID + "-" + operation.CheckpointHash,
		OperationID: operation.ID, TaskID: checkpoint.Task.ID, TaskHash: taskHash,
		ConfigHash: selectionHash, SelectionHash: selectionHash, Worktree: checkpoint.Task.Worktree,
		SessionID: turn.SessionID, InputHash: turn.InputHash, PromptHash: turn.InputHash,
		SourceHash: sourceHash, Deadline: operation.Deadline.UTC().Format(time.RFC3339Nano),
	}
	terminal, err := terminalFromSavedPlanner(binding, turn, request.AcceptUncertainty && request.Resolution == "cancel")
	return binding, terminal, turn.SubmittedAt, err
}

func legacyPlannerInputMatches(input string, checkpoint requestCheckpoint) bool {
	var envelope struct {
		Question string `json:"question"`
		Context  struct {
			Planner struct {
				Task struct {
					ID string `json:"id"`
				} `json:"task"`
			} `json:"planner"`
		} `json:"context"`
	}
	if json.Unmarshal([]byte(input), &envelope) != nil || checkpoint.RequestHash == "" ||
		envelope.Context.Planner.Task.ID != checkpoint.Task.ID {
		return false
	}
	var question struct {
		Conversation []requestMessage `json:"conversation"`
	}
	if json.Unmarshal([]byte(envelope.Question), &question) != nil {
		return false
	}
	for _, message := range question.Conversation {
		if message.Role == "human" && "sha256:"+core.SHA256Hex([]byte(message.Text)) == checkpoint.RequestHash {
			return true
		}
	}
	return false
}

func requestRecoveryRecords(state store.Workspace, operationID string) (plannerAttemptEvidence,
	plannerTerminalEvidence, plannerCleanupEvidence, time.Time, error) {
	var initial, session, send plannerAttemptEvidence
	var terminal plannerTerminalEvidence
	var cleanup plannerCleanupEvidence
	var sessionAt time.Time
	var terminalAt time.Time
	var cleanupAt time.Time
	attempts, sessions, sends, terminals, cleanups := 0, 0, 0, 0, 0
	terminalSequence, cleanupSequence := uint64(0), uint64(0)
	attemptSequence, sessionSequence, sendSequence := uint64(0), uint64(0), uint64(0)
	sequences := map[uint64]bool{}
	var lastAt time.Time
	recovered := false
	for _, record := range state.Records.Requests {
		if record.RequestID != operationID || record.Kind != "planning" {
			continue
		}
		if record.Sequence == 0 || sequences[record.Sequence] || record.At.IsZero() ||
			!lastAt.IsZero() && record.At.Before(lastAt) {
			return initial, terminal, cleanup, time.Time{}, fmt.Errorf("request recovery found ambiguous receipt ordering")
		}
		sequences[record.Sequence] = true
		lastAt = record.At
		if core.SHA256Hex([]byte(record.Payload)) != record.Hash {
			return initial, terminal, cleanup, time.Time{}, fmt.Errorf("request recovery found a corrupt append-only receipt")
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(record.Payload), &fields); err != nil {
			return initial, terminal, cleanup, time.Time{}, err
		}
		if _, ok := fields["phase"]; ok {
			var attempt plannerAttemptEvidence
			if err := json.Unmarshal([]byte(record.Payload), &attempt); err != nil {
				return initial, terminal, cleanup, time.Time{}, err
			}
			switch attempt.Phase {
			case "attempt":
				if attempts != 0 {
					if !(terminals == 1 && cleanups == 1 && cleanup.Stopped && !cleanup.PostStopError) && !recovered {
						return initial, terminal, cleanup, time.Time{}, fmt.Errorf("earlier Planner attempt remains unresolved")
					}
					attempts, sessions, sends, terminals, cleanups = 0, 0, 0, 0, 0
					terminal, cleanup = plannerTerminalEvidence{}, plannerCleanupEvidence{}
					sessionAt, terminalAt, cleanupAt = time.Time{}, time.Time{}, time.Time{}
					terminalSequence, cleanupSequence = 0, 0
					recovered = false
				}
				initial, attempts = attempt, attempts+1
				attemptSequence = record.Sequence
			case "session":
				session, sessionAt, sessions = attempt, record.At, sessions+1
				sessionSequence = record.Sequence
			case "send-intent":
				send, sends = attempt, sends+1
				sendSequence = record.Sequence
			}
		} else if _, ok := fields["terminalEvent"]; ok {
			var candidate plannerTerminalEvidence
			if err := json.Unmarshal([]byte(record.Payload), &candidate); err != nil {
				return initial, terminal, cleanup, time.Time{}, err
			}
			if candidate.ObservedAt.IsZero() || candidate.ObservedAt.After(record.At) {
				return initial, terminal, cleanup, time.Time{}, fmt.Errorf("request recovery found an invalid terminal receipt time")
			}
			terminal, terminals, terminalSequence = candidate, terminals+1, record.Sequence
			terminalAt = record.At
		} else if _, ok := fields["stopped"]; ok {
			var candidate plannerCleanupEvidence
			if err := json.Unmarshal([]byte(record.Payload), &candidate); err != nil {
				return initial, terminal, cleanup, time.Time{}, err
			}
			cleanup, cleanups, cleanupSequence, cleanupAt = candidate, cleanups+1, record.Sequence, record.At
		} else if _, ok := fields["previewHash"]; ok {
			var application requestRecoveryApplication
			if json.Unmarshal([]byte(record.Payload), &application) == nil &&
				application.AttemptID == initial.AttemptID && application.PreviewHash != "" {
				recovered = true
			}
		}
	}
	if attempts == 0 && sessions == 0 && sends == 0 && terminals == 0 && cleanups == 0 {
		return initial, terminal, cleanup, time.Time{}, errNoRecordedPlannerAttempt
	}
	if attempts != 1 || sessions != 1 || sends != 1 || terminals > 1 || cleanups > 1 ||
		attemptSequence >= sessionSequence || sessionSequence >= sendSequence ||
		terminals == 1 && terminalSequence <= sendSequence ||
		cleanups == 1 && cleanupSequence <= sendSequence ||
		terminals == 1 && cleanups == 1 && cleanupSequence <= terminalSequence ||
		sessionAt.IsZero() || terminals == 1 && (!terminal.ObservedAt.After(sessionAt) || !terminalAt.After(sessionAt)) ||
		terminals == 1 && cleanups == 1 && !cleanupAt.After(terminalAt) ||
		initial.AttemptID == "" || initial.OperationID != operationID ||
		initial.Phase != "attempt" || session.Phase != "session" || send.Phase != "send-intent" ||
		terminals == 1 && (terminal.Version != 1 || terminal.AttemptID != initial.AttemptID ||
			terminal.Terminal != runtime.EventTurnFinished && terminal.Terminal != runtime.EventTurnEnded) {
		return initial, terminal, cleanup, time.Time{}, fmt.Errorf("request recovery requires one unambiguous sent attempt, terminal answer and cleanup receipt")
	}
	sameBinding := func(a, b plannerAttemptEvidence) bool {
		a.Phase, b.Phase = "", ""
		a.SessionID, b.SessionID = "", ""
		a.PromptHash, b.PromptHash = "", ""
		return reflect.DeepEqual(a, b)
	}
	if !sameBinding(initial, session) || !sameBinding(initial, send) ||
		session.SessionID == "" || send.SessionID != session.SessionID ||
		send.PromptHash == "" ||
		cleanups == 1 && (cleanup.AttemptID != initial.AttemptID || cleanup.SessionID != session.SessionID) ||
		terminals == 1 && (terminal.SessionID != session.SessionID || terminal.InputHash != initial.InputHash ||
			terminal.PromptHash != send.PromptHash || terminal.SourceHash != initial.SourceHash) {
		return initial, terminal, cleanup, time.Time{}, fmt.Errorf("request recovery attempt, session, input and receipt bindings conflict")
	}
	return send, terminal, cleanup, sessionAt, nil
}

func buildRequestRecoveryPreview(state store.Workspace, checkpoint requestCheckpoint, operation store.Operation,
	request requestRecoveryInput, binding plannerAttemptEvidence, terminal plannerTerminalEvidence,
	cleanup plannerCleanupEvidence, sessionAt time.Time, turn runtime.SavedTurn,
	planner controller.PlannerContext) (requestRecoveryPreview,
	controller.QuestionAnswer, float64, string, error) {
	fail := func(reason string) (requestRecoveryPreview, controller.QuestionAnswer, float64, string, error) {
		return requestRecoveryPreview{}, controller.QuestionAnswer{}, 0, "", fmt.Errorf("request recovery: %s", reason)
	}
	if cleanup.PostStopError && !(request.Resolution == "cancel" && request.AcceptUncertainty && request.StoppedConfirmed) {
		return fail("observed post-stop activity requires confirmed uncertainty cancellation, not answer acceptance")
	}
	sideQuestion := operation.Kind == "question"
	if !state.HasGraph || operation.Kind != "request" && !sideQuestion || operation.ID != request.OperationID ||
		operation.Phase != "PLANNING" && !(sideQuestion && operation.Phase == "QUESTION") ||
		operation.FlowID != "" || operation.TaskID != checkpoint.Task.ID ||
		operation.State != "BLOCKED" && operation.State != "USAGE_EXCEEDED" &&
			!(operation.State == "RUNNING" && request.StoppedConfirmed) ||
		checkpoint.Version != 2 || checkpoint.OperationID != operation.ID ||
		!checkpoint.PlannerStarted || checkpoint.Answer != nil || checkpoint.Approved ||
		checkpoint.ProposalHash != "" || checkpoint.AuthorizationID != "" {
		return fail("requires an unapproved, interrupted version-2 Planner operation without an execution Flow")
	}
	if !sideQuestion && (operation.CheckpointHash == "" || core.SHA256Hex(operation.Checkpoint) != operation.CheckpointHash) ||
		binding.TaskID != checkpoint.Task.ID || binding.Worktree != checkpoint.Task.Worktree ||
		!filepath.IsAbs(binding.Worktree) || binding.ConfigHash == "" ||
		binding.InputHash == "" || binding.SourceHash == "" {
		return fail("checkpoint or saved attempt identity does not match the request")
	}
	task, exists := state.Graph.GraphTask(checkpoint.Task.ID)
	if sideQuestion {
		task = planner.Task
	}
	taskHash, err := core.CanonicalSHA256(task)
	if !exists || err != nil || taskHash != binding.TaskHash {
		return fail("planning Task changed since the attempt")
	}
	selectionHash, err := core.CanonicalSHA256(checkpoint.Selection)
	if err != nil || selectionHash != binding.SelectionHash {
		return fail("saved configuration selection changed since the attempt")
	}
	deadline, err := time.Parse(time.RFC3339Nano, binding.Deadline)
	if sideQuestion && operation.Deadline.IsZero() && binding.Deadline == "" {
		deadline, err = time.Time{}, nil
	}
	if err != nil || terminal.Version == 1 && terminal.ObservedAt.IsZero() || !deadline.Equal(operation.Deadline) ||
		sessionAt.IsZero() ||
		turn.SessionID != binding.SessionID || turn.HistoryHash == "" ||
		turn.InputHash != binding.PromptHash || turn.MessageID == "" || turn.TurnID == "" ||
		terminal.Version == 1 && terminal.ObservedAt.Before(turn.FinishedAt) ||
		turn.SubmittedAt.Before(sessionAt) || !deadline.IsZero() && !turn.SubmittedAt.Before(deadline) ||
		turn.FinishedAt.Before(turn.SubmittedAt) || !deadline.IsZero() && !turn.FinishedAt.Before(deadline) ||
		!turn.ShutdownAt.IsZero() && (turn.ShutdownAt.Before(turn.FinishedAt) || turn.ShutdownAt.After(time.Now())) ||
		turn.ShutdownAt.IsZero() && !(request.StoppedConfirmed && request.AcceptUncertainty && request.Resolution == "cancel") {
		return fail("saved main turn is ambiguous, incomplete, unbound or outside the original deadline")
	}
	if core.SHA256Hex([]byte(turn.Payload)) != terminal.OutputHash {
		return fail("native saved answer differs from the durable terminal receipt")
	}
	var answer controller.QuestionAnswer
	uncertainCancellation := request.StoppedConfirmed && request.AcceptUncertainty && request.Resolution == "cancel"
	if terminal.InvalidAnswer {
		if !uncertainCancellation {
			return fail("invalid native answer cannot be adopted")
		}
	} else {
		answer, err = controller.DecodeQuestionAnswer(turn.Payload)
		if err != nil || !reflect.DeepEqual(answer, terminal.Answer) {
			return fail("native saved answer conflicts with the durable receipt")
		}
	}
	sourceHash, err := controller.PlannerQuestionSourceHash(planner)
	plainQuestion := sideQuestion && planner.Observation != nil && planner.Observation.ContentHash == binding.SourceHash
	if plainQuestion {
		sourceHash, err = binding.SourceHash, nil
	}
	if err != nil {
		return fail("current saved planning context is unavailable")
	}
	if sourceHash != binding.SourceHash ||
		!plainQuestion && !uncertainCancellation && controller.ValidatePlannerAnswer(answer, planner) != nil {
		return fail("saved context drifted or the answer is no longer valid for request planning")
	}
	usageMatches := matchesQuestionUsage(terminal.Usage, turn.UsageTotals)
	checkpointMatches, checkpointFloor, checkpointErr := savedCheckpointUsage(turn)
	if checkpointErr != nil {
		return fail("saved usage checkpoint is invalid, out of scope or contradictory")
	}
	finalCheckpointMatches := len(turn.UsageCheckpoints) != 0 && checkpointMatches
	if finalCheckpointMatches && len(terminal.Usage) != 0 {
		summary, err := core.SummarizeUsage(turn.SessionID, terminal.Usage, turn.UsageCheckpoints)
		if err != nil {
			return fail("saved final checkpoint conflicts with terminal usage")
		}
		finalCheckpointMatches = summary.CreditsStatus == "complete"
	}
	usageMatches = (usageMatches || finalCheckpointMatches) && (checkpointMatches || operation.ExternalBudgetOnly())
	usageUncertain := uncertainCancellation && (len(terminal.Usage) == 0 && len(turn.UsageTotals) == 0 ||
		len(terminal.Usage) != 0 && !questionUsageTotalsConflict(terminal.Usage, turn.UsageTotals))
	if turnHasExecution(turn) || !usageMatches && !usageUncertain {
		return fail("saved turn contains tools or incomplete/conflicting usage")
	}
	if operation.Deadline.IsZero() && !sideQuestion {
		return fail("successful session quiescence or original deadline is missing")
	}
	for _, flow := range state.Graph.ReviewFlows {
		if flow.Status == "RUNNING" || flow.RunnerID != "" {
			return fail("a review Flow is active or claimed")
		}
		if flow.ID == operation.ID+"-flow" || flow.TaskID == checkpoint.Task.ID {
			return fail("the Planner attempt is associated with a review Flow")
		}
	}
	for _, dispatch := range state.Graph.Dispatches {
		if dispatch.TaskID == operation.ID || dispatch.TaskID == checkpoint.Task.ID ||
			dispatch.ID == operation.ID+"-flow" || dispatch.RelatedID == operation.ID {
			return fail("request recovery cannot associate a Planner turn with a Dispatch or Flow")
		}
	}
	terminalSummary, err := core.SummarizeUsage(turn.SessionID, terminal.Usage, terminal.UsageCheckpoints)
	if err != nil {
		return fail("saved usage is invalid")
	}
	total, usageStatus := 0.0, "unknown"
	if terminalSummary.NanoAIU != nil {
		total = *terminalSummary.NanoAIU
	}
	if terminalSummary.CreditsStatus == "complete" {
		usageStatus = "complete"
	}
	total = max(total, checkpointFloor)
	if finalCheckpointMatches {
		usageStatus = "complete"
	}
	if !checkpointMatches {
		usageStatus = "unknown"
	}
	if math.IsNaN(total) || math.IsInf(total, 0) {
		return fail("saved checkpoint cost is invalid")
	}
	if uncertainCancellation && !usageMatches {
		usageStatus = "unknown"
	}
	if usageStatus != "complete" && !operation.ExternalBudgetOnly() && !uncertainCancellation {
		return fail("native usage is incomplete under the approved numeric budget")
	}
	hashState := state
	if state.Records.Operation != nil {
		operationCopy := *state.Records.Operation
		operationCopy.Owner = ""
		hashState.Records.Operation = &operationCopy
	}
	hash, err := core.CanonicalSHA256(struct {
		State   store.Workspace
		Request requestRecoveryInput
		Binding plannerAttemptEvidence
		Answer  string
		History string
		Usage   string
	}{hashState, request, binding, terminal.OutputHash, turn.HistoryHash, usageStatus})
	if err != nil {
		return fail("could not bind the exact recovery preview")
	}
	title := ""
	if answer.Proposal != nil {
		title = answer.Proposal.Title
	}
	cleanupStatus := "cleanup receipt missing; native routine shutdown verified"
	bindingStatus := "durable attempt and sent-input binding"
	var uncertainties []string
	if uncertainCancellation {
		uncertainties = append(uncertainties, "saved advice is retained as unaccepted evidence; answer validity is not execution approval")
	}
	if binding.Version == 2 {
		bindingStatus = "explicit legacy association; historical receipt not fabricated"
		uncertainties = append(uncertainties, "historical runtime configuration was not durably bound; saved advice is not execution authority")
	}
	if turn.ShutdownAt.IsZero() {
		cleanupStatus = "original process stop explicitly confirmed by cited evidence; native routine shutdown absent"
		uncertainties = append(uncertainties, "native aggregate usage unavailable; cumulative known usage retained and unknown usage not reset to zero")
	}
	if cleanup.AttemptID != "" && cleanup.Stopped {
		cleanupStatus = "cleanup stopped; native routine shutdown verified"
	} else if cleanup.AttemptID != "" {
		cleanupStatus = "cleanup reported failure; native routine shutdown verified"
	}
	transitions := []string{
		"adopt the exact on-time saved Planner answer into the original checkpoint",
		"deduplicate its native usage without changing the saved deadline or budget",
		"leave HUMAN_REQUIRED as questions and do not authorize execution",
	}
	if request.Resolution == "cancel" {
		transitions = append(transitions, "terminalize only this operation as CANCELLED; retain the checkpoint, original evidence and uncertainty; future work requires ordinary gates")
	}
	if sideQuestion {
		transitions[0] = "retain the exact saved side-question answer as advice, not Task or execution approval"
		transitions[2] = "complete only the advisory operation after reconciliation; preserve original evidence and require ordinary future execution gates"
	}
	return requestRecoveryPreview{
		Version: 1, OperationID: operation.ID, TaskID: checkpoint.Task.ID,
		AttemptID: binding.AttemptID, SessionID: binding.SessionID, HistoryHash: turn.HistoryHash,
		Recommendation: answer.Recommendation, Questions: answer.Questions, ProposalTitle: title,
		UsageStatus: usageStatus, CleanupStatus: cleanupStatus,
		BindingStatus: bindingStatus, Uncertainties: uncertainties, Resolution: request.Resolution,
		Transitions: transitions,
		Hash:        "sha256:" + hash,
	}, answer, total, usageStatus, nil
}

func turnHasExecution(turn runtime.SavedTurn) bool {
	outputs := 0
	for _, event := range turn.Events {
		if !runtime.QuestionEventAllowed(event.Type) {
			return true
		}
		switch event.Type {
		case runtime.EventOutput:
			if strings.TrimSpace(event.Payload) != "" {
				outputs++
			}
		}
	}
	return outputs != 1
}

func matchesQuestionUsage(calls, totals []core.DispatchUsage) bool {
	matches, _ := compareQuestionUsage(calls, totals)
	return matches
}

func questionUsageTotalsConflict(calls, totals []core.DispatchUsage) bool {
	_, conflict := compareQuestionUsage(calls, totals)
	return conflict
}

func compareQuestionUsage(calls, totals []core.DispatchUsage) (bool, bool) {
	return core.CompareUsage(calls, totals)
}

func savedCheckpointUsage(turn runtime.SavedTurn) (bool, float64, error) {
	var calls []core.DispatchUsage
	var checkpoints []runtime.UsageCheckpoint
	for _, event := range turn.Events {
		if event.Type == runtime.EventUsage {
			var call core.DispatchUsage
			if err := core.DecodeGraphInput(strings.NewReader(event.Payload), &call); err != nil {
				return false, 0, err
			}
			if err := core.AppendUsage(&calls, call); err != nil {
				return false, 0, err
			}
		}
		if event.Type == runtime.EventUsageCheckpoint {
			checkpoint, _, err := runtime.DecodeUsageCheckpoint(event.Payload, turn.SessionID, calls)
			if err != nil {
				return false, 0, err
			}
			checkpoints = append(checkpoints, checkpoint)
		}
	}
	checkpoints = append(checkpoints, turn.UsageCheckpoints...)
	if len(checkpoints) == 0 {
		return true, 0, nil
	}
	summary, err := core.SummarizeUsage(turn.SessionID, calls, checkpoints)
	if err != nil {
		return false, 0, err
	}
	floor := 0.0
	if summary.NanoAIU != nil {
		floor = *summary.NanoAIU
	}
	finalKnown, finalStatus, err := plannerUsageTotal(turn.UsageTotals)
	if err != nil {
		return false, 0, err
	}
	if finalStatus == "complete" && len(turn.UsageTotals) != 0 && floor > finalKnown+1e-6 {
		return false, 0, fmt.Errorf("checkpoint exceeds final aggregate")
	}
	finalSummary, err := core.SummarizeUsage("", turn.UsageTotals, nil)
	if err != nil {
		return false, 0, err
	}
	if finalStatus == "complete" && summary.CreditsStatus == "complete" && math.Abs(floor-finalKnown) > 1e-6 ||
		finalSummary.TokenStatus == "complete" && summary.InputOutputTokens != nil &&
			(*summary.InputOutputTokens > *finalSummary.InputOutputTokens ||
				summary.TokenStatus == "complete" && *summary.InputOutputTokens != *finalSummary.InputOutputTokens) {
		return false, 0, fmt.Errorf("final aggregate conflicts with saved checkpoint")
	}
	for _, checkpoint := range checkpoints {
		if len(turn.UsageTotals) != 0 && core.UsageBelowKnown(checkpoint.UsageTotals, turn.UsageTotals) {
			return false, 0, fmt.Errorf("checkpoint conflicts with final aggregate")
		}
	}
	return summary.CreditsStatus == "complete", floor, nil
}

func recordRecoveredPlannerUsage(state *store.Workspace, operation *store.Operation,
	attemptID string, usage []core.DispatchUsage, amount float64, usageState string, checkpoints ...runtime.UsageCheckpoint) error {
	hash, err := plannerUsageHash(usage, checkpoints)
	if err != nil {
		return err
	}
	var prior *plannerUsageReceipt
	for _, record := range state.Records.Requests {
		if record.RequestID != operation.ID || record.Kind != "planning" {
			continue
		}
		var previous plannerUsageReceipt
		if json.Unmarshal([]byte(record.Payload), &previous) != nil ||
			previous.Version != 1 || previous.AttemptID != attemptID || previous.UsageHash == "" {
			continue
		}
		copy := previous
		prior = &copy
	}
	delta := amount
	if prior != nil {
		if prior.UsageHash == hash && prior.Amount == amount && prior.State == usageState {
			return nil
		}
		if prior.State == "complete" && usageState == "complete" && math.Abs(prior.Amount-amount) > 1e-6 ||
			usageState != "unknown" && amount < prior.Amount {
			return fmt.Errorf("request recovery found conflicting usage accounting for the same attempt")
		}
		if usageState == "unknown" {
			amount = math.Max(amount, prior.Amount)
		}
		delta = amount - prior.Amount
	}
	operation.PlanningNanoAIU += delta
	if math.IsInf(operation.PlanningNanoAIU, 0) {
		return fmt.Errorf("request recovery cumulative usage overflowed")
	}
	if usageState == "unknown" {
		operation.PlanningUsageState = "unknown"
	}
	payload, err := json.Marshal(plannerUsageReceipt{1, attemptID, hash, amount, usageState})
	if err != nil {
		return err
	}
	appendRequestRecord(state, operation.ID, "planning", payload)
	return nil
}

func appendRequestRecord(state *store.Workspace, id, kind string, payload []byte) {
	sequence := uint64(1)
	for _, record := range state.Records.Requests {
		if record.RequestID == id && record.Kind == kind {
			sequence++
		}
	}
	state.Records.Requests = append(state.Records.Requests, store.RequestRecord{
		RequestID: id, Kind: kind, Sequence: sequence, Payload: string(payload),
		Hash: core.SHA256Hex(payload), At: time.Now().UTC(),
	})
}

func priorRequestRecovery(state store.Workspace, id, expectedHash string,
	request requestRecoveryInput) (requestRecoveryApplication, bool, error) {
	requestHash, err := core.CanonicalSHA256(request)
	if err != nil {
		return requestRecoveryApplication{}, false, err
	}
	for _, record := range state.Records.Requests {
		if record.RequestID != id || record.Kind != "planning" ||
			core.SHA256Hex([]byte(record.Payload)) != record.Hash {
			continue
		}
		var applied requestRecoveryApplication
		if json.Unmarshal([]byte(record.Payload), &applied) != nil ||
			applied.Version != 1 || applied.PreviewHash == "" {
			continue
		}
		if applied.PreviewHash == expectedHash && applied.RequestHash == requestHash {
			return applied, true, nil
		}
	}
	return requestRecoveryApplication{}, false, nil
}
