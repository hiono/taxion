package copilot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	sdk "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
	"github.com/hiono/taxion/internal/runtime"
)

var (
	ErrUnknownSession = errors.New("taxion: unknown Copilot session")
	ErrStoppedSession = errors.New("taxion: Copilot session is stopped")
	ErrClosedAdapter  = errors.New("taxion: Copilot adapter is closed")
	ErrSessionBusy    = errors.New("taxion: Copilot session already has an active turn")
)

const watcherBuffer = 256

var _ runtime.Runtime = (*Adapter)(nil)

type Config struct {
	Role             string
	SystemPrompt     string
	WorkingDirectory string
	CLIPath          string
	BaseDirectory    string
	AvailableTools   []string
	ResponseSchema   map[string]any
	ManagedCommit    *runtime.ManagedCommit
}

type Adapter struct {
	config Config
	client *sdk.Client

	opMu     sync.Mutex
	mu       sync.Mutex
	started  bool
	closed   bool
	sessions map[string]*managedSession
}

type managedSession struct {
	sdk *sdk.Session

	opMu     sync.Mutex
	emitMu   sync.Mutex
	mu       sync.Mutex
	stopped  bool
	busy     bool
	sequence uint64
	history  []runtime.Event
	watchers map[*subscription]struct{}
}

type subscription struct {
	queue chan runtime.Event
	out   chan runtime.Event
	done  chan struct{}
	once  sync.Once
}

func (s *subscription) close() {
	s.once.Do(func() { close(s.done) })
}

// New creates one isolated Copilot client for a single role and worktree.
func New(config Config) (*Adapter, error) {
	config.Role = strings.ToUpper(strings.TrimSpace(config.Role))
	if !slices.Contains([]string{"ORCHESTRATOR", "CODER", "REVIEWER", "PLANNER"}, config.Role) {
		return nil, fmt.Errorf("taxion: unsupported runtime role %q", config.Role)
	}
	if strings.TrimSpace(config.SystemPrompt) == "" {
		return nil, fmt.Errorf("taxion: system prompt must not be empty")
	}
	if config.WorkingDirectory == "" {
		return nil, fmt.Errorf("taxion: working directory must not be empty")
	}
	workingDirectory, err := filepath.Abs(config.WorkingDirectory)
	if err != nil {
		return nil, fmt.Errorf("taxion: resolve working directory: %w", err)
	}
	info, err := os.Stat(workingDirectory)
	if err != nil {
		return nil, fmt.Errorf("taxion: access working directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("taxion: working directory is not a directory")
	}
	workingDirectory, err = filepath.EvalSymlinks(workingDirectory)
	if err != nil {
		return nil, fmt.Errorf("taxion: resolve working directory symlinks: %w", err)
	}
	config.WorkingDirectory = workingDirectory
	if config.CLIPath == "" {
		config.CLIPath, err = exec.LookPath("copilot")
		if err != nil {
			return nil, fmt.Errorf("taxion: locate Copilot CLI: %w", err)
		}
	} else {
		config.CLIPath, err = exec.LookPath(config.CLIPath)
		if err != nil {
			return nil, fmt.Errorf("taxion: locate Copilot CLI: %w", err)
		}
	}
	if config.BaseDirectory == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("taxion: locate Copilot home: %w", err)
		}
		config.BaseDirectory = filepath.Join(home, ".copilot")
	}
	config.AvailableTools = slices.Clone(config.AvailableTools)
	if config.AvailableTools == nil {
		config.AvailableTools = []string{}
	}
	if config.ResponseSchema != nil {
		encoded, err := json.Marshal(config.ResponseSchema)
		if err != nil {
			return nil, fmt.Errorf("taxion: encode response schema: %w", err)
		}
		if err := json.Unmarshal(encoded, &config.ResponseSchema); err != nil {
			return nil, fmt.Errorf("taxion: copy response schema: %w", err)
		}
	}
	if slices.Contains(config.AvailableTools, ManagedCommitToolName) {
		return nil, fmt.Errorf("taxion: managed commit tool is reserved; enable it through the host binding")
	}
	if config.ManagedCommit != nil {
		binding := *config.ManagedCommit
		if config.Role != "CODER" || strings.TrimSpace(binding.DispatchID) == "" ||
			!filepath.IsAbs(binding.Scanner) || binding.Commit == nil {
			return nil, fmt.Errorf("taxion: managed commit requires a Coder, Dispatch, absolute scanner path and host handler")
		}
		config.ManagedCommit = &binding
		config.AvailableTools = append(config.AvailableTools, ManagedCommitToolName)
	}
	return &Adapter{
		config: config,
		client: sdk.NewClient(&sdk.ClientOptions{
			Connection:       sdk.StdioConnection{Path: config.CLIPath},
			WorkingDirectory: config.WorkingDirectory,
			BaseDirectory:    config.BaseDirectory,
			LogLevel:         "error",
			Mode:             sdk.ModeEmpty,
		}),
		sessions: map[string]*managedSession{},
	}, nil
}

