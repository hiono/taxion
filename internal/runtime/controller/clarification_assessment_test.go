package controller

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func TestClarificationConflictsRemainUniqueAndUnresolvedAfterRestart(t *testing.T) {
	db, path, _, _ := questionFixture(t)
	input := clarificationRequest(t, db, "task", "dispatch", "CODER")
	before, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RequestClarification(db, input); err != nil {
		t.Fatal(err)
	}
	original := ClarificationAnswer{Role: "HUMAN", Actor: "owner", Source: "decision:original", Text: "Preserve the current behavior."}
	first, err := AnswerClarification(db, input.ID, original)
	if err != nil {
		t.Fatal(err)
	}
	alternative := ClarificationAnswer{Role: "HUMAN", Actor: "owner", Source: "decision:alternative",
		Text: "Change the current behavior.", At: time.Unix(1, 0)}
	var workers sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, err := AnswerClarification(db, input.ID, alternative)
			if err == nil || !strings.Contains(err.Error(), "retained as OBSERVED") {
				errs <- fmt.Errorf("unexpected conflicting-answer result: %v", err)
			}
		}()
	}
	workers.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if _, err := AnswerClarification(db, input.ID, original); err == nil {
		t.Fatal("original answer replay silently resolved its conflict")
	}
	history, err := ClarificationHistory(db)
	if err != nil || len(history) != 3 || !reflect.DeepEqual(history[2].Answer, first.Answer) ||
		history[2].Conflict == nil || history[2].Conflict.Source != alternative.Source ||
		history[2].Conflict.Text != alternative.Text || history[2].Conflict.At.Equal(alternative.At) {
		t.Fatal("conflict lost provenance, replaced the original answer or duplicated", history, err)
	}
	other := alternative
	other.Text = "Another incompatible interpretation."
	if _, err := AnswerClarification(db, input.ID, other); err == nil {
		t.Fatal("new conflict accepted")
	}
	if _, err := AnswerClarification(db, input.ID, alternative); err == nil {
		t.Fatal("old conflict retry accepted")
	}
	second := input
	second.ID, second.Uncertainty = "second-question", "another-interpretation"
	if _, err := RequestClarification(db, second); err != nil {
		t.Fatal(err)
	}
	if _, err := AnswerClarification(db, second.ID, original); err != nil {
		t.Fatal(err)
	}
	if _, err := AnswerClarification(db, second.ID, alternative); err == nil {
		t.Fatal("the first question's conflict suppressed evidence on another question")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	input.ID = "new-session-retry"
	replayed, err := RequestClarification(reopened, input)
	if err != nil || replayed.Request.ID != "question" || replayed.Conflict == nil ||
		!reflect.DeepEqual(replayed.Answer, first.Answer) || replayed.State != "OBSERVED" {
		t.Fatal("restart reissued or implicitly resolved the question", replayed, err)
	}
	report, err := AssessClarification(reopened, replayed.Request.ID)
	if err != nil || report.State != "OBSERVED" || !slices.ContainsFunc(report.Decision.Conditions, func(c core.InputCondition) bool {
		return c.Code == "clarification-conflict"
	}) {
		t.Fatal("conflicting answer entered input admission", report, err)
	}
	after, err := reopened.LoadWorkspace()
	if err != nil || len(after.Records.Requests) != len(before.Records.Requests)+7 {
		t.Fatal("concurrent/restarted retries appended duplicate evidence", err)
	}
	history, err = ClarificationHistory(reopened)
	if err != nil || history[len(history)-1].Request.ID != second.ID || history[len(history)-1].Conflict == nil {
		t.Fatal("conflict evidence was not isolated to its question", history, err)
	}
	after.Records.Requests = before.Records.Requests
	if !reflect.DeepEqual(before, after) {
		t.Fatal("conflict recording changed approval, Dispatch, HOLD or operation ownership")
	}
}

func TestClarificationAssessmentObservesDriftWithoutStateChanges(t *testing.T) {
	for _, scenario := range []string{"current", "unanswered", "scope", "session", "dispatch", "task", "deadline", "authority", "reuse"} {
		t.Run(scenario, func(t *testing.T) {
			db, _, _, _ := questionFixture(t)
			input := clarificationRequest(t, db, "task", "dispatch", "CODER")
			if scenario == "scope" || scenario == "task" {
				if err := db.UpdateWorkspace(func(state *store.Workspace) error {
					prior := state.Graph.Tasks[0]
					task := core.GraphTask{
						ID: "draft-question", Title: "Clarify before approval", Repo: prior.Repo,
						Type: "IMPLEMENTATION", Issue: "Issue #283", Status: core.Draft, Owner: "CODER", Reviewer: "REVIEWER",
						CreatedFrom: "human:assessment", Objective: "Preserve the accepted contract",
						Scope: []string{"README.md"}, Acceptance: []string{"approved behavior"},
						Worktree: prior.Worktree, Branch: prior.Branch, BaselineSHA: prior.BaselineSHA,
					}
					if err := state.Graph.Track(task, "human", "create a draft for clarification"); err != nil {
						return err
					}
					state.Ledger.Tasks = append(state.Ledger.Tasks, core.Task{ID: task.ID, Title: task.Title, Repo: task.Repo})
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				input = clarificationRequest(t, db, "draft-question", "", "PLANNER")
			}
			switch scenario {
			case "deadline":
				input.Deadline = time.Now().Add(-time.Hour)
			case "authority":
				input.Category = "authority"
			case "reuse":
				input.Category, input.SavedSourceID = "saved", "taxion:clarification-task"
			}
			if _, err := RequestClarification(db, input); err != nil {
				t.Fatal(err)
			}
			if scenario != "unanswered" && scenario != "deadline" && scenario != "reuse" {
				if _, err := AnswerClarification(db, input.ID, ClarificationAnswer{
					Role: "HUMAN", Actor: "owner", Source: "human:interpretation", Text: "Preserve the original behavior.",
				}); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.UpdateWorkspace(func(state *store.Workspace) error {
				switch scenario {
				case "scope":
					task, _ := state.Graph.GraphTask(input.TaskID)
					task.Scope = append(task.Scope, "changed")
					return state.Graph.Track(task, "human", "revise the draft scope")
				case "session":
					return state.Graph.BindRuntimeSession("dispatch", "different-session", "different-config")
				case "dispatch":
					return state.Graph.TransitionDispatch("dispatch", core.DispatchAcknowledged, "human", "acknowledge the issued dispatch", true, false, nil)
				case "task":
					return state.Graph.Transition(input.TaskID, core.Blocked, "human", "block the draft", core.TransitionProof{})
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			before, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			report, err := AssessClarification(db, input.ID)
			expected := map[string]string{
				"unanswered": "clarification-unanswered", "scope": "clarification-context",
				"session": "clarification-session", "dispatch": "clarification-session",
				"task": "clarification-task", "deadline": "clarification-deadline", "authority": "clarification-human-approval",
			}[scenario]
			if err != nil || report.ID != input.ID || report.State != "OBSERVED" || report.Receipt.Hash == "" ||
				report.Decision.BasisHash == "" || expected == "" && report.Decision.Verdict != "accept" ||
				expected != "" && (report.Decision.Verdict != "blocked" ||
					!slices.ContainsFunc(report.Decision.Conditions, func(c core.InputCondition) bool { return c.Code == expected })) {
				t.Fatal("wrong assessment of saved evidence", report, err)
			}
			after, err := db.LoadWorkspace()
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("read-only assessment mutated the workspace", err)
			}
		})
	}
}
