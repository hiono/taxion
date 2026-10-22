package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/store"
)

func TestRequestCheckpointRequiresFormalBoundApprovalBeforeResume(t *testing.T) {
	for _, mode := range []string{"approved-flag", "missing-approval", "proposal", "task", "operation", "operation-task", "authorization", "plan-drift", "preflight", "bytes", "display-flag", "matching"} {
		t.Run(mode, func(t *testing.T) {
			root, args := requestFixture(t)
			approved := mode != "approved-flag" && mode != "missing-approval"
			input := "/plan\n"
			if approved {
				input += "/approve\n"
			}
			runs := 0
			runner := func(context.Context, []string, io.Writer) error { runs++; return nil }
			if err := requestCommand(append(args, "add search"), strings.NewReader(input), io.Discard,
				fixturePlan(t, requestFixtureAnswer([]string{})), runner); err != nil {
				t.Fatal(err)
			}
			db, state, err := loadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var checkpoint requestCheckpoint
			if err := json.Unmarshal(state.Records.Operation.Checkpoint, &checkpoint); err != nil {
				t.Fatal(err)
			}
			err = db.UpdateWorkspace(func(state *store.Workspace) error {
				switch mode {
				case "approved-flag":
					checkpoint.Approved = true
				case "proposal":
					checkpoint.ProposalHash = "sha256:" + strings.Repeat("a", 64)
				case "task":
					checkpoint.Task.ID = "different-planning"
				case "operation":
					checkpoint.OperationID = "different"
				case "operation-task":
					state.Records.Operation.TaskID = "different"
				case "authorization":
					checkpoint.AuthorizationID = "different-authorization"
				case "missing-approval":
					checkpoint.Approved = true
					checkpoint.AuthorizationID = "intake-confirmation:" + state.Records.Operation.ID
					checkpoint.ProposalHash = "sha256:" + strings.Repeat("a", 64)
					state.Records.Operation.TaskID = state.Records.Operation.ID
					state.Records.Operation.FlowID = state.Records.Operation.ID + "-flow"
				case "preflight":
					checkpoint.Preflight.TaskID = "different-task"
				case "plan-drift":
					checkpoint.Answer.Proposal.Objective = "a different contract"
				case "bytes", "display-flag":
					checkpoint.Approved = false
				}
				data, err := json.Marshal(checkpoint)
				if err != nil {
					return err
				}
				state.Records.Operation.Checkpoint = data
				if mode != "bytes" {
					// Even a recomputed local checksum cannot replace formal approval.
					state.Records.Operation.CheckpointHash = core.SHA256Hex(data)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			before, _ := db.LoadWorkspace()
			tree := gitTest(t, root, "status", "--porcelain")
			oldRuns := runs
			err = requestCommand([]string{"--resume", before.Records.Operation.ID}, strings.NewReader(""), io.Discard, nil, runner)
			after, loadErr := db.LoadWorkspace()
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if mode == "matching" || mode == "display-flag" {
				if err != nil || runs != oldRuns+1 || !reflect.DeepEqual(before.Graph, after.Graph) {
					t.Fatal("matching formal approval could not resume", err)
				}
			} else if err == nil || runs != oldRuns || !reflect.DeepEqual(before, after) ||
				gitTest(t, root, "status", "--porcelain") != tree {
				t.Fatal("invalid checkpoint changed Task, Dispatch, runtime, worktree or state", err)
			}
		})
	}
}

func TestRequestMinimalPlanRejectsCredentialBeforeTaskCreation(t *testing.T) {
	_, args := requestFixture(t)
	secret := "github_pat_" + strings.Repeat("z", 24)
	answer := requestFixtureAnswer([]string{})
	answer.Proposal.Objective = "invalid plan " + secret
	if err := requestCommand(append(args, "add search"), strings.NewReader("/plan\n/approve\n"), io.Discard,
		fixturePlan(t, answer), nil); err == nil {
		t.Fatal("credential-like plan was accepted")
	}
	db, state, err := loadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	data, err := json.Marshal(state)
	if err != nil || bytes.Contains(data, []byte(secret)) || len(state.Graph.Dispatches) != 0 || len(state.Graph.Tasks) != 1 {
		t.Fatal("rejected private plan persisted content or created execution", err)
	}
}

func TestRequestApprovalRejectsAuthoritativeRecordAndExecutionDrift(t *testing.T) {
	_, args := requestFixture(t)
	if err := requestCommand(append(args, "add search"), strings.NewReader("/plan\n/approve\n"), io.Discard,
		fixturePlan(t, requestFixtureAnswer([]string{})), func(context.Context, []string, io.Writer) error { return nil }); err != nil {
		t.Fatal(err)
	}
	db, original, err := loadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	saved, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"missing-confirmation", "confirmation-operation", "confirmation-basis", "contract", "flow-contract", "budget", "deadline", "repair", "usage"} {
		t.Run(mode, func(t *testing.T) {
			var state store.Workspace
			if err := json.Unmarshal(saved, &state); err != nil {
				t.Fatal(err)
			}
			operation := state.Records.Operation
			var checkpoint requestCheckpoint
			if err := json.Unmarshal(operation.Checkpoint, &checkpoint); err != nil {
				t.Fatal(err)
			}
			for i := range state.Graph.Tasks {
				task := &state.Graph.Tasks[i]
				if task.ID != operation.TaskID {
					continue
				}
				if mode == "contract" {
					task.Objective = "changed product contract"
				}
				for j := range task.Decisions {
					decision := &task.Decisions[j]
					if decision.ID != checkpoint.AuthorizationID {
						continue
					}
					switch mode {
					case "missing-confirmation":
						decision.ID = "unrelated-decision"
					case "confirmation-operation", "confirmation-basis":
						var confirmation controller.IntakeConfirmation
						if err := json.Unmarshal([]byte(decision.Text), &confirmation); err != nil {
							t.Fatal(err)
						}
						if mode == "confirmation-operation" {
							confirmation.RequestOperationID = "another-operation"
						} else {
							confirmation.RequestBasisHash = strings.Repeat("a", 64)
						}
						text, err := json.Marshal(confirmation)
						if err != nil {
							t.Fatal(err)
						}
						decision.Text = string(text)
					}
				}
			}
			flow := &state.Graph.ReviewFlows[0]
			switch mode {
			case "flow-contract":
				flow.Contract.Objective = "different execution contract"
			case "budget":
				flow.Budget.MaxNanoAIU = operation.MaxNanoAIU + 1
			case "deadline":
				flow.Deadline = operation.Deadline.Add(time.Hour)
			case "repair":
				flow.MaxRepairs++
			case "usage":
				operation.PlanningUsageState = "unknown"
			}
			before, _ := json.Marshal(state)
			if _, err := verifyRequestCheckpoint(state, *operation, checkpoint); err == nil {
				t.Fatal("checkpoint accepted missing authorization or authoritative drift")
			}
			after, _ := json.Marshal(state)
			if !bytes.Equal(before, after) {
				t.Fatal("verification changed saved state")
			}
		})
	}
}

func TestRequestPersistentBytesContainOnlyMinimalPlanAndDigests(t *testing.T) {
	_, args := requestFixture(t)
	secret := "ghp_" + strings.Repeat("z", 24)
	prompt := "private original request " + secret
	response := "private response and reasoning " + secret
	answer := requestFixtureAnswer([]string{})
	answer.Assumptions = []string{response}
	if err := requestCommand(append(args, prompt), strings.NewReader("/plan\n/approve\n"), io.Discard,
		fixturePlan(t, answer), func(context.Context, []string, io.Writer) error { return nil }); err != nil {
		t.Fatal(err)
	}
	db, state, err := loadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{prompt, response, secret, `"conversation"`, `"committedContext"`, `"committedFilesByDirectory"`} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatal("workspace JSON retained private input/result", forbidden)
		}
	}
	var checkpoint requestCheckpoint
	if err := json.Unmarshal(state.Records.Operation.Checkpoint, &checkpoint); err != nil ||
		checkpoint.Version != 2 || checkpoint.RequestHash == "" || checkpoint.Answer.Proposal == nil ||
		checkpoint.AuthorizationID == "" || len(checkpoint.Answer.Assumptions) != 0 {
		t.Fatal("minimal checkpoint lost its plan/approval identity", err)
	}
	for _, receipt := range state.Records.Requests {
		if receipt.Kind != "planning" {
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(receipt.Payload), &fields); err != nil ||
			fields["input"] != nil || fields["result"] != nil || fields["inputHash"] == nil || fields["resultHash"] == nil {
			t.Fatal("planning receipt retained full input/result", err)
		}
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		data, err := os.ReadFile(workspaceDBPath() + suffix)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(secret)) || bytes.Contains(data, []byte(response)) {
			t.Fatal("SQLite persisted secret/conversation bytes")
		}
	}
}