// ConfigHash identifies the role prompt, worktree, and tool surface that must
// be supplied again when resuming a durable SDK session.
func (a *Adapter) ConfigHash() string {
	managedCommitPolicy := ""
	if a.config.ManagedCommit != nil {
		managedCommitPolicy = "managed-commit-v1"
	}
	permissionPolicy := "cli-managed-v1"
	if a.config.Role == "CODER" {
		permissionPolicy = "coder-worktree-write-v1"
	} else if a.config.Role == "REVIEWER" {
		permissionPolicy = "reviewer-read-only-v1"
	}
	data, _ := json.Marshal(struct {
		Role                string
		SystemPrompt        string
		WorkingDirectory    string
		CLIPath             string
		BaseDirectory       string
		AvailableTools      []string
		ResponseSchema      map[string]any
		PermissionPolicy    string
		ManagedCommit       *runtime.ManagedCommit `json:",omitempty"`
		ManagedCommitPolicy string                 `json:",omitempty"`
	}{
		Role:                a.config.Role,
		SystemPrompt:        a.config.SystemPrompt,
		WorkingDirectory:    a.config.WorkingDirectory,
		CLIPath:             a.config.CLIPath,
		BaseDirectory:       a.config.BaseDirectory,
		AvailableTools:      a.config.AvailableTools,
		ResponseSchema:      a.config.ResponseSchema,
		PermissionPolicy:    permissionPolicy,
		ManagedCommit:       a.config.ManagedCommit,
		ManagedCommitPolicy: managedCommitPolicy,
	})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (a *Adapter) Role() string { return a.config.Role }

func (a *Adapter) Worktree() string { return a.config.WorkingDirectory }

func (a *Adapter) Start(ctx context.Context, taskID string) (string, error) {
	if strings.TrimSpace(taskID) == "" {
		return "", fmt.Errorf("taxion: task id must not be empty")
	}
	a.opMu.Lock()
	defer a.opMu.Unlock()
	if err := a.ensureStarted(ctx); err != nil {
		return "", err
	}
	handle := &managedSession{watchers: map[*subscription]struct{}{}}
	session, err := a.client.CreateSession(ctx, a.sessionConfig(handle.onEvent))
	if err != nil {
		return "", fmt.Errorf("taxion: create Copilot session: %w", err)
	}
	handle.sdk = session
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		_ = session.Disconnect()
		return "", ErrClosedAdapter
	}
	a.sessions[session.SessionID] = handle
	a.mu.Unlock()
	return session.SessionID, nil
}

