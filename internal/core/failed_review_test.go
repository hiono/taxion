package core

import (
	"slices"
	"testing"
	"time"
)

func TestFailedReviewTimingSeparatesAnswerFromShutdown(t *testing.T) {
	stamp := func(value string) time.Time {
		t.Helper()
		result, err := time.Parse(time.RFC3339Nano, "2026-11-05T"+value+"Z")
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	timing := FailedReviewTiming{
		DispatchCreatedAt: stamp("21:08:00"), SubmittedAt: stamp("21:09:13.391"),
		FinishedAt: stamp("21:09:13.402"), FailedResultAt: stamp("21:11:31.13725837"),
		ShutdownAt: stamp("21:11:40.811"), SubmissionDeadline: stamp("21:37:08.475923"),
		ObservedAt: stamp("21:40:00"),
	}
	if failed := timing.FailedChecks(); len(failed) != 0 {
		t.Fatal("on-time answer with post-failure routine shutdown rejected", failed)
	}
	for _, test := range []struct {
		name   string
		change func(*FailedReviewTiming)
		failed string
	}{
		{"early-shutdown", func(v *FailedReviewTiming) { v.ShutdownAt = v.FinishedAt }, ""},
		{"shutdown-at-failure", func(v *FailedReviewTiming) { v.ShutdownAt = v.FailedResultAt }, ""},
		{"shutdown-at-observation", func(v *FailedReviewTiming) {
			v.ObservedAt = v.ShutdownAt
		}, ""},
		{"turn-at-failure", func(v *FailedReviewTiming) { v.FinishedAt = v.FailedResultAt }, ""},
		{"answer-before-creation", func(v *FailedReviewTiming) { v.SubmittedAt = v.DispatchCreatedAt.Add(-time.Nanosecond) }, "answer-at-or-after-dispatch-created"},
		{"answer-at-deadline", func(v *FailedReviewTiming) { v.SubmittedAt = v.SubmissionDeadline }, "answer-before-submission-deadline"},
		{"turn-before-answer", func(v *FailedReviewTiming) { v.FinishedAt = v.SubmittedAt.Add(-time.Nanosecond) }, "turn-end-at-or-after-answer"},
		{"turn-at-deadline", func(v *FailedReviewTiming) { v.FinishedAt = v.SubmissionDeadline }, "turn-end-before-submission-deadline"},
		{"turn-after-failure", func(v *FailedReviewTiming) { v.FinishedAt = v.FailedResultAt.Add(time.Nanosecond) }, "turn-end-not-after-failed-result"},
		{"missing-shutdown", func(v *FailedReviewTiming) { v.ShutdownAt = time.Time{} }, "routine-shutdown-time-present"},
		{"shutdown-before-turn", func(v *FailedReviewTiming) { v.ShutdownAt = v.FinishedAt.Add(-time.Nanosecond) }, "shutdown-at-or-after-turn-end"},
		{"shutdown-at-deadline", func(v *FailedReviewTiming) { v.ShutdownAt = v.SubmissionDeadline }, "shutdown-before-submission-deadline"},
		{"future-shutdown", func(v *FailedReviewTiming) { v.ShutdownAt = v.ObservedAt.Add(time.Nanosecond) }, "shutdown-not-in-future"},
		{"future-failure", func(v *FailedReviewTiming) { v.FailedResultAt = v.ObservedAt.Add(time.Nanosecond) }, "failed-result-not-in-future"},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := timing
			test.change(&changed)
			failed := changed.FailedChecks()
			if test.failed == "" && len(failed) != 0 || test.failed != "" && !slices.Contains(failed, test.failed) {
				t.Fatal("incorrect timing diagnosis", failed, test.failed)
			}
		})
	}
	timing.SubmissionDeadline = stamp("20:56:08.475923")
	if got, want := timing.FailedChecks(), []string{
		"answer-before-submission-deadline", "turn-end-before-submission-deadline", "shutdown-before-submission-deadline",
	}; !slices.Equal(got, want) {
		t.Fatal("diagnosis hid simultaneous deadline failures", got)
	}
}