func TestRequestLegacyCheckpointReadbackDiscardsTranscriptAndFailsClosed(t *testing.T) {
	for _, version := range []int{0, 1, 99} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			_, args := requestFixture(t)
			if err := requestCommand(append(args, "add search"), strings.NewReader("/plan\n"), io.Discard,
				fixturePlan(t, requestFixtureAnswer([]string{})), nil); err != nil {
				t.Fatal(err)
			}
			db, state, err := loadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			legacy := []byte(fmt.Sprintf(`{"version":%d,"approved":true,"conversation":[{"text":"old-private-transcript"}],"request":"old-private-transcript","args":["old-private-transcript"],"task":{"objective":"old-private-transcript"}}`, version))
			var checkpoint requestCheckpoint
			if err := json.Unmarshal(legacy, &checkpoint); err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(checkpoint)
			if err != nil || bytes.Contains(data, []byte("old-private-transcript")) {
				t.Fatal("legacy readback retained private content", err)
			}
			if err := db.UpdateWorkspace(func(current *store.Workspace) error {
				current.Records.Operation.Checkpoint = legacy
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			before, _ := db.LoadWorkspace()
			if err := requestCommand([]string{"--resume", state.Records.Operation.ID}, nil, io.Discard, nil, nil); err == nil {
				t.Fatal("legacy/future checkpoint supplied approval")
			}
			after, _ := db.LoadWorkspace()
			if !reflect.DeepEqual(before, after) {
				t.Fatal("legacy checkpoint was migrated/resaved during rejection")
			}
		})
	}
}

