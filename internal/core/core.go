// Package core implements the SDK-neutral governance core of Taxion.
//
// SDK-001: no SDK-specific, network, or provider types may appear here.
// Only plain stdlib types: strings, times, maps, slices.
package core

import (
	"errors"
	"time"
)

// Task is the unit of ordered work.
type Task struct {
	ID        string
	Title     string
	CreatedAt time.Time
}

// Dispatch binds a Task to an isolated execution context (ROLE-001).
type Dispatch struct {
	ID        string
	TaskID    string
	Active    bool
	CreatedAt time.Time
}

// Evidence is an immutable record bound to an exact output SHA (SHA-001).
type Evidence struct {
	SHA       string // exact retained coder output SHA
	TaskID    string
	CreatedAt time.Time
}

// Checkpoint gates readiness: incomplete sequence must never report ready (CHK-001).
type Checkpoint struct {
	Name string
	Done bool
}

var (
	ErrActiveDispatchExists = errors.New("taxion: active dispatch already exists (DSP-001)")
	ErrEmptySHA             = errors.New("taxion: evidence SHA must not be empty (SHA-001)")
	ErrNotReady             = errors.New("taxion: checkpoint sequence incomplete (CHK-001)")
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

// CheckReady enforces CHK-001: all checkpoints must be done.
func CheckReady(checkpoints []Checkpoint) error {
	for _, c := range checkpoints {
		if !c.Done {
			return ErrNotReady
		}
	}
	return nil
}
