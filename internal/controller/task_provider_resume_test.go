package controller

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/provider"
)

func prepareTaskResumeTurn(t *testing.T, f *taskProviderStopFixture) {
	t.Helper()
	now := f.controller.now()
	f.runtime.capabilities.SendLookup = true
	f.runtime.capabilities.SendIdempotency = true
	f.runtime.capabilities.FinalOutputRetrieval = true
	f.runtime.capabilities.UsageIdentity = true
	f.runtime.capabilities.UsageCompleteness = true
	f.runtime.capabilities.Evidence = append(f.runtime.capabilities.Evidence,
		provider.CapabilityEvidence{Capability: provider.CapabilitySendLookup, Identity: f.runtime.identity, TestID: "task-resume-send", EvidenceSHA256: strings.Repeat("1", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilitySendIdempotency, Identity: f.runtime.identity, TestID: "task-resume-send", EvidenceSHA256: strings.Repeat("2", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityFinalOutput, Identity: f.runtime.identity, TestID: "task-resume-send", EvidenceSHA256: strings.Repeat("3", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityUsageIdentity, Identity: f.runtime.identity, TestID: "task-resume-send", EvidenceSHA256: strings.Repeat("4", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityUsageCompleteness, Identity: f.runtime.identity, TestID: "task-resume-send", EvidenceSHA256: strings.Repeat("5", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
	)
	if _, _, _, _, err := f.controller.SendTaskExecution(
		f.ctx, f.jobID, f.execution.ID, f.permission.ID,
		"cmd_67676767676767676767676767676767",
		"Persist a completed Task turn before Resume.", f.permissionRequest, 100, f.principal,
	); err != nil {
		t.Fatalf("SendTaskExecution fixture: %v", err)
	}
}

func enableTaskResumeE5(f *taskProviderStopFixture) {
	f.runtime.capabilities.CrossProcessSessionReattach = true
	f.runtime.capabilities.ResumeIdempotency = true
	f.runtime.capabilities.Evidence = append(f.runtime.capabilities.Evidence, provider.CapabilityEvidence{
		Capability:     provider.CapabilitySessionReattach,
		Identity:       f.runtime.identity,
		TestID:         "task-resume-e5",
		EvidenceSHA256: strings.Repeat("f", 64),
		EvidenceLevel:  provider.EvidenceDurable,
		VerifiedAt:     f.controller.now(),
	}, provider.CapabilityEvidence{
		Capability:     provider.CapabilityResumeIdempotency,
		Identity:       f.runtime.identity,
		TestID:         "task-resume-idempotency",
		EvidenceSHA256: strings.Repeat("e", 64),
		EvidenceLevel:  provider.EvidenceLive,
		VerifiedAt:     f.controller.now(),
	})
}

func (f taskProviderStopFixture) resume(commandID string) (domain.PlanningResumeIntent, domain.PlanningResumeObservation, provider.ResumeResult, error) {
	_, intent, observation, result, err := f.controller.ResumeTaskExecution(
		f.ctx, f.jobID, f.execution.ID, f.permission.ID, commandID,
		f.permissionRequest, f.principal,
	)
	return intent, observation, result, err
}

func TestTaskProviderResumeRejectsUnconfirmedResponsesAndReconcilesSameIdentity(t *testing.T) {
	tests := []struct {
		name   string
		result provider.ResumeResult
	}{
		{name: "wrong operation identity", result: provider.ResumeResult{OperationID: "provider_resume_wrong", Attached: true, EvidenceSHA256: strings.Repeat("a", 64)}},
		{name: "wrong session identity", result: provider.ResumeResult{SessionID: "session_other", Attached: true, EvidenceSHA256: strings.Repeat("a", 64)}},
		{name: "not attached", result: provider.ResumeResult{Attached: false, EvidenceSHA256: strings.Repeat("a", 64)}},
		{name: "missing evidence", result: provider.ResumeResult{Attached: true}},
		{name: "invalid evidence digest", result: provider.ResumeResult{Attached: true, EvidenceSHA256: "not-a-digest"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newTaskProviderStopFixture(t)
			prepareTaskResumeTurn(t, &f)
			enableTaskResumeE5(&f)
			commandID := "cmd_12121212121212121212121212121212"
			result := test.result
			if result.OperationID == "" {
				result.OperationID = domain.PlanningResumeOperationID(commandID)
			}
			if result.SessionID == "" {
				result.SessionID = "session_1"
			}
			f.runtime.resumeResult = result
			intent, unknown, _, err := f.resume(commandID)
			if !errors.Is(err, provider.ErrResumeUnconfirmed) ||
				unknown.Status != domain.PlanningResumeUnknown || f.runtime.resumeCalls != 1 {
				t.Fatalf("unconfirmed Resume response was accepted: intent=%#v observation=%#v calls=%d err=%v",
					intent, unknown, f.runtime.resumeCalls, err)
			}
			job, readErr := f.database.GetJob(f.ctx, f.jobID)
			if readErr != nil {
				t.Fatalf("GetJob: %v", readErr)
			}
			if len(job.PlanningResumeIntents) != 1 || len(job.PlanningResumeObservations) != 1 ||
				job.PlanningResumeObservations[0].Status != domain.PlanningResumeUnknown {
				t.Fatalf("unconfirmed Resume did not persist one UNKNOWN observation: intents=%d observations=%#v",
					len(job.PlanningResumeIntents), job.PlanningResumeObservations)
			}
			f.runtime.resumeResult = provider.ResumeResult{}
			replayedIntent, attached, result, err := f.resume(commandID)
			if err != nil || replayedIntent.ID != intent.ID || replayedIntent.OperationID != intent.OperationID ||
				attached.Status != domain.PlanningResumeAttached || !result.Attached ||
				result.OperationID != intent.OperationID || f.runtime.resumeCalls != 2 {
				t.Fatalf("same Resume identity did not reconcile to ATTACHED: intent=%#v observation=%#v result=%#v calls=%d err=%v",
					replayedIntent, attached, result, f.runtime.resumeCalls, err)
			}
			job, readErr = f.database.GetJob(f.ctx, f.jobID)
			if readErr != nil {
				t.Fatalf("GetJob after Resume reconciliation: %v", readErr)
			}
			if len(job.PlanningResumeObservations) != 2 ||
				job.PlanningResumeObservations[0].Status != domain.PlanningResumeUnknown ||
				job.PlanningResumeObservations[1].Status != domain.PlanningResumeAttached ||
				!job.PlanningResumeObservations[1].ReconcilesUnknown {
				t.Fatalf("Resume reconciliation did not append ATTACHED while preserving UNKNOWN: %#v",
					job.PlanningResumeObservations)
			}
			replayedIntent, replayed, _, err := f.resume(commandID)
			if err != nil || replayedIntent.ID != intent.ID || replayed.ID != attached.ID || f.runtime.resumeCalls != 2 {
				t.Fatalf("terminal Resume replay repeated Provider side effect: intent=%#v observation=%#v calls=%d err=%v",
					replayedIntent, replayed, f.runtime.resumeCalls, err)
			}
		})
	}
}

func TestTaskProviderResumeRequiresMatchingE5CapabilityEvidence(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*taskProviderStopFixture)
	}{
		{name: "capability absent", mutate: func(f *taskProviderStopFixture) { f.runtime.capabilities.CrossProcessSessionReattach = false }},
		{name: "Provider identity mismatch", mutate: func(f *taskProviderStopFixture) {
			f.runtime.capabilities.Identity.ProviderID = "provider.other"
		}},
		{name: "E5 evidence missing", mutate: func(f *taskProviderStopFixture) {
			f.runtime.capabilities.Evidence = nil
		}},
		{name: "evidence below E5", mutate: func(f *taskProviderStopFixture) {
			for i := range f.runtime.capabilities.Evidence {
				if f.runtime.capabilities.Evidence[i].Capability == provider.CapabilitySessionReattach {
					f.runtime.capabilities.Evidence[i].EvidenceLevel = provider.EvidenceLive
				}
			}
		}},
		{name: "Resume idempotency evidence missing", mutate: func(f *taskProviderStopFixture) {
			f.runtime.capabilities.ResumeIdempotency = true
			filtered := f.runtime.capabilities.Evidence[:0]
			for _, evidence := range f.runtime.capabilities.Evidence {
				if evidence.Capability != provider.CapabilityResumeIdempotency {
					filtered = append(filtered, evidence)
				}
			}
			f.runtime.capabilities.Evidence = filtered
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newTaskProviderStopFixture(t)
			prepareTaskResumeTurn(t, &f)
			enableTaskResumeE5(&f)
			test.mutate(&f)
			_, _, _, err := f.resume("cmd_23232323232323232323232323232323")
			if !errors.Is(err, provider.ErrUnsupportedCapability) || f.runtime.resumeCalls != 0 {
				t.Fatalf("Resume crossed an unsupported E5 boundary: calls=%d err=%v", f.runtime.resumeCalls, err)
			}
			job, readErr := f.database.GetJob(f.ctx, f.jobID)
			if readErr != nil {
				t.Fatalf("GetJob: %v", readErr)
			}
			if len(job.PlanningResumeIntents) != 0 || len(job.PlanningResumeObservations) != 0 {
				t.Fatalf("unsupported E5 evidence persisted Resume facts: intents=%d observations=%d",
					len(job.PlanningResumeIntents), len(job.PlanningResumeObservations))
			}
		})
	}
}

func TestTaskProviderResumeWorktreeGatesAndIntentFirstOrdering(t *testing.T) {
	t.Run("pre-intent Worktree failure", func(t *testing.T) {
		f := newTaskProviderStopFixture(t)
		prepareTaskResumeTurn(t, &f)
		enableTaskResumeE5(&f)
		f.worktree.failAt = f.worktree.verifyCalls + 1
		f.worktree.verifyErr = errors.New("Worktree changed before Resume intent")
		_, _, _, err := f.resume("cmd_34343434343434343434343434343434")
		if err == nil || f.runtime.resumeCalls != 0 {
			t.Fatalf("Resume crossed a failed pre-intent Worktree check: calls=%d err=%v", f.runtime.resumeCalls, err)
		}
		job, readErr := f.database.GetJob(f.ctx, f.jobID)
		if readErr != nil || len(job.PlanningResumeIntents) != 0 {
			t.Fatalf("pre-intent Worktree failure persisted Resume intent: intents=%d err=%v",
				len(job.PlanningResumeIntents), readErr)
		}
	})
	t.Run("intent committed before Provider call", func(t *testing.T) {
		f := newTaskProviderStopFixture(t)
		prepareTaskResumeTurn(t, &f)
		enableTaskResumeE5(&f)
		f.runtime.resumeHook = func(_ context.Context, request provider.ResumeRequest) error {
			job, err := f.database.GetJob(f.ctx, f.jobID)
			if err != nil {
				return err
			}
			if len(job.PlanningResumeIntents) != 1 || len(job.PlanningResumeObservations) != 0 ||
				job.PlanningResumeIntents[0].OperationID != request.OperationID {
				return errors.New("Resume intent was not durably committed before Runtime.Resume")
			}
			return nil
		}
		intent, observation, result, err := f.resume("cmd_45454545454545454545454545454545")
		if err != nil || observation.Status != domain.PlanningResumeAttached ||
			!result.Attached || result.OperationID != intent.OperationID || f.runtime.resumeCalls != 1 {
			t.Fatalf("Resume intent-first ordering failed: intent=%#v observation=%#v result=%#v calls=%d err=%v",
				intent, observation, result, f.runtime.resumeCalls, err)
		}
	})
	t.Run("final Worktree failure after intent", func(t *testing.T) {
		f := newTaskProviderStopFixture(t)
		prepareTaskResumeTurn(t, &f)
		enableTaskResumeE5(&f)
		f.worktree.failAt = f.worktree.verifyCalls + 2
		f.worktree.verifyErr = errors.New("Worktree changed immediately before Runtime.Resume")
		commandID := "cmd_56565656565656565656565656565656"
		intent, _, _, err := f.resume(commandID)
		if err == nil || f.runtime.resumeCalls != 0 {
			t.Fatalf("Resume crossed failed final Worktree check: calls=%d err=%v", f.runtime.resumeCalls, err)
		}
		job, readErr := f.database.GetJob(f.ctx, f.jobID)
		if readErr != nil || len(job.PlanningResumeIntents) != 1 || len(job.PlanningResumeObservations) != 1 ||
			job.PlanningResumeObservations[0].Status != domain.PlanningResumeNotDispatched ||
			job.PlanningResumeObservations[0].DispatchClaimID != "" || len(job.PlanningResumeDispatchClaims) != 0 {
			t.Fatalf("final pre-dispatch Worktree failure was not recorded as NOT_DISPATCHED: intents=%d observations=%#v claims=%#v err=%v",
				len(job.PlanningResumeIntents), job.PlanningResumeObservations, job.PlanningResumeDispatchClaims, readErr)
		}
		replayedIntent, observation, result, err := f.resume(commandID)
		if err != nil || replayedIntent.ID != intent.ID || replayedIntent.OperationID != intent.OperationID ||
			observation.Status != domain.PlanningResumeAttached || !result.Attached || f.runtime.resumeCalls != 1 {
			t.Fatalf("retry did not reattach the same Session / OperationID: intent=%#v observation=%#v result=%#v calls=%d err=%v",
				replayedIntent, observation, result, f.runtime.resumeCalls, err)
		}
		job, readErr = f.database.GetJob(f.ctx, f.jobID)
		if readErr != nil || len(job.PlanningResumeObservations) != 2 ||
			job.PlanningResumeObservations[0].Status != domain.PlanningResumeNotDispatched ||
			job.PlanningResumeObservations[1].Status != domain.PlanningResumeAttached ||
			len(job.PlanningResumeDispatchClaims) != 1 || len(job.PlanningResumeDispatchReleases) != 1 {
			t.Fatalf("Resume retry did not preserve NOT_DISPATCHED and terminal ATTACHED facts: observations=%#v claims=%#v releases=%#v err=%v",
				job.PlanningResumeObservations, job.PlanningResumeDispatchClaims, job.PlanningResumeDispatchReleases, readErr)
		}
	})
}

func TestTaskProviderResumeRefreshesE5EvidenceAfterNotDispatched(t *testing.T) {
	f := newTaskProviderStopFixture(t)
	prepareTaskResumeTurn(t, &f)
	enableTaskResumeE5(&f)
	f.worktree.failAt = f.worktree.verifyCalls + 2
	f.worktree.verifyErr = errors.New("Worktree unavailable before Resume dispatch")

	firstCommand := "cmd_71717171717171717171717171717171"
	firstIntent, firstObservation, _, firstErr := f.resume(firstCommand)
	if firstErr == nil || firstObservation.Status != domain.PlanningResumeNotDispatched ||
		f.runtime.resumeCalls != 0 || !validTestDigest(firstIntent.CapabilityEvidenceSHA256) {
		t.Fatalf("initial Resume did not persist a NOT_DISPATCHED intent: intent=%#v observation=%#v calls=%d err=%v",
			firstIntent, firstObservation, f.runtime.resumeCalls, firstErr)
	}

	refreshedEvidence := strings.Repeat("d", 64)
	updated := false
	for i := range f.runtime.capabilities.Evidence {
		evidence := &f.runtime.capabilities.Evidence[i]
		if evidence.Capability == provider.CapabilitySessionReattach &&
			evidence.EvidenceLevel == provider.EvidenceDurable {
			evidence.EvidenceSHA256 = refreshedEvidence
			updated = true
		}
	}
	if !updated {
		t.Fatal("fixture has no durable SessionReattach evidence to refresh")
	}

	secondIntent, secondObservation, result, secondErr := f.resume("cmd_72727272727272727272727272727272")
	if secondErr != nil || secondIntent.ID == firstIntent.ID ||
		secondIntent.CapabilityEvidenceSHA256 != refreshedEvidence ||
		secondObservation.Status != domain.PlanningResumeAttached || !result.Attached ||
		f.runtime.resumeCalls != 1 {
		t.Fatalf("refreshed E5 evidence did not recover an un-dispatched Resume: first=%#v second=%#v observation=%#v result=%#v calls=%d err=%v",
			firstIntent, secondIntent, secondObservation, result, f.runtime.resumeCalls, secondErr)
	}
	job, err := f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(job.PlanningResumeIntents) != 2 || len(job.PlanningResumeObservations) != 2 ||
		job.PlanningResumeObservations[0].ResumeID != firstIntent.ID ||
		job.PlanningResumeObservations[0].Status != domain.PlanningResumeNotDispatched ||
		job.PlanningResumeObservations[1].ResumeID != secondIntent.ID ||
		job.PlanningResumeObservations[1].Status != domain.PlanningResumeAttached {
		t.Fatalf("refreshed Resume did not preserve both append-only lifecycles: intents=%#v observations=%#v",
			job.PlanningResumeIntents, job.PlanningResumeObservations)
	}
}

func validTestDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return false
		}
	}
	return true
}

func TestTaskProviderResumePersistsDefinitiveFailureAndAllowsStop(t *testing.T) {
	f := newTaskProviderStopFixture(t)
	prepareTaskResumeTurn(t, &f)
	enableTaskResumeE5(&f)
	failure, err := provider.NewOperationError(provider.FailureDefinitive, provider.OperationCodeInvalidRequest)
	if err != nil {
		t.Fatalf("NewOperationError: %v", err)
	}
	f.runtime.resumeErr = failure
	commandID := "cmd_34343434343434343434343434343434"
	_, returnedObservation, _, resumeErr := f.resume(commandID)
	job, err := f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob after definitive Resume failure: %v", err)
	}
	if len(job.PlanningResumeIntents) != 1 || len(job.PlanningResumeObservations) != 1 ||
		len(job.PlanningResumeDispatchClaims) != 1 || len(job.PlanningResumeDispatchReleases) != 1 {
		t.Fatalf("definitive Resume failure facts = intents:%d observations:%d claims:%d releases:%d",
			len(job.PlanningResumeIntents), len(job.PlanningResumeObservations),
			len(job.PlanningResumeDispatchClaims), len(job.PlanningResumeDispatchReleases))
	}
	intent := job.PlanningResumeIntents[0]
	observation := job.PlanningResumeObservations[0]
	claim := job.PlanningResumeDispatchClaims[0]
	release := job.PlanningResumeDispatchReleases[0]
	if release.ClaimID != claim.ID || release.Outcome != domain.PlanningResumeDispatchReleasedFailed {
		t.Fatalf("definitive Resume failure did not release its original claim before replay: claim=%#v release=%#v",
			claim, release)
	}
	if !errors.Is(resumeErr, failure) || observation.Status != domain.PlanningResumeFailed ||
		observation.ResumeID != intent.ID || returnedObservation.ID != observation.ID ||
		f.runtime.resumeCalls != 1 {
		t.Fatalf("definitive Resume failure was not persisted: intent=%#v observation=%#v returned=%#v calls=%d err=%v",
			intent, observation, returnedObservation, f.runtime.resumeCalls, resumeErr)
	}
	_, replayedFailure, _, replayErr := f.resume(commandID)
	if replayErr == nil || replayedFailure.Status != domain.PlanningResumeFailed || f.runtime.resumeCalls != 1 {
		t.Fatalf("definitive Resume replay did not preserve failure without another Provider call: observation=%#v calls=%d err=%v",
			replayedFailure, f.runtime.resumeCalls, replayErr)
	}
	_, stopIntent, _, stopErr := f.stop("cmd_45454545454545454545454545454545", domain.ProviderStopReasonFailure)
	if stopIntent.ID == "" {
		t.Fatalf("Stop did not proceed after definitive Resume failure: %v", stopErr)
	}
	job, err = f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob after Stop intent: %v", err)
	}
	if len(job.ProviderStopIntents) != 1 || job.ProviderStopIntents[0].ID != stopIntent.ID {
		t.Fatalf("Stop intent was not persisted after definitive Resume failure: %#v", job.ProviderStopIntents)
	}
}