func TestRequestCancellationPreservesTaskAndRefusesActiveWorkflow(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(map[bool]string{false: "waiting", true: "active-flow"}[active], func(t *testing.T) {
			_, args := requestFixture(t)
			question := []string{"Which behavior?"}
			input := "/plan\n"
			if active {
				question, input = []string{}, "/plan\n/approve\n"
			}
			runner := func(context.Context, []string, io.Writer) error { return nil }
			if err := requestCommand(append(args, "add search"), strings.NewReader(input), io.Discard,
				fixturePlan(t, requestFixtureAnswer(question)), runner); err != nil {
				t.Fatal(err)
			}
			db, before, err := loadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var output bytes.Buffer
			err = requestCommand([]string{"--cancel", before.Records.Operation.ID}, nil, &output, nil, nil)
			if active == (err == nil) {
				t.Fatal("cancellation did not distinguish idle and active execution", err)
			}
			after, err := db.LoadWorkspace()
			if err != nil || !reflect.DeepEqual(before.Graph, after.Graph) || !reflect.DeepEqual(before.Ledger, after.Ledger) {
				t.Fatal("cancellation changed a Task, decision, approval or Dispatch", err)
			}
			if !active && (after.Records.Operation.State != "CANCELLED" || after.Records.Operation.Owner != "") {
				t.Fatal("idle cancellation retained the active operation")
			}
		})
	}
}