func (a *Adapter) Send(ctx context.Context, sessionID, prompt string) error {
	if strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("taxion: prompt must not be empty")
	}
	handle, err := a.lookup(sessionID)
	if err != nil {
		return err
	}
	handle.opMu.Lock()
	defer handle.opMu.Unlock()
	handle.mu.Lock()
	session, stopped := handle.sdk, handle.stopped
	busy := handle.busy
	if !stopped && session != nil && busy {
		handle.mu.Unlock()
		return ErrSessionBusy
	}
	if !stopped && session != nil {
		handle.busy = true
	}
	handle.mu.Unlock()
	if stopped || session == nil {
		return ErrStoppedSession
	}
	if _, err := session.Send(ctx, sdk.MessageOptions{
		Prompt: prompt, ResponseSchema: a.config.ResponseSchema,
	}); err != nil {
		handle.emit(runtime.Event{Type: runtime.EventFailed, Payload: err.Error()})
		return fmt.Errorf("taxion: send Copilot instruction: %w", err)
	}
	return nil
}

func (a *Adapter) Resume(ctx context.Context, sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("taxion: session id must not be empty")
	}
	a.opMu.Lock()
	defer a.opMu.Unlock()
	if err := a.ensureStarted(ctx); err != nil {
		return err
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return ErrClosedAdapter
	}
	handle := a.sessions[sessionID]
	if handle == nil {
		handle = &managedSession{watchers: map[*subscription]struct{}{}}
	}
	a.mu.Unlock()

	handle.opMu.Lock()
	defer handle.opMu.Unlock()
	handle.mu.Lock()
	if !handle.stopped && handle.sdk != nil {
		handle.mu.Unlock()
		return nil
	}
	handle.stopped = false
	handle.mu.Unlock()
	session, err := a.client.ResumeSession(ctx, sessionID, a.resumeConfig(handle.onEvent))
	if err != nil {
		handle.mu.Lock()
		handle.stopped = true
		handle.mu.Unlock()
		return fmt.Errorf("taxion: resume Copilot session: %w", err)
	}
	handle.mu.Lock()
	handle.sdk = session
	handle.mu.Unlock()
	a.mu.Lock()
	a.sessions[sessionID] = handle
	a.mu.Unlock()
	return nil
}

func (a *Adapter) Stop(ctx context.Context, sessionID string) error {
	handle, err := a.lookup(sessionID)
	if err != nil {
		return err
	}
	handle.opMu.Lock()
	defer handle.opMu.Unlock()
	handle.mu.Lock()
	session, stopped := handle.sdk, handle.stopped
	handle.mu.Unlock()
	if stopped || session == nil {
		return nil
	}
	handle.mu.Lock()
	busy := handle.busy
	handle.mu.Unlock()
	if busy {
		if err := session.Abort(ctx); err != nil {
			return fmt.Errorf("taxion: abort Copilot session: %w", err)
		}
		handle.mu.Lock()
		stillBusy := handle.busy
		handle.mu.Unlock()
		if stillBusy {
			handle.emit(runtime.Event{Type: runtime.EventCancelled, Payload: "stopped"})
		}
	}
	handle.mu.Lock()
	handle.stopped = true
	handle.sdk = nil
	watchers := handle.watchers
	handle.watchers = map[*subscription]struct{}{}
	handle.mu.Unlock()
	for watcher := range watchers {
		watcher.close()
	}
	if err := session.Disconnect(); err != nil {
		return fmt.Errorf("taxion: disconnect Copilot session: %w", err)
	}
	return nil
}

func (a *Adapter) Events(ctx context.Context, sessionID string) (<-chan runtime.Event, error) {
	return a.EventsAfter(ctx, sessionID, 0)
}

func (a *Adapter) EventCursor(sessionID string) (uint64, error) {
	handle, err := a.lookup(sessionID)
	if err != nil {
		return 0, err
	}
	handle.mu.Lock()
	defer handle.mu.Unlock()
	return handle.sequence, nil
}

