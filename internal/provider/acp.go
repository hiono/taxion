package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ACPConfig selects one Copilot CLI ACP process for each RoleExecution.
// Tool permission requests are denied by default; this adapter does not infer
// Job authority from ACP prompts or tool metadata.
type ACPConfig struct {
	Command          string
	Args             []string
	WorkingDirectory string
	Identity         Identity
	// Environment lists non-secret host variables explicitly passed to the CLI.
	Environment []string
}

var allowedACPEnvironment = map[string]struct{}{
	"LANG": {}, "LC_ALL": {}, "GH_HOST": {}, "COPILOT_GH_HOST": {},
	"TAXION_ACP_HELPER": {},
}

type ACPAdapter struct {
	config ACPConfig
	mu     sync.Mutex
	byRole map[string]*acpSession
	byID   map[string]*acpSession
	byOp   map[string]StartResult
}

type acpSession struct {
	roleExecutionID string
	processID       int
	sessionID       string
	process         *acpProcess
	mu              sync.Mutex
	turns           map[string]TurnResult
}

type acpProcess struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	pending  sync.Map
	updates  chan json.RawMessage
	done     chan struct{}
	failed   chan struct{}
	failMu   sync.Mutex
	failErr  error
	failOnce sync.Once
	writeMu  sync.Mutex
	nextID   atomic.Uint64
}

type acpMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func NewACPAdapter(config ACPConfig) (*ACPAdapter, error) {
	if config.Command == "" || config.WorkingDirectory == "" {
		return nil, errors.New("ACP command and working directory are required")
	}
	if err := config.Identity.Validate(); err != nil {
		return nil, fmt.Errorf("invalid ACP Provider identity: %w", err)
	}
	absolute, err := filepath.Abs(config.WorkingDirectory)
	if err != nil {
		return nil, errors.New("ACP working directory is invalid")
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return nil, errors.New("ACP working directory is unavailable")
	}
	if len(config.Args) == 0 {
		config.Args = []string{"--acp", "--stdio"}
	}
	seenEnvironment := make(map[string]struct{}, len(config.Environment))
	for _, name := range config.Environment {
		if _, allowed := allowedACPEnvironment[name]; !allowed {
			return nil, errors.New("ACP environment contains a disallowed variable")
		}
		if _, duplicate := seenEnvironment[name]; duplicate {
			return nil, errors.New("ACP environment contains a duplicate variable")
		}
		seenEnvironment[name] = struct{}{}
	}
	config.WorkingDirectory = absolute
	return &ACPAdapter{config: config, byRole: make(map[string]*acpSession), byID: make(map[string]*acpSession), byOp: make(map[string]StartResult)}, nil
}

func (a *ACPAdapter) Identity() Identity {
	if a == nil {
		return Identity{}
	}
	return a.config.Identity
}

// Copilot CLI ACP currently provides no verified operation idempotency,
// durable lookup, usage identity, or cross-process reattachment evidence.
// Returning no capabilities keeps the production Runtime fail-closed.
func (a *ACPAdapter) Capabilities(ctx context.Context) (Capabilities, error) {
	if a == nil || ctx == nil {
		return Capabilities{}, ErrProviderInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return Capabilities{}, err
	}
	return Capabilities{Identity: a.config.Identity}, nil
}