func TestRequestResumesUnansweredCheckpointWithoutRepeatingPlanner(t *testing.T) {
	_, args := requestFixture(t)
	calls := 0
	planner := func(ctx context.Context, db *store.Store, selection config.Config, task core.GraphTask,
		text string, credits float64, cli, base string) (controller.QuestionResult, error) {
		calls++
		if calls == 2 && !strings.Contains(text, "partial match") {
			t.Fatal("resumed Planner did not receive the answer")
		}

		return fixturePlan(t, requestFixtureAnswer([]string{"Which match behavior?"}))(ctx, db, selection, task, text, credits, cli, base)
	}
	var out bytes.Buffer
	if err := requestCommand(append(args, "add search"), strings.NewReader("/plan\n"), &out, planner, nil); err != nil {
		t.Fatal(err)
	}
	db, state, err := loadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	operation := state.Records.Operation
	if operation == nil || operation.State != "WAITING_FOR_ANSWER" || operation.Owner != "" {
		t.Fatal("EOF lost the waiting operation", operation)
	}
	if err := requestCommand(append(args, "different workflow"), strings.NewReader("/plan\n"), io.Discard, planner, nil); err == nil {
		t.Fatal("a different request displaced the pending question")
	}
	if err := requestCommand([]string{"--resume", operation.ID}, strings.NewReader("/plan\n"), io.Discard, planner, nil); err == nil {
		t.Fatal("answer-wait accepted a workflow command")
	}
	if calls != 1 {
		t.Fatal("resume repeated a paid Planner before receiving the answer")
	}
	out.Reset()
	if err := requestCommand([]string{"--status"}, nil, &out, nil, nil); err != nil {
		t.Fatal(err)
	}
	var status struct {
		Operation store.Operation `json:"operation"`
	}
	if err := json.Unmarshal(out.Bytes(), &status); err != nil ||
		status.Operation.State != "WAITING_FOR_ANSWER" || status.Operation.MaxNanoAIU != 1e9 ||
		status.Operation.Owner != "" || status.Operation.Checkpoint != nil {
		t.Fatal("status lacks machine-readable ceilings or discloses the owner/checkpoint", out.String(), err)
	}
	if err := requestCommand([]string{"--resume", operation.ID}, strings.NewReader("partial match\n/quit\n"), io.Discard, planner, nil); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("answer did not continue the saved Planner conversation", calls)
	}
	state, _ = db.LoadWorkspace()
	if state.Records.Operation.State != "CANCELLED" || len(state.Graph.Dispatches) != 0 {
		t.Fatal("answer or cancellation authorized execution")
	}
}

func TestRequestExpiredCheckpointStopsWithoutRepeatingPlanner(t *testing.T) {
	_, args := requestFixture(t)
	args = append(args, "--timeout", "1s")
	calls := 0
	planner := func(ctx context.Context, db *store.Store, selection config.Config, task core.GraphTask,
		text string, credits float64, cli, base string) (controller.QuestionResult, error) {
		calls++
		return fixturePlan(t, requestFixtureAnswer([]string{"Which behavior?"}))(ctx, db, selection, task, text, credits, cli, base)
	}
	if err := requestCommand(append(args, "add search"), strings.NewReader("/plan\n"), io.Discard, planner, nil); err != nil {
		t.Fatal(err)
	}
	db, before, err := loadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	operation := before.Records.Operation
	time.Sleep(time.Until(operation.Deadline) + time.Millisecond)
	if err := requestCommand([]string{"--resume", operation.ID}, strings.NewReader("answer\n"), io.Discard, planner, nil); err == nil {
		t.Fatal("expired checkpoint allowed another paid call")
	}
	after, err := db.LoadWorkspace()
	if err != nil || calls != 1 || after.Records.Operation.State != "USAGE_EXCEEDED" ||
		!after.Records.Operation.Deadline.Equal(operation.Deadline) ||
		!reflect.DeepEqual(before.Graph, after.Graph) {
		t.Fatal("deadline stop was not durable or changed the contract", err)
	}
}

func TestRequestUnknownPlanningUsageStopsBeforeAnotherPaidCall(t *testing.T) {
	_, args := requestFixture(t)
	calls := 0
	planner := func(context.Context, *store.Store, config.Config, core.GraphTask, string, float64, string, string) (controller.QuestionResult, error) {
		calls++
		answer := requestFixtureAnswer([]string{"Which behavior?"})
		return controller.QuestionResult{Answer: &answer}, nil
	}
	if err := requestCommand(append(args, "add search"), strings.NewReader("/plan\nanswer\n"), io.Discard, planner, nil); err == nil {
		t.Fatal("unknown usage allowed another paid call")
	}
	db, state, err := loadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if calls != 1 || state.Records.Operation.State != "USAGE_EXCEEDED" ||
		state.Records.Operation.PlanningUsageState != "unknown" || len(state.Graph.Dispatches) != 0 {
		t.Fatal("hard stop lost its state or issued work", calls)
	}
}
