// Package core implements the SDK-neutral governance core of Taxion.
//
// SDK-001: no SDK-specific, network, or provider types may appear here.
// Only plain stdlib types: strings, times, maps, slices.
package core

import (
	"errors"
	"time"
)

// Task is the unit of ordered work. Repo records the explicit repository
// identity given at creation (REP-001).
type Task struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Repo      string    `json:"repo"`
	CreatedAt time.Time `json:"createdAt"`
}

// Dispatch binds a Task to an isolated execution context (ROLE-001).
// Repo records the explicit repository identity the dispatch is bound to;
// it must come from an explicit --repo flag, never inferred (REP-001).
type Dispatch struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"taskId"`
	Repo      string    `json:"repo"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"createdAt"`
}

// Evidence is an immutable record bound to an exact output SHA (SHA-001).
type Evidence struct {
	SHA       string    `json:"sha"` // exact retained coder output SHA
	TaskID    string    `json:"taskId"`
	CreatedAt time.Time `json:"createdAt"`
}

// Checkpoint gates readiness: incomplete sequence must never report ready (CHK-001).
type Checkpoint struct {
	Name string `json:"name"`
	Done bool   `json:"done"`
}

var (
	ErrActiveDispatchExists = errors.New("taxion: active dispatch already exists (DSP-001)")
	ErrEmptySHA             = errors.New("taxion: evidence SHA must not be empty (SHA-001)")
	ErrNotReady             = errors.New("taxion: checkpoint sequence incomplete (CHK-001)")
	ErrOutOfOrder           = errors.New("taxion: checkpoint completed out of order (CHK-002)")
	ErrUnknownCheckpoint    = errors.New("taxion: unknown checkpoint name (CHK-002)")
	ErrEmptyRepo            = errors.New("taxion: repo identity must be given explicitly via --repo (REP-001)")
)

// GuardActiveDispatch enforces DSP-001: an active Dispatch must never be
// silently replaced. Call before creating a new dispatch.
func GuardActiveDispatch(existing []Dispatch) error {
	for _, d := range existing {
		if d.Active {
			return ErrActiveDispatchExists
		}
	}
	return nil
}

// BindEvidence enforces SHA-001: reviewer execution must bind to the exact
// retained coder output SHA — empty or ambiguous SHAs are rejected.
func BindEvidence(taskID, sha string) (Evidence, error) {
	if sha == "" {
		return Evidence{}, ErrEmptySHA
	}
	return Evidence{TaskID: taskID, SHA: sha, CreatedAt: time.Now().UTC()}, nil
}

// CheckRepo enforces REP-001: the repository identity an operation binds to
// must be stated explicitly. Empty means "infer from cwd", which caused the
// generalized repository-confusion failure and is therefore rejected.
func CheckRepo(repo string) error {
	if repo == "" {
		return ErrEmptyRepo
	}
	return nil
}

// CheckReady enforces CHK-001: all checkpoints must be done.
func CheckReady(checkpoints []Checkpoint) error {
	for _, c := range checkpoints {
		if !c.Done {
			return ErrNotReady
		}
	}
	return nil
}

// Sequence is an ordered checkpoint sequence (CHK-002): steps must complete
// in declaration order. An empty sequence reports ready (no gates to pass).
type Sequence struct {
	steps []Checkpoint
}

// NewSequence declares an ordered checkpoint sequence.
func NewSequence(names ...string) *Sequence {
	s := &Sequence{}
	for _, n := range names {
		s.steps = append(s.steps, Checkpoint{Name: n})
	}
	return s
}

// Advance marks name done. It must be the next pending step in order:
// completing a later step first, re-completing, or naming an undeclared
// step is rejected.
func (s *Sequence) Advance(name string) error {
	next := -1
	for i, c := range s.steps {
		if !c.Done {
			next = i
			break
		}
	}
	if next == -1 {
		return ErrOutOfOrder // sequence already complete
	}
	known := false
	for _, c := range s.steps {
		if c.Name == name {
			known = true
			break
		}
	}
	if !known {
		return ErrUnknownCheckpoint
	}
	if s.steps[next].Name != name {
		return ErrOutOfOrder
	}
	s.steps[next].Done = true
	return nil
}

// Steps returns a snapshot of the ordered checkpoints.
func (s *Sequence) Steps() []Checkpoint {
	out := make([]Checkpoint, len(s.steps))
	copy(out, s.steps)
	return out
}

// Ready reports whether the full sequence completed (CHK-001 over CHK-002 order).
func (s *Sequence) Ready() bool {
	return CheckReady(s.steps) == nil
}
