package core

import "time"

// FailedReviewTiming separates answer completion from later session closure.
type FailedReviewTiming struct {
	DispatchCreatedAt  time.Time `json:"dispatchCreatedAt"`
	SubmittedAt        time.Time `json:"submittedAt"`
	FinishedAt         time.Time `json:"finishedAt"`
	ShutdownAt         time.Time `json:"shutdownAt"`
	FailedResultAt     time.Time `json:"failedResultAt"`
	SubmissionDeadline time.Time `json:"submissionDeadline"`
	ObservedAt         time.Time `json:"observedAt"`
}

func (t FailedReviewTiming) FailedChecks() []string {
	failed := []string{}
	for _, check := range []struct {
		name string
		ok   bool
	}{
		{"answer-at-or-after-dispatch-created", !t.SubmittedAt.IsZero() && !t.SubmittedAt.Before(t.DispatchCreatedAt)},
		{"answer-before-submission-deadline", t.SubmittedAt.Before(t.SubmissionDeadline)},
		{"turn-end-at-or-after-answer", !t.FinishedAt.IsZero() && !t.FinishedAt.Before(t.SubmittedAt)},
		{"turn-end-before-submission-deadline", t.FinishedAt.Before(t.SubmissionDeadline)},
		{"turn-end-not-after-failed-result", !t.FinishedAt.After(t.FailedResultAt)},
		{"turn-end-not-in-future", !t.FinishedAt.After(t.ObservedAt)},
		{"routine-shutdown-time-present", !t.ShutdownAt.IsZero()},
		{"shutdown-at-or-after-turn-end", !t.ShutdownAt.Before(t.FinishedAt)},
		{"shutdown-before-submission-deadline", t.ShutdownAt.Before(t.SubmissionDeadline)},
		{"shutdown-not-in-future", !t.ShutdownAt.After(t.ObservedAt)},
		{"failed-result-not-in-future", !t.FailedResultAt.After(t.ObservedAt)},
	} {
		if !check.ok {
			failed = append(failed, check.name)
		}
	}
	return failed
}
