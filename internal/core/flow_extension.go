package core

import (
	"encoding/hex"
	"math"
	"time"
)

// FlowExtension adds authority without replacing the original plan or usage.
type FlowExtension struct {
	BasisHash          string    `json:"basisHash"`
	Hash               string    `json:"hash"`
	PreviousDeadline   time.Time `json:"previousDeadline"`
	Deadline           time.Time `json:"deadline"`
	PreviousMaxNanoAIU float64   `json:"previousMaxNanoAiu"`
	MaxNanoAIU         float64   `json:"maxNanoAiu"`
	Actor              string    `json:"actor"`
	Source             string    `json:"source"`
	Reason             string    `json:"reason"`
	At                 time.Time `json:"at"`
}

func (e FlowExtension) ApprovalHash() (string, error) {
	e.Hash, e.At = "", time.Time{}
	return CanonicalSHA256(e)
}

func (f ReviewFlow) ExecutionDeadline() time.Time {
	if len(f.Extensions) != 0 {
		return f.Extensions[len(f.Extensions)-1].Deadline
	}
	return f.Deadline
}

func (f ReviewFlow) NativeLimit() float64 {
	if len(f.Extensions) != 0 {
		return f.Extensions[len(f.Extensions)-1].MaxNanoAIU
	}
	return f.Budget.MaxNanoAIU
}

func (f ReviewFlow) validateExtensions() error {
	deadline, limit := f.Deadline, f.Budget.MaxNanoAIU
	appliedAt := f.StartedAt
	for _, e := range f.Extensions {
		hash, err := e.ApprovalHash()
		_, digestErr := hex.DecodeString(e.BasisHash)
		if err != nil || hash != e.Hash || digestErr != nil || len(e.BasisHash) != 64 ||
			!e.PreviousDeadline.Equal(deadline) || e.PreviousMaxNanoAIU != limit ||
			e.At.IsZero() || !e.At.Before(deadline) || e.At.Before(appliedAt) ||
			e.Deadline.Before(deadline) || e.Deadline.Sub(f.StartedAt) > 365*24*time.Hour ||
			math.IsNaN(e.MaxNanoAIU) || math.IsInf(e.MaxNanoAIU, 0) || e.MaxNanoAIU < limit ||
			limit == 0 && e.MaxNanoAIU != 0 ||
			e.Deadline.Equal(deadline) && e.MaxNanoAIU == limit ||
			!nonempty(e.Actor) || !nonempty(e.Source) || !nonempty(e.Reason) ||
			Redact(e.Actor) != e.Actor || Redact(e.Source) != e.Source || Redact(e.Reason) != e.Reason {
			return graphError("invalid append-only resource extension for Flow %s", f.ID)
		}
		deadline, limit = e.Deadline, e.MaxNanoAIU
		appliedAt = e.At
	}
	return nil
}