func TestTaskProviderResumeDoesNotDispatchFromReplayedActiveClaim(t *testing.T) {
	f := newTaskProviderStopFixture(t)
	prepareTaskResumeTurn(t, &f)
	enableTaskResumeE5(&f)

	entered := make(chan struct{})
	continueResume := make(chan struct{})
	f.runtime.resumeHook = func(ctx context.Context, _ provider.ResumeRequest) error {
		close(entered)
		select {
		case <-continueResume:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	commandID := "cmd_83838383838383838383838383838383"
	firstResult := make(chan error, 1)
	go func() {
		_, _, _, err := f.resume(commandID)
		firstResult <- err
	}()
	<-entered

	_, _, _, replayErr := f.resume(commandID)
	if !errors.Is(replayErr, provider.ErrResumeUnconfirmed) || f.runtime.resumeCalls != 1 {
		close(continueResume)
		t.Fatalf("replayed Resume dispatched while another caller owned the claim: calls=%d err=%v",
			f.runtime.resumeCalls, replayErr)
	}
	close(continueResume)
	if err := <-firstResult; err != nil {
		t.Fatalf("claim owner failed to complete Resume: %v", err)
	}
	job, err := f.database.GetJob(f.ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob after Resume race: %v", err)
	}
	if len(job.PlanningResumeObservations) != 1 ||
		job.PlanningResumeObservations[0].Status != domain.PlanningResumeAttached ||
		len(job.PlanningResumeDispatchClaims) != 1 || len(job.PlanningResumeDispatchReleases) != 1 {
		t.Fatalf("Resume race left inconsistent facts: observations=%#v claims=%#v releases=%#v",
			job.PlanningResumeObservations, job.PlanningResumeDispatchClaims, job.PlanningResumeDispatchReleases)
	}
}
