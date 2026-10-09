// Package proc provides a process-backed runtime.Runtime adapter.
//
// Taxion ships no LLM: it supervises a configured worker command (the "real
// worker" behind the window). Start spawns the command; stdout/stderr lines
// become output events; exit code 0 becomes done, anything else becomes
// failed (fail-closed — a crashed worker never reports readiness). Stop kills
// the process; the session can never become done afterwards.
//
// Backpressure: live watchers are buffered (256); a consumer that stops
// draining blocks the pump. This mirrors internal/runtime/local and is
// deliberate — the supervisor must not silently drop worker output.
//
// ROLE-001: create one Proc per role (coder, reviewer) so registries never
// mix. Checkpoint sequencing (CHK-002) stays in the local/ACP layer; proc
// reports raw execution only. No ledger keys are added (M2-3 wire freeze);
// "output"/"done"/"failed" are runtime-internal event types.
//
// Stdlib only (os/exec) — no SDK dependency (SDK-001).
package proc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/hiono/taxion/internal/runtime"
)

var (
	ErrUnknownSession = errors.New("taxion: unknown session")
	ErrStoppedSession = errors.New("taxion: session is stopped")
)

var _ runtime.Runtime = (*Proc)(nil)

// watcherBuf caps live event buffering per watcher (see backpressure note).
const watcherBuf = 256

// Proc supervises one worker command per session. Create one per role.
type Proc struct {
	role    string
	command string
	args    []string
	dir     string

	mu       sync.Mutex
	sessions map[string]*session
	seq      int
}

type session struct {
	taskID   string
	cmd      *exec.Cmd
	stopped  bool
	finished bool
	events   []runtime.Event
	watchers []chan runtime.Event
}

// New returns a Proc supervising command with args in dir (empty dir inherits).
// command must be non-empty; Start fails closed otherwise.
func New(role, command string, args []string, dir string) *Proc {
	return &Proc{role: role, command: command, args: args, dir: dir, sessions: map[string]*session{}}
}

func (p *Proc) nextID() string {
	p.seq++
	return fmt.Sprintf("%s-%d-%d", p.role, time.Now().UnixNano(), p.seq)
}

// Start spawns the worker for a task. Empty task IDs and unconfigured or
// missing binaries fail closed: no session is registered.
func (p *Proc) Start(_ context.Context, taskID string) (string, error) {
	if taskID == "" {
		return "", fmt.Errorf("taxion: task id must not be empty")
	}
	if p.command == "" {
		return "", fmt.Errorf("taxion: worker command is not configured")
	}
	cmd := exec.Command(p.command, p.args...)
	if p.dir != "" {
		cmd.Dir = p.dir
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	p.mu.Lock()
	id := p.nextID()
	s := &session{taskID: taskID, cmd: cmd}
	p.sessions[id] = s
	p.mu.Unlock()
	go p.pump(s, stdout, stderr, cmd)
	return id, nil
}

// pump streams worker output as events, then reports done/failed exactly once.
func (p *Proc) pump(s *session, stdout, stderr io.Reader, cmd *exec.Cmd) {
	scan := func(r io.Reader) {
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			p.emit(s, runtime.Event{Type: "output", Payload: sc.Text()})
		}
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); scan(stdout) }()
	go func() { defer wg.Done(); scan(stderr) }()
	wg.Wait()
	err := cmd.Wait()
	if err == nil {
		p.emit(s, runtime.Event{Type: "done", Payload: ""})
	} else {
		p.emit(s, runtime.Event{Type: "failed", Payload: err.Error()})
	}
	p.mu.Lock()
	s.finished = true
	for _, w := range s.watchers {
		close(w)
	}
	s.watchers = nil
	p.mu.Unlock()
}

// emit appends to history and broadcasts to live watchers. Terminal state
// (stopped) swallows late worker output — a killed worker reports nothing.
func (p *Proc) emit(s *session, ev runtime.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s.stopped {
		return
	}
	s.events = append(s.events, ev)
	for _, w := range s.watchers {
		w <- ev
	}
}

// Resume re-validates a session handle. Unknown or stopped sessions fail closed.
func (p *Proc) Resume(_ context.Context, sessionID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.sessions[sessionID]
	if !ok {
		return ErrUnknownSession
	}
	if s.stopped {
		return ErrStoppedSession
	}
	return nil
}

// Stop kills the worker. Late output is swallowed and the session can never
// become done afterwards. Stopping an unknown session is an error; stopping
// twice is a no-op.
func (p *Proc) Stop(_ context.Context, sessionID string) error {
	p.mu.Lock()
	s, ok := p.sessions[sessionID]
	if !ok {
		p.mu.Unlock()
		return ErrUnknownSession
	}
	if s.stopped {
		p.mu.Unlock()
		return nil
	}
	s.stopped = true
	for _, w := range s.watchers {
		close(w)
	}
	s.watchers = nil
	cmd := s.cmd
	p.mu.Unlock()
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	return nil
}

// Events streams neutral events for a session: replays history, then follows
// live. Unknown sessions are an error; the channel closes on done/failed/
// stop or when ctx is cancelled.
func (p *Proc) Events(ctx context.Context, sessionID string) (<-chan runtime.Event, error) {
	p.mu.Lock()
	s, ok := p.sessions[sessionID]
	if !ok {
		p.mu.Unlock()
		return nil, ErrUnknownSession
	}
	ch := make(chan runtime.Event, len(s.events)+watcherBuf)
	for _, ev := range s.events {
		ch <- ev
	}
	if s.finished || s.stopped {
		close(ch)
		p.mu.Unlock()
		return ch, nil
	}
	s.watchers = append(s.watchers, ch)
	p.mu.Unlock()
	go func() {
		<-ctx.Done()
		p.mu.Lock()
		defer p.mu.Unlock()
		for i, w := range s.watchers {
			if w == ch {
				s.watchers = append(s.watchers[:i], s.watchers[i+1:]...)
				break
			}
		}
	}()
	return ch, nil
}
