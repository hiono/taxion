package core

import (
	"math"
	"slices"
	"time"
)

// StageBudget is approved separately from the immutable Task, in the exact
// Flow plan. Credits are provider session soft limits, never reserved money.
type StageBudget struct {
	Version                int     `json:"version"`
	ValidationSeconds      int64   `json:"validationSeconds,omitempty"`
	ReviewSeconds          int64   `json:"reviewSeconds,omitempty"`
	RepairSeconds          int64   `json:"repairSeconds,omitempty"`
	CoderSessionCredits    float64 `json:"coderSessionCredits,omitempty"`
	ReviewerSessionCredits float64 `json:"reviewerSessionCredits,omitempty"`
	ReviewNanoAIU          float64 `json:"reviewNanoAiu,omitempty"`
	RepairNanoAIU          float64 `json:"repairNanoAiu,omitempty"`
}

type StagePlan struct {
	Version                int      `json:"version"`
	Hash                   string   `json:"hash"`
	TimeoutSeconds         int64    `json:"timeoutSeconds"`
	MaxRepairs             int      `json:"maxRepairs"`
	ReviewOnly             bool     `json:"reviewOnly"`
	ValidationSeconds      int64    `json:"validationSeconds"`
	ReviewSeconds          int64    `json:"reviewSeconds"`
	RepairSeconds          int64    `json:"repairSeconds"`
	CoderSessionCredits    float64  `json:"coderSessionCredits"`
	ReviewerSessionCredits float64  `json:"reviewerSessionCredits"`
	ReviewNanoAIU          float64  `json:"reviewNanoAiu"`
	RepairNanoAIU          float64  `json:"repairNanoAiu"`
	Basis                  []string `json:"basis"`
}

func validStageNumber(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func (s *StageBudget) Validate() error {
	if s == nil {
		return nil
	}
	if s.Version != 1 {
		return graphError("stage budget requires version 1")
	}
	for _, seconds := range []int64{s.ValidationSeconds, s.ReviewSeconds, s.RepairSeconds} {
		if seconds < 0 || seconds > int64((365*24*time.Hour)/time.Second) {
			return graphError("stage estimates must be between zero and one year")
		}
	}
	for _, credits := range []float64{s.CoderSessionCredits, s.ReviewerSessionCredits} {
		if !validStageNumber(credits) || credits != 0 && credits < 30 {
			return graphError("stage session credits must be unset or a soft ceiling of at least 30")
		}
	}
	if !validStageNumber(s.ReviewNanoAIU) || !validStageNumber(s.RepairNanoAIU) {
		return graphError("stage native estimates must be finite and nonnegative")
	}
	return nil
}

func (s *StagePlan) Validate(flow ReviewFlow) error {
	if s == nil {
		return nil
	}
	policy := StageBudget{Version: s.Version, ValidationSeconds: s.ValidationSeconds,
		ReviewSeconds: s.ReviewSeconds, RepairSeconds: s.RepairSeconds,
		CoderSessionCredits: s.CoderSessionCredits, ReviewerSessionCredits: s.ReviewerSessionCredits,
		ReviewNanoAIU: s.ReviewNanoAIU, RepairNanoAIU: s.RepairNanoAIU}
	if policy.Validate() != nil || s.ValidateNativeReservations(flow.Budget.MaxNanoAIU) != nil ||
		!validDocumentHash(s.Hash) || s.TimeoutSeconds <= 0 || s.MaxRepairs < 0 || s.MaxRepairs > 1000 ||
		s.MaxRepairs != flow.MaxRepairs || s.ReviewOnly != (flow.SourceDispatchID != "") ||
		s.ValidationSeconds <= 0 || s.ReviewSeconds <= 0 || s.RepairSeconds <= 0 ||
		!validList(s.Basis) || len(s.Basis) == 0 ||
		s.TimeoutSeconds > int64((365*24*time.Hour)/time.Second) ||
		flow.Deadline.Sub(flow.StartedAt) > time.Duration(s.TimeoutSeconds)*time.Second ||
		s.ReservedSeconds(0, "CODER") >= s.TimeoutSeconds && !s.ReviewOnly ||
		s.ReservedSeconds(0, "REVIEWER") >= s.TimeoutSeconds && s.ReviewOnly {
		return graphError("invalid approved stage plan")
	}
	return nil
}

func CloneStagePlan(s *StagePlan) *StagePlan {
	if s == nil {
		return nil
	}
	clone := *s
	clone.Basis = slices.Clone(s.Basis)
	return &clone
}

// Reservations are offsets from the original deadline, not a fresh timeout on
// resume. Earlier completion can be reused without borrowing a later stage.
func (s StagePlan) ReservedSeconds(repairs int, role string) int64 {
	remainingRepairs := int64(max(s.MaxRepairs-repairs, 0))
	future := remainingRepairs * (s.RepairSeconds + s.ValidationSeconds + s.ReviewSeconds)
	if role == "CODER" {
		future += s.ValidationSeconds + s.ReviewSeconds
	}
	return future
}

func (s StagePlan) ReservedNanoAIU(repairs int, role string) float64 {
	future := float64(max(s.MaxRepairs-repairs, 0)) * (s.RepairNanoAIU + s.ReviewNanoAIU)
	if role == "CODER" {
		future += s.ReviewNanoAIU
	}
	return future
}

func (s StagePlan) SubmissionDeadline(flow ReviewFlow, role string) time.Time {
	return flow.Deadline.Add(-time.Duration(s.ReservedSeconds(flow.Repairs, role)) * time.Second)
}

func (s StagePlan) ValidateNativeReservations(limit float64) error {
	if limit <= 0 {
		if s.ReviewNanoAIU != 0 || s.RepairNanoAIU != 0 {
			return graphError("native stage reservations require an approved native Flow ceiling")
		}
		return nil
	}
	if (!s.ReviewOnly || s.MaxRepairs > 0) && s.ReviewNanoAIU == 0 ||
		s.MaxRepairs > 0 && s.RepairNanoAIU == 0 {
		return graphError("native stage reservations require positive reviewNanoAiu and, when repairs are allowed, repairNanoAiu from complete same-configuration usage or an explicitly approved estimate")
	}
	role := "CODER"
	if s.ReviewOnly {
		role = "REVIEWER"
	}
	if s.ReservedNanoAIU(0, role) >= limit {
		return graphError("native stage reservations leave insufficient room within the approved Flow ceiling")
	}
	return nil
}