// Start deliberately does not expose CredentialLease to Copilot CLI. ACP tools may
// inherit the CLI process environment, so passing a token there would cross the
// Tool credential boundary. The production capability gate remains fail-closed.
func (a *ACPAdapter) Start(ctx context.Context, request StartRequest, _ CredentialLease) (StartResult, error) {
	if a == nil || ctx == nil || request.Validate() != nil {
		return StartResult{}, ErrProviderInvalidRequest
	}
	a.mu.Lock()
	if existing, ok := a.byOp[request.OperationID]; ok {
		a.mu.Unlock()
		return existing, nil
	}
	if existing := a.byRole[request.RoleExecutionID]; existing != nil {
		a.mu.Unlock()
		return StartResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
	}
	if err := ctx.Err(); err != nil {
		a.mu.Unlock()
		return StartResult{}, err
	}

	process, err := a.startProcess()
	if err != nil {
		a.mu.Unlock()
		return StartResult{}, err
	}
	if _, err = process.call(ctx, "initialize", map[string]any{
		"protocolVersion":    1,
		"clientInfo":         map[string]string{"name": "taxion", "version": "0.1"},
		"clientCapabilities": map[string]any{},
	}); err != nil {
		process.close(ctx)
		a.mu.Unlock()
		return StartResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
	}
	if err := process.notify("initialized", map[string]any{}); err != nil {
		process.close(ctx)
		a.mu.Unlock()
		return StartResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
	}
	created, err := process.call(ctx, "session/new", map[string]any{
		"cwd":        a.config.WorkingDirectory,
		"mcpServers": []any{},
	})
	if err != nil {
		process.close(ctx)
		a.mu.Unlock()
		return StartResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
	}
	var sessionReply struct {
		SessionID string `json:"sessionId"`
	}
	if json.Unmarshal(created, &sessionReply) != nil || !validIdentifier(sessionReply.SessionID) {
		process.close(ctx)
		a.mu.Unlock()
		return StartResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
	}
	if _, exists := a.byID[sessionReply.SessionID]; exists {
		process.close(ctx)
		a.mu.Unlock()
		return StartResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
	}
	session := &acpSession{
		roleExecutionID: request.RoleExecutionID,
		processID:       process.cmd.Process.Pid,
		sessionID:       sessionReply.SessionID,
		process:         process,
		turns:           make(map[string]TurnResult),
	}
	result := StartResult{
		OperationID:    request.OperationID,
		SessionID:      session.sessionID,
		ReceiptID:      "acp-session-" + request.RoleExecutionID,
		EvidenceSHA256: sha256Hex(string(created)),
	}
	a.byRole[request.RoleExecutionID] = session
	a.byID[session.sessionID] = session
	a.mu.Unlock()
	initialPrompt := acpStartPrompt(request)
	initialTurn := TurnRequest{
		OperationID:  request.OperationID,
		TurnID:       "start_" + request.RoleExecutionID,
		SessionID:    session.sessionID,
		PromptSHA256: sha256Hex(initialPrompt),
		Prompt:       initialPrompt,
		MaxTokens:    request.MaxTokens,
		Deadline:     request.Deadline,
	}
	if _, err := a.Send(ctx, initialTurn, nil); err != nil {
		a.mu.Lock()
		delete(a.byRole, request.RoleExecutionID)
		delete(a.byID, session.sessionID)
		a.mu.Unlock()
		_ = process.close(ctx)
		return StartResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
	}
	a.mu.Lock()
	a.byOp[request.OperationID] = result
	a.mu.Unlock()
	return result, nil
}

func acpStartPrompt(request StartRequest) string {
	var prompt strings.Builder
	prompt.WriteString(request.Prompt)
	for _, document := range request.Context {
		prompt.WriteString("\n\n--- Context document: ")
		prompt.WriteString(document.ID)
		prompt.WriteString(" (sha256:")
		prompt.WriteString(document.SHA256)
		prompt.WriteString(") ---\n")
		prompt.WriteString(document.Content)
	}
	return prompt.String()
}