func (a *Adapter) EventsAfter(ctx context.Context, sessionID string, cursor uint64) (<-chan runtime.Event, error) {
	if ctx == nil {
		return nil, fmt.Errorf("taxion: event context must not be nil")
	}
	handle, err := a.lookup(sessionID)
	if err != nil {
		return nil, err
	}
	handle.mu.Lock()
	if handle.stopped || handle.sdk == nil {
		out := make(chan runtime.Event, len(handle.history))
		for _, event := range handle.history {
			if event.Sequence > cursor {
				out <- event
			}
		}
		close(out)
		handle.mu.Unlock()
		return out, nil
	}
	replay := 0
	for _, event := range handle.history {
		if event.Sequence > cursor {
			replay++
		}
	}
	sub := &subscription{
		queue: make(chan runtime.Event, replay+watcherBuffer),
		out:   make(chan runtime.Event, watcherBuffer),
		done:  make(chan struct{}),
	}
	for _, event := range handle.history {
		if event.Sequence > cursor {
			sub.queue <- event
		}
	}
	handle.watchers[sub] = struct{}{}
	handle.mu.Unlock()
	go handle.forward(ctx, sub)
	return sub.out, nil
}

func (a *Adapter) Close() error {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	started := a.started
	sessions := make([]*managedSession, 0, len(a.sessions))
	for _, session := range a.sessions {
		sessions = append(sessions, session)
	}
	a.mu.Unlock()

	var errs []error
	for _, handle := range sessions {
		handle.opMu.Lock()
		handle.mu.Lock()
		session := handle.sdk
		handle.mu.Unlock()
		if session != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			handle.mu.Lock()
			busy := handle.busy
			handle.mu.Unlock()
			if busy {
				if err := session.Abort(ctx); err != nil {
					errs = append(errs, fmt.Errorf("abort Copilot session %s: %w", session.SessionID, err))
				} else {
					handle.mu.Lock()
					stillBusy := handle.busy
					handle.mu.Unlock()
					if stillBusy {
						handle.emit(runtime.Event{Type: runtime.EventCancelled, Payload: "adapter closed"})
					}
				}
			}
			cancel()
			handle.mu.Lock()
			handle.stopped = true
			handle.sdk = nil
			watchers := handle.watchers
			handle.watchers = map[*subscription]struct{}{}
			handle.mu.Unlock()
			for watcher := range watchers {
				watcher.close()
			}
			if err := session.Disconnect(); err != nil {
				errs = append(errs, fmt.Errorf("disconnect Copilot session %s: %w", session.SessionID, err))
			}
		}
		handle.opMu.Unlock()
	}
	if started {
		if err := a.client.Stop(); err != nil {
			errs = append(errs, fmt.Errorf("stop Copilot client: %w", err))
		}
	}
	return errors.Join(errs...)
}

func (a *Adapter) ensureStarted(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return ErrClosedAdapter
	}
	if a.started {
		return nil
	}
	if err := a.client.Start(ctx); err != nil {
		return fmt.Errorf("taxion: start Copilot client: %w", err)
	}
	a.started = true
	return nil
}

func (a *Adapter) lookup(sessionID string) (*managedSession, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	handle := a.sessions[sessionID]
	if handle == nil {
		return nil, ErrUnknownSession
	}
	return handle, nil
}

func (a *Adapter) sessionConfig(onEvent sdk.SessionEventHandler) *sdk.SessionConfig {
	streaming, disabled := true, false
	return &sdk.SessionConfig{
		Model:                              "auto",
		SystemMessage:                      &sdk.SystemMessageConfig{Mode: "append", Content: a.config.SystemPrompt},
		WorkingDirectory:                   a.config.WorkingDirectory,
		AvailableTools:                     slices.Clone(a.config.AvailableTools),
		Tools:                              a.managedTools(),
		Streaming:                          &streaming,
		EnableConfigDiscovery:              &disabled,
		EnableSkills:                       &disabled,
		EnableFileHooks:                    &disabled,
		EnableOnDemandInstructionDiscovery: &disabled,
		InfiniteSessions:                   infiniteSessions(),
		OnPermissionRequest:                a.permissionHandler(),
		OnEvent:                            onEvent,
	}
}

