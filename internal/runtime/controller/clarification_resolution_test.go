package controller

import (
	"fmt"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func TestClarificationResolutionSelectsEvidenceWithoutGrantingAuthority(t *testing.T) {
	db, path, _, _ := questionFixture(t)
	request := clarificationRequest(t, db, "task", "dispatch", "CODER")
	before, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RequestClarification(db, request); err != nil {
		t.Fatal(err)
	}
	original := ClarificationAnswer{Role: "HUMAN", Actor: "owner", Source: "spec:first", Text: "Preserve behavior."}
	first, err := AnswerClarification(db, request.ID, original)
	if err != nil {
		t.Fatal(err)
	}
	alternative := original
	alternative.Source, alternative.Text = "spec:second", "Replace behavior."
	if _, err := AnswerClarification(db, request.ID, alternative); err == nil {
		t.Fatal("conflict was accepted")
	}
	report, err := AssessClarification(db, request.ID)
	if err != nil || len(report.Candidates) != 2 {
		t.Fatal(report, err)
	}
	input := ClarificationResolutionRequest{
		ID: request.ID, ExpectedHash: report.Decision.BasisHash, Selected: report.Candidates[1].Receipt,
		Role: "HUMAN", Actor: "specification-owner", Source: "decision:clarification", Reason: "The second source states the intended behavior.",
	}
	snapshot, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"stale", "foreign-question", "wrong-hash", "role", "provenance"} {
		t.Run(scenario, func(t *testing.T) {
			invalid := input
			switch scenario {
			case "stale":
				invalid.ExpectedHash = "outdated"
			case "foreign-question":
				invalid.Selected.RequestID = "clarification:other"
			case "wrong-hash":
				invalid.Selected.Hash = "different"
			case "role":
				invalid.Role = "REVIEWER"
			case "provenance":
				invalid.Source = ""
			}
			if _, err := ResolveClarificationConflict(db, invalid); err == nil {
				t.Fatal("invalid selection accepted")
			}
			after, err := db.LoadWorkspace()
			if err != nil || !reflect.DeepEqual(snapshot, after) {
				t.Fatal("rejected resolution mutated the workspace", err)
			}
		})
	}
	var workers sync.WaitGroup
	errs := make(chan error, 6)
	for range 6 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			resolved, err := ResolveClarificationConflict(db, input)
			if err != nil || resolved.State != "OBSERVED" || !reflect.DeepEqual(first.Answer, resolved.Answer) {
				errs <- fmt.Errorf("resolution changed authority or original evidence: %v", err)
			}
		}()
	}
	workers.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := ResolveClarificationConflict(reopened, input); err != nil {
		t.Fatal("restart lost idempotency", err)
	}
	report, err = AssessClarification(reopened, request.ID)
	if err != nil || report.State != "OBSERVED" || report.Decision.Verdict != "accept" ||
		report.Resolution == nil || report.Resolution.Selected != input.Selected {
		t.Fatal("explicit selection did not restore input eligibility", report, err)
	}
	history, err := ClarificationHistory(reopened)
	if err != nil {
		t.Fatal(err)
	}
	invalid := history[len(history)-1]
	resolution := *invalid.Resolution
	invalid.Resolution = &resolution
	resolution.Selected = report.Candidates[0].Receipt
	state, err := reopened.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if err := validateClarificationResolution(state, invalid); err == nil {
		t.Fatal("restore accepted a selection inconsistent with the recorded judgment identity")
	}
	retry := request
	retry.ID = "new-session"
	replayed, err := RequestClarification(reopened, retry)
	if err != nil || replayed.Request.ID != request.ID || replayed.Resolution == nil {
		t.Fatal("resolved question was reissued", replayed, err)
	}
	changed := input
	changed.ExpectedHash, changed.Selected = report.Decision.BasisHash, report.Candidates[0].Receipt
	if _, err := ResolveClarificationConflict(reopened, changed); err == nil {
		t.Fatal("resolution changed without new evidence")
	}
	if _, err := AnswerClarification(reopened, request.ID, alternative); err != nil {
		t.Fatal("known evidence retry unnecessarily requested resolution again", err)
	}
	report, err = AssessClarification(reopened, request.ID)
	if err != nil || report.Decision.Verdict != "accept" {
		t.Fatal("old conflict retry reopened a resolved question", report, err)
	}
	after, err := reopened.LoadWorkspace()
	if err != nil || len(after.Records.Requests) != len(before.Records.Requests)+4 {
		t.Fatal("retries duplicated the selected judgment", err)
	}
	newEvidence := alternative
	newEvidence.Source, newEvidence.Text = "spec:third", "A third interpretation."
	if _, err := AnswerClarification(reopened, request.ID, newEvidence); err == nil {
		t.Fatal("new conflict accepted")
	}
	report, err = AssessClarification(reopened, request.ID)
	if err != nil || report.Resolution != nil || !slices.ContainsFunc(report.Decision.Conditions, func(c core.InputCondition) bool {
		return c.Code == "clarification-conflict"
	}) {
		t.Fatal("new evidence did not invalidate the earlier selection", report, err)
	}
	if _, err := ResolveClarificationConflict(reopened, input); err == nil {
		t.Fatal("stale selection was reapplied after new evidence")
	}
	after, err = reopened.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	after.Records.Requests = before.Records.Requests
	if !reflect.DeepEqual(before, after) {
		t.Fatal("observational judgment changed approvals, Dispatch, HOLD or ownership")
	}
}