func (a *ACPAdapter) Send(ctx context.Context, request TurnRequest, _ CredentialLease) (TurnResult, error) {
	if a == nil || ctx == nil || request.Validate() != nil {
		return TurnResult{}, ErrProviderInvalidRequest
	}
	a.mu.Lock()
	session := a.byID[request.SessionID]
	a.mu.Unlock()
	if session == nil {
		return TurnResult{}, &OperationError{Class: FailureDefinitive, Code: OperationCodeNotFound}
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if existing, ok := session.turns[request.OperationID]; ok {
		return existing, nil
	}
	if err := ctx.Err(); err != nil {
		return TurnResult{}, err
	}
	for {
		select {
		case <-session.process.updates:
		default:
			goto drained
		}
	}
drained:
	response, err := session.process.call(ctx, "session/prompt", map[string]any{
		"sessionId": request.SessionID,
		"prompt":    []map[string]string{{"type": "text", "text": request.Prompt}},
	})
	if err != nil {
		_ = session.process.notify("session/cancel", map[string]string{"sessionId": request.SessionID})
		return TurnResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
	}
	var promptReply struct {
		StopReason string `json:"stopReason"`
	}
	if json.Unmarshal(response, &promptReply) != nil || promptReply.StopReason == "" {
		return TurnResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
	}
	var output strings.Builder
	for {
		select {
		case update := <-session.process.updates:
			var notification struct {
				Method string `json:"method"`
				Params struct {
					Update struct {
						SessionUpdate string `json:"sessionUpdate"`
						Content       struct {
							Type string `json:"type"`
							Text string `json:"text"`
						} `json:"content"`
					} `json:"update"`
				} `json:"params"`
			}
			if json.Unmarshal(update, &notification) == nil &&
				notification.Method == "session/update" &&
				notification.Params.Update.SessionUpdate == "agent_message_chunk" &&
				notification.Params.Update.Content.Type == "text" {
				output.WriteString(notification.Params.Update.Content.Text)
			}
		default:
			goto collected
		}
	}
collected:
	if session.process.failure() != nil {
		return TurnResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
	}
	if output.Len() == 0 {
		return TurnResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
	}
	result := TurnResult{
		OperationID: request.OperationID, TurnID: request.TurnID,
		SessionID: request.SessionID, ReceiptID: "acp-turn-" + request.TurnID,
		Status: "COMPLETED", Output: output.String(),
		EvidenceSHA256: sha256Hex(string(response) + "\n" + output.String()),
	}
	session.turns[request.OperationID] = result
	return result, nil
}

func (a *ACPAdapter) Stop(ctx context.Context, request StopRequest, _ CredentialLease) (StopResult, error) {
	if ctx == nil || request.Validate() != nil {
		return StopResult{}, ErrProviderInvalidRequest
	}
	a.mu.Lock()
	session := a.byID[request.SessionID]
	a.mu.Unlock()
	if session == nil {
		return StopResult{}, &OperationError{Class: FailureDefinitive, Code: OperationCodeNotFound}
	}
	_ = session.process.notify("session/cancel", map[string]string{"sessionId": request.SessionID})
	_ = session.process.close(ctx)
	// ACP cancellation is a request, not proof that tools or child processes are quiescent.
	return StopResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
}

func (a *ACPAdapter) Resume(context.Context, ResumeRequest, CredentialLease) (ResumeResult, error) {
	return ResumeResult{}, ErrUnsupportedCapability
}

func (a *ACPAdapter) Lookup(context.Context, LookupRequest, CredentialLease) (LookupResult, error) {
	return LookupResult{}, ErrUnsupportedCapability
}

func (a *ACPAdapter) Result(ctx context.Context, request ResultRequest, _ CredentialLease) (Result, error) {
	if err := request.Validate(); err != nil {
		return Result{}, ErrProviderInvalidRequest
	}
	a.mu.Lock()
	session := a.byID[request.SessionID]
	a.mu.Unlock()
	if session == nil {
		return Result{}, ErrUnsupportedCapability
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	for _, turn := range session.turns {
		if turn.OperationID == request.OperationID && (request.TurnID == "" || turn.TurnID == request.TurnID) {
			return Result{
				OperationID: turn.OperationID, SessionID: turn.SessionID, TurnID: turn.TurnID,
				Content: turn.Output, ResultSHA256: sha256Hex(turn.Output), Final: true,
				EvidenceSHA256: turn.EvidenceSHA256,
			}, nil
		}
	}
	return Result{}, ErrUnsupportedCapability
}

func (a *ACPAdapter) Usage(context.Context, UsageRequest, CredentialLease) (Usage, error) {
	return Usage{}, ErrUnsupportedCapability
}

func (a *ACPAdapter) ReplayEvents(context.Context, ReplayEventsRequest, CredentialLease) (EventPage, error) {
	return EventPage{}, ErrUnsupportedCapability
}

func (a *ACPAdapter) Close() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	sessions := make([]*acpSession, 0, len(a.byID))
	for _, session := range a.byID {
		sessions = append(sessions, session)
	}
	a.byID = make(map[string]*acpSession)
	a.byRole = make(map[string]*acpSession)
	a.mu.Unlock()
	var joined error
	for _, session := range sessions {
		if err := session.process.close(context.Background()); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

func (a *ACPAdapter) startProcess() (*acpProcess, error) {
	homeDir, err := os.MkdirTemp("", "taxion-acp-home-")
	if err != nil {
		return nil, errors.New("prepare isolated ACP home failed")
	}
	cleanupHome := func() { _ = os.RemoveAll(homeDir) }
	for _, directory := range []string{".config", ".cache", ".local/share"} {
		if err := os.MkdirAll(filepath.Join(homeDir, directory), 0700); err != nil {
			cleanupHome()
			return nil, errors.New("prepare isolated ACP home failed")
		}
	}
	command := exec.Command(a.config.Command, a.config.Args...)
	command.Dir = a.config.WorkingDirectory
	command.Stderr = io.Discard
	stdin, err := command.StdinPipe()
	if err != nil {
		cleanupHome()
		return nil, errors.New("start ACP stdin pipe failed")
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		cleanupHome()
		return nil, errors.New("start ACP stdout pipe failed")
	}
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + homeDir,
		"TMPDIR=" + os.TempDir(),
		"XDG_CONFIG_HOME=" + filepath.Join(homeDir, ".config"),
		"XDG_CACHE_HOME=" + filepath.Join(homeDir, ".cache"),
		"XDG_DATA_HOME=" + filepath.Join(homeDir, ".local", "share"),
	}
	for _, name := range a.config.Environment {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	command.Env = env
	if err := command.Start(); err != nil {
		for index := range env {
			env[index] = ""
		}
		command.Env = nil
		_ = stdin.Close()
		_ = stdout.Close()
		cleanupHome()
		return nil, errors.New("start Copilot CLI ACP process failed")
	}
	for index := range env {
		env[index] = ""
	}
	command.Env = nil
	process := &acpProcess{
		cmd: command, stdin: stdin, updates: make(chan json.RawMessage, 256),
		done: make(chan struct{}), failed: make(chan struct{}),
	}
	go process.read(stdout)
	go func() {
		_ = command.Wait()
		cleanupHome()
		close(process.done)
	}()
	return process, nil
}
func (p *acpProcess) read(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		var message acpMessage
		if json.Unmarshal(line, &message) != nil {
			p.fail(errors.New("invalid ACP JSON-RPC message"))
			return
		}
		if message.Method != "" {
			if len(message.ID) != 0 {
				// Permission requests are denied unless a future trusted controller
				// integration supplies a separately verified authorization handler.
				if message.Method == "session/request_permission" || message.Method == "requestPermission" {
					_ = p.write(map[string]any{
						"jsonrpc": "2.0", "id": json.RawMessage(message.ID),
						"result": map[string]any{"outcome": map[string]string{"outcome": "cancelled"}},
					})
				} else {
					_ = p.write(map[string]any{
						"jsonrpc": "2.0", "id": json.RawMessage(message.ID),
						"error": map[string]any{"code": -32601, "message": "Method not found"},
					})
				}
				continue
			}
			select {
			case p.updates <- line:
			case <-p.done:
				return
			default:
				p.fail(errors.New("ACP notification queue overflow"))
				return
			}
			continue
		}
		if len(message.ID) != 0 {
			if waiter, ok := p.pending.LoadAndDelete(string(message.ID)); ok {
				waiter.(chan acpMessage) <- message
			}
		}
	}
	if err := scanner.Err(); err != nil {
		p.fail(errors.New("ACP stdout read failed"))
	}
	for {
		closed := false
		p.pending.Range(func(key, value any) bool {
			p.pending.Delete(key)
			close(value.(chan acpMessage))
			closed = true
			return true
		})
		if !closed {
			break
		}
	}
}

func (p *acpProcess) fail(err error) {
	p.failOnce.Do(func() {
		p.failMu.Lock()
		p.failErr = err
		p.failMu.Unlock()
		close(p.failed)
		if p.cmd != nil && p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
	})
}

func (p *acpProcess) failure() error {
	select {
	case <-p.failed:
		p.failMu.Lock()
		defer p.failMu.Unlock()
		return p.failErr
	default:
		return nil
	}
}

func (p *acpProcess) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := strconv.FormatUint(p.nextID.Add(1), 10)
	waiter := make(chan acpMessage, 1)
	p.pending.Store(id, waiter)
	if err := p.write(map[string]any{"jsonrpc": "2.0", "id": json.Number(id), "method": method, "params": params}); err != nil {
		p.pending.Delete(id)
		return nil, err
	}
	select {
	case response, ok := <-waiter:
		if !ok || response.Error != nil || len(response.Result) == 0 {
			return nil, errors.New("ACP request failed")
		}
		return response.Result, nil
	case <-ctx.Done():
		p.pending.Delete(id)
		return nil, ctx.Err()
	case <-p.done:
		p.pending.Delete(id)
		return nil, errors.New("ACP process exited")
	case <-p.failed:
		p.pending.Delete(id)
		return nil, errors.New("ACP notification stream failed")
	}
}

func (p *acpProcess) notify(method string, params any) error {
	return p.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (p *acpProcess) write(message any) error {
	encoded, err := json.Marshal(message)
	if err != nil {
		return err
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if _, err := p.stdin.Write(append(encoded, '\n')); err != nil {
		return err
	}
	return nil
}

func (p *acpProcess) close(ctx context.Context) error {
	if ctx == nil {
		return ErrProviderInvalidRequest
	}
	_ = p.stdin.Close()
	wait := 2 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining < wait {
			wait = remaining
		}
	}
	if wait <= 0 {
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		<-p.done
		return ctx.Err()
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		<-p.done
		return ctx.Err()
	case <-timer.C:
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		<-p.done
		return nil
	}
}