func (a *Adapter) resumeConfig(onEvent sdk.SessionEventHandler) *sdk.ResumeSessionConfig {
	streaming, disabled := true, false
	return &sdk.ResumeSessionConfig{
		Model:                              "auto",
		SystemMessage:                      &sdk.SystemMessageConfig{Mode: "append", Content: a.config.SystemPrompt},
		WorkingDirectory:                   a.config.WorkingDirectory,
		AvailableTools:                     slices.Clone(a.config.AvailableTools),
		Tools:                              a.managedTools(),
		Streaming:                          &streaming,
		EnableConfigDiscovery:              &disabled,
		EnableSkills:                       &disabled,
		EnableFileHooks:                    &disabled,
		EnableOnDemandInstructionDiscovery: &disabled,
		InfiniteSessions:                   infiniteSessions(),
		OnPermissionRequest:                a.permissionHandler(),
		OnEvent:                            onEvent,
	}
}

func infiniteSessions() *sdk.InfiniteSessionConfig {
	enabled := true
	return &sdk.InfiniteSessionConfig{Enabled: &enabled}
}

func (a *Adapter) permissionHandler() sdk.PermissionHandlerFunc {
	if a.config.Role != "REVIEWER" && a.config.Role != "CODER" {
		return nil
	}
	return func(request sdk.PermissionRequest, invocation sdk.PermissionInvocation) (rpc.PermissionDecision, error) {
		decision := reviewerPermissionDecision(a.config.WorkingDirectory, request)
		if a.config.Role == "CODER" {
			decision = coderPermissionDecision(a.config.WorkingDirectory, a.config.AvailableTools, request)
		}
		if handle, err := a.lookup(invocation.SessionID); err == nil {
			payload, _ := json.Marshal(struct {
				RequestKind string `json:"requestKind"`
				Decision    string `json:"decision"`
			}{RequestKind: string(request.Kind()), Decision: string(decision.Kind())})
			handle.emit(runtime.Event{Type: runtime.EventPermission, Payload: string(payload)})
		}
		return decision, nil
	}
}

func coderPermissionDecision(worktree string, availableTools []string, request sdk.PermissionRequest) rpc.PermissionDecision {
	reject := func() rpc.PermissionDecision {
		reason := "Coder permissions are limited to the assigned worktree and explicitly allowlisted tools"
		return &rpc.PermissionDecisionReject{Feedback: &reason}
	}
	switch permission := request.(type) {
	case *sdk.PermissionRequestRead:
		return reviewerPermissionDecision(worktree, permission)
	case *sdk.PermissionRequestWrite:
		if permission.RequestSandboxBypass != nil && *permission.RequestSandboxBypass ||
			permission.ManagedApprovalRequired != nil && *permission.ManagedApprovalRequired {
			return reject()
		}
		target := permission.FileName
		if permission.ResolvedPath != nil && *permission.ResolvedPath != "" {
			target = *permission.ResolvedPath
		}
		target, err := canonicalWriteTarget(worktree, target)
		if err != nil || !within(worktree, target) {
			return reject()
		}
		return &rpc.PermissionDecisionApproveOnce{}
	case *sdk.PermissionRequestCustomTool:
		if slices.Contains(availableTools, permission.ToolName) &&
			(permission.ManagedApprovalRequired == nil || !*permission.ManagedApprovalRequired) {
			return &rpc.PermissionDecisionApproveOnce{}
		}
		return reject()
	default:
		return reject()
	}
}

func canonicalWriteTarget(worktree, target string) (string, error) {
	if !filepath.IsAbs(target) {
		target = filepath.Join(worktree, target)
	}
	target, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		return resolved, nil
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(target))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(target)), nil
}

func reviewerPermissionDecision(worktree string, request sdk.PermissionRequest) rpc.PermissionDecision {
	read, ok := request.(*sdk.PermissionRequestRead)
	if !ok || read.RequestSandboxBypass != nil && *read.RequestSandboxBypass {
		reason := "Reviewer role permits reads only within its assigned worktree"
		return &rpc.PermissionDecisionReject{Feedback: &reason}
	}
	target := read.Path
	if read.ResolvedPath != nil && *read.ResolvedPath != "" {
		target = *read.ResolvedPath
	}
	target, err := filepath.EvalSymlinks(target)
	if err != nil || !within(worktree, target) {
		reason := "Reviewer role may read only within its assigned worktree"
		return &rpc.PermissionDecisionReject{Feedback: &reason}
	}
	return &rpc.PermissionDecisionApproveReadOnlyForSession{Directories: []string{worktree}}
}

