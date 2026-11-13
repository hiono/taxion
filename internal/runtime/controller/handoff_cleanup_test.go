package controller

import (
	"bytes"
	"errors"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
)

func TestHandoffCleanupFailureIsNotReportedAsResumableHold(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	cleanupErr := errors.New("session cleanup failed")
	flow, err = RunReviewFlow(t.Context(), db, ledger, flow.ID, func(dispatch core.GraphDispatch) (runtime.Identity, error) {
		if _, err := ChangeHandoff(db, handoffRequest("hold", "", "HOLD")); err != nil {
			return nil, err
		}
		return &scriptedRuntime{fakeRuntime: fakeRuntime{
			role: dispatch.Recipient, hash: "hash", worktree: dispatch.Instruction.Worktree, closeErr: cleanupErr,
		}}, nil
	}, &bytes.Buffer{})
	if !errors.Is(err, core.ErrHandoffHeld) || !errors.Is(err, cleanupErr) || flow.Status != "STOPPED" {
		t.Fatalf("cleanup failure hidden as ordinary hold: %+v %v", flow, err)
	}
}
