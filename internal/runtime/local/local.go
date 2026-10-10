// Package local provides an in-process runtime.Runtime adapter.
//
// Separate Adapter instances keep coder and reviewer session registries
// isolated; sharing an instance would violate ROLE-001.
package local

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
)

var (
	ErrUnknownSession = errors.New("taxion: unknown session")
	ErrStoppedSession = errors.New("taxion: session is stopped")
)

var _ runtime.Runtime = (*Adapter)(nil)

// Adapter is an in-process Runtime. Create one per role (coder, reviewer).
type Adapter struct {
	role  string
	steps []string

	mu       sync.Mutex
	sessions map[string]*session
	seq      int
}

type session struct {
	taskID   string
	checks   *core.Sequence
	stopped  bool
	done     bool
	events   []runtime.Event
	watchers []chan runtime.Event
}

// New returns an Adapter that runs the given ordered checkpoint steps
// for every session it starts.
func New(role string, steps ...string) *Adapter {
	return &Adapter{role: role, steps: steps, sessions: map[string]*session{}}
}

func (a *Adapter) nextID() string {
	a.seq++
	return fmt.Sprintf("%s-%d-%d", a.role, time.Now().UnixNano(), a.seq)
}

// Start creates a session with its own checkpoint sequence; it runs no worker.
func (a *Adapter) Start(_ context.Context, taskID string) (string, error) {
	if taskID == "" {
		return "", fmt.Errorf("taxion: task id must not be empty")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	id := a.nextID()
	a.sessions[id] = &session{taskID: taskID, checks: core.NewSequence(a.steps...)}
	return id, nil
}

// CompleteStep marks a checkpoint done. It must be the next pending step
// (CHK-002); violations fail closed — the sequence does not advance.
func (a *Adapter) CompleteStep(_ context.Context, sessionID, name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[sessionID]
	if !ok {
		return ErrUnknownSession
	}
	if s.stopped {
		return ErrStoppedSession
	}
	if err := s.checks.Advance(name); err != nil {
		return err
	}
	ev := runtime.Event{Type: "checkpoint", Payload: name}
	s.events = append(s.events, ev)
	for _, w := range s.watchers {
		w <- ev
	}
	if s.checks.Ready() {
		s.done = true
		done := runtime.Event{Type: "done", Payload: sessionID}
		s.events = append(s.events, done)
		for _, w := range s.watchers {
			w <- done
			close(w)
		}
		s.watchers = nil
	}
	return nil
}

// Resume re-validates a session handle. Unknown or stopped sessions fail closed.
func (a *Adapter) Resume(_ context.Context, sessionID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[sessionID]
	if !ok {
		return ErrUnknownSession
	}
	if s.stopped {
		return ErrStoppedSession
	}
	return nil
}

// Stop prevents further advancement and closes live event streams.
func (a *Adapter) Stop(_ context.Context, sessionID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[sessionID]
	if !ok {
		return ErrUnknownSession
	}
	s.stopped = true
	for _, w := range s.watchers {
		close(w)
	}
	s.watchers = nil
	return nil
}

// Events streams neutral events for a session: replays history, then follows
// live. Unknown sessions are an error; the channel closes on done/stop.
// Cancelling ctx unregisters the watcher but does not close its channel.
func (a *Adapter) Events(ctx context.Context, sessionID string) (<-chan runtime.Event, error) {
	a.mu.Lock()
	s, ok := a.sessions[sessionID]
	if !ok {
		a.mu.Unlock()
		return nil, ErrUnknownSession
	}
	ch := make(chan runtime.Event, len(s.events)+8)
	for _, ev := range s.events {
		ch <- ev
	}
	if s.done || s.stopped {
		close(ch)
		a.mu.Unlock()
		return ch, nil
	}
	s.watchers = append(s.watchers, ch)
	a.mu.Unlock()
	go func() {
		<-ctx.Done()
		a.mu.Lock()
		defer a.mu.Unlock()
		for i, w := range s.watchers {
			if w == ch {
				s.watchers = append(s.watchers[:i], s.watchers[i+1:]...)
				break
			}
		}
	}()
	return ch, nil
}