func within(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) &&
		!filepath.IsAbs(relative)
}

func (h *managedSession) onEvent(event sdk.SessionEvent) {
	mapped, ok := normalizeEvent(event)
	if ok {
		h.emit(mapped)
	}
}

func normalizeEvent(event sdk.SessionEvent) (runtime.Event, bool) {
	switch data := event.Data.(type) {
	case *sdk.AssistantMessageData:
		return runtime.Event{Type: runtime.EventOutput, Payload: data.Content}, true
	case *sdk.AssistantMessageDeltaData:
		return runtime.Event{Type: runtime.EventOutputDelta, Payload: data.DeltaContent}, true
	case *sdk.AssistantTurnStartData:
		return runtime.Event{Type: runtime.EventTurnStarted}, true
	case *sdk.AssistantTurnEndData:
		return runtime.Event{Type: runtime.EventTurnEnded}, true
	case *sdk.SessionIdleData:
		if data.Aborted != nil && *data.Aborted {
			return runtime.Event{Type: runtime.EventCancelled}, true
		}
		return runtime.Event{Type: runtime.EventTurnFinished}, true
	case *sdk.SessionErrorData:
		return runtime.Event{Type: runtime.EventFailed, Payload: data.Message}, true
	case *sdk.ModelCallFailureData:
		message := "model call failed"
		if data.ErrorMessage != nil {
			message = *data.ErrorMessage
		}
		return runtime.Event{Type: runtime.EventFailed, Payload: message}, true
	case *sdk.AssistantUsageData:
		payload, err := json.Marshal(struct {
			Model            string `json:"model"`
			IsAuto           *bool  `json:"isAuto,omitempty"`
			InputTokens      *int64 `json:"inputTokens,omitempty"`
			OutputTokens     *int64 `json:"outputTokens,omitempty"`
			CacheReadTokens  *int64 `json:"cacheReadTokens,omitempty"`
			CacheWriteTokens *int64 `json:"cacheWriteTokens,omitempty"`
		}{
			Model: data.Model, IsAuto: data.IsAuto, InputTokens: data.InputTokens,
			OutputTokens: data.OutputTokens, CacheReadTokens: data.CacheReadTokens,
			CacheWriteTokens: data.CacheWriteTokens,
		})
		if err != nil {
			return runtime.Event{}, false
		}
		return runtime.Event{Type: runtime.EventUsage, Payload: string(payload)}, true
	case *sdk.SessionUsageInfoData:
		payload, err := json.Marshal(struct {
			CurrentTokens  int64  `json:"currentTokens"`
			TokenLimit     int64  `json:"tokenLimit"`
			Conversation   *int64 `json:"conversationTokens,omitempty"`
			System         *int64 `json:"systemTokens,omitempty"`
			ToolDefinition *int64 `json:"toolDefinitionsTokens,omitempty"`
			Messages       int64  `json:"messagesLength"`
		}{
			CurrentTokens: data.CurrentTokens, TokenLimit: data.TokenLimit,
			Conversation: data.ConversationTokens, System: data.SystemTokens,
			ToolDefinition: data.ToolDefinitionsTokens, Messages: data.MessagesLength,
		})
		if err != nil {
			return runtime.Event{}, false
		}
		return runtime.Event{Type: runtime.EventContextUsage, Payload: string(payload)}, true
	case *sdk.SessionCompactionStartData:
		payload, err := json.Marshal(struct {
			Phase              string  `json:"phase"`
			Model              *string `json:"model,omitempty"`
			CurrentTokens      *int64  `json:"currentTokens,omitempty"`
			TokenLimit         *int64  `json:"tokenLimit,omitempty"`
			ConversationTokens *int64  `json:"conversationTokens,omitempty"`
		}{
			Phase: "started", Model: data.Model, CurrentTokens: data.CurrentTokens,
			TokenLimit: data.TokenLimit, ConversationTokens: data.ConversationTokens,
		})
		if err != nil {
			return runtime.Event{}, false
		}
		return runtime.Event{Type: runtime.EventCompaction, Payload: string(payload)}, true
	case *sdk.SessionCompactionCompleteData:
		payload, err := json.Marshal(struct {
			Phase                 string  `json:"phase"`
			Success               bool    `json:"success"`
			Model                 *string `json:"model,omitempty"`
			SystemTokens          *int64  `json:"systemTokens,omitempty"`
			ConversationTokens    *int64  `json:"conversationTokens,omitempty"`
			PreCompactionTokens   *int64  `json:"preCompactionTokens,omitempty"`
			PostCompactionTokens  *int64  `json:"postCompactionTokens,omitempty"`
			TokensRemoved         *int64  `json:"tokensRemoved,omitempty"`
			TokenLimit            *int64  `json:"tokenLimit,omitempty"`
			PreCompactionMessages *int64  `json:"preCompactionMessagesLength,omitempty"`
			MessagesRemoved       *int64  `json:"messagesRemoved,omitempty"`
		}{
			Phase: "completed", Success: data.Success, Model: data.BehaviorModelID,
			SystemTokens: data.SystemTokens, ConversationTokens: data.ConversationTokens,
			PreCompactionTokens: data.PreCompactionTokens, PostCompactionTokens: data.PostCompactionTokens,
			TokensRemoved: data.TokensRemoved, TokenLimit: data.TokenLimit,
			PreCompactionMessages: data.PreCompactionMessagesLength, MessagesRemoved: data.MessagesRemoved,
		})
		if err != nil {
			return runtime.Event{}, false
		}
		return runtime.Event{Type: runtime.EventCompaction, Payload: string(payload)}, true
	case *sdk.PermissionCompletedData:
		payload, err := json.Marshal(struct {
			Result string `json:"result"`
		}{Result: string(data.Result.Kind())})
		if err != nil {
			return runtime.Event{}, false
		}
		return runtime.Event{Type: runtime.EventPermission, Payload: string(payload)}, true
	case *sdk.AbortData:
		return runtime.Event{Type: runtime.EventCancelled, Payload: string(data.Reason)}, true
	default:
		return runtime.Event{}, false
	}
}

