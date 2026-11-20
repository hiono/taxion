package domain

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrInvalidJob = errors.New("invalid job")

type JobState string

const (
	JobDraft JobState = "DRAFT"
)

type Job struct {
	ID        string    `json:"id"`
	Request   string    `json:"request"`
	Goal      string    `json:"goal,omitempty"`
	State     JobState  `json:"state"`
	Revision  uint64    `json:"revision"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func NewID(prefix string) (string, error) {
	if strings.TrimSpace(prefix) == "" {
		return "", fmt.Errorf("%w: empty identifier prefix", ErrInvalidJob)
	}
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate identifier: %w", err)
	}
	return prefix + "_" + hex.EncodeToString(value[:]), nil
}

func NewDraftJob(id, request string, now time.Time) (Job, error) {
	job := Job{
		ID:        id,
		Request:   request,
		State:     JobDraft,
		Revision:  1,
		CreatedAt: now.UTC().Round(0),
		UpdatedAt: now.UTC().Round(0),
	}
	if err := job.Validate(); err != nil {
		return Job{}, err
	}
	return job, nil
}

func (j Job) Validate() error {
	if strings.TrimSpace(j.ID) == "" {
		return fmt.Errorf("%w: id is required", ErrInvalidJob)
	}
	if strings.TrimSpace(j.Request) == "" {
		return fmt.Errorf("%w: original request is required", ErrInvalidJob)
	}
	if j.State != JobDraft {
		return fmt.Errorf("%w: unsupported initial state %q", ErrInvalidJob, j.State)
	}
	if j.Revision != 1 {
		return fmt.Errorf("%w: a newly created job must have revision 1", ErrInvalidJob)
	}
	if j.CreatedAt.IsZero() || j.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: timestamps are required", ErrInvalidJob)
	}
	if j.UpdatedAt.Before(j.CreatedAt) {
		return fmt.Errorf("%w: updated_at precedes created_at", ErrInvalidJob)
	}
	return nil
}
