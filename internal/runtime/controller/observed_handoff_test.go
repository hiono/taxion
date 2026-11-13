package controller

import (
	"errors"
	"reflect"
	"testing"

	"github.com/hiono/taxion/internal/core"
)

func TestObservedHandoffRejectsChangedTargetDraftAndContract(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ChangeHandoff(db, handoffRequest("hold", "", "HOLD")); err != nil {
		t.Fatal(err)
	}
	observation, err := ObserveFeedback(db, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"target", "role", "draft", "contract", "hold"} {
		bad := observation
		switch field {
		case "target":
			bad.Target.OutputSHA = "another-sha"
		case "role":
			bad.Target.Recipient = "REVIEWER"
		case "draft":
			bad.ExpectedDraftID = "another-draft"
		case "contract":
			bad.DraftContractHash = "another-contract"
		case "hold":
			bad.ExpectedChangeID = "another-hold"
		}
		_, err := ChangeObservedHandoff(db, bad, core.HandoffChange{
			ID: "release", ExpectedChangeID: observation.ExpectedChangeID,
			Action: "RELEASE", Actor: "human", Source: "human:console", Reason: "explicitly continue",
		})
		if !errors.Is(err, core.ErrFeedbackStale) {
			t.Fatal("stale displayed state authorized continuation", field, err)
		}
		after, err := db.LoadWorkspace()
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("stale choice changed handoff state", field, err)
		}
	}
	flow, err = ChangeObservedHandoff(db, observation, core.HandoffChange{
		ID: "release", ExpectedChangeID: observation.ExpectedChangeID,
		Action: "RELEASE", Actor: "human", Source: "human:console", Reason: "explicitly continue",
	})
	if err != nil || flow.HandoffHeld() {
		t.Fatal("matching explicit continuation rejected", err)
	}
}