func (h *managedSession) emit(event runtime.Event) {
	h.emitMu.Lock()
	defer h.emitMu.Unlock()
	h.mu.Lock()
	if h.stopped {
		h.mu.Unlock()
		return
	}
	if event.Type == runtime.EventCancelled && !h.busy {
		h.mu.Unlock()
		return
	}
	h.sequence++
	event.Sequence = h.sequence
	switch event.Type {
	case runtime.EventTurnFinished, runtime.EventFailed, runtime.EventCancelled:
		h.busy = false
	}
	h.history = append(h.history, event)
	watchers := make([]*subscription, 0, len(h.watchers))
	for watcher := range h.watchers {
		watchers = append(watchers, watcher)
	}
	h.mu.Unlock()
	for _, watcher := range watchers {
		select {
		case watcher.queue <- event:
		case <-watcher.done:
		}
	}
}

func (h *managedSession) forward(ctx context.Context, watcher *subscription) {
	defer close(watcher.out)
	defer func() {
		h.mu.Lock()
		delete(h.watchers, watcher)
		h.mu.Unlock()
	}()
	for {
		select {
		case event := <-watcher.queue:
			select {
			case watcher.out <- event:
			case <-watcher.done:
				return
			case <-ctx.Done():
				watcher.close()
				return
			}
		case <-watcher.done:
			return
		case <-ctx.Done():
			watcher.close()
			return
		}
	}
}
