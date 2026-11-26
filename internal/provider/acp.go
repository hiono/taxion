package provider

import (
	"bufio"
	"bytes"
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

const maxACPOutputBytes = 4 * 1024 * 1024
const maxACPQueuedUpdateBytes = 8 * 1024 * 1024

// ACPConfig selects one Copilot CLI ACP process for each RoleExecution.
// Tool permission requests are denied by default; this adapter does not infer
// Job authority from ACP prompts or tool metadata.
type ACPConfig struct {
	Command              string
	Args                 []string
	WorkingDirectory     string
	Identity             Identity
	PermissionAuthorizer ACPToolPermissionAuthorizer
	// Environment lists non-secret host variables explicitly passed to the CLI.
	Environment []string
}

var allowedACPEnvironment = map[string]struct{}{
	"LANG": {}, "LC_ALL": {}, "GH_HOST": {}, "COPILOT_GH_HOST": {},
	"TAXION_ACP_HELPER": {},
}

type ACPAdapter struct {
	config       ACPConfig
	mu           sync.Mutex
	closed       bool
	byRole       map[string]*acpSession
	byID         map[string]*acpSession
	byOp         map[string]StartResult
	startingRole map[string]*acpStartReservation
	startingOp   map[string]*acpStartReservation
}

type acpStartReservation struct {
	cancel context.CancelFunc
	done   chan struct{}
}

type acpSession struct {
	roleExecutionID string
	sessionID       string
	process         *acpProcess
	turnGate        chan struct{}
	stopping        atomic.Bool
	mu              sync.RWMutex
	turns           map[string]TurnResult
}

type acpProcess struct {
	cmd               *exec.Cmd
	stdin             io.WriteCloser
	pending           sync.Map
	updates           chan json.RawMessage
	done              chan struct{}
	readerDone        chan struct{}
	stop              chan struct{}
	failed            chan struct{}
	failMu            sync.Mutex
	failErr           error
	failOnce          sync.Once
	stopOnce          sync.Once
	closing           atomic.Bool
	updateMu          sync.Mutex
	updateConsumers   int
	updateBytes       int64
	updateWake        chan struct{}
	permissionMu      sync.RWMutex
	permissionHandler func(context.Context, json.RawMessage) bool
	writeGate         chan struct{}
	nextID            atomic.Uint64
}

type acpMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

func NewACPAdapter(config ACPConfig) (*ACPAdapter, error) {
	// Copy caller-owned slices before validation so later mutation cannot
	// change the command or environment that will be passed to the child.
	config.Args = append([]string(nil), config.Args...)
	config.Environment = append([]string(nil), config.Environment...)
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
	return &ACPAdapter{
		config:       config,
		byRole:       make(map[string]*acpSession),
		byID:         make(map[string]*acpSession),
		byOp:         make(map[string]StartResult),
		startingRole: make(map[string]*acpStartReservation),
		startingOp:   make(map[string]*acpStartReservation),
	}, nil
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

func acpInitialTurnID(roleExecutionID string) string {
	return "start_" + sha256Hex(roleExecutionID)[:32]
}

func acpReceiptID(prefix, value string) string {
	return prefix + sha256Hex(value)[:32]
}

func (a *ACPAdapter) Start(ctx context.Context, request StartRequest, _ CredentialLease) (StartResult, error) {
	if a == nil || ctx == nil || request.Validate() != nil {
		return StartResult{}, ErrProviderInvalidRequest
	}
	operationCtx, cancel := context.WithDeadline(ctx, request.Deadline)
	if err := operationCtx.Err(); err != nil {
		cancel()
		return StartResult{}, err
	}
	initialPrompt := acpStartPrompt(request)
	initialTurnID := acpInitialTurnID(request.RoleExecutionID)
	if !validIdentifier(initialTurnID) {
		cancel()
		return StartResult{}, ErrProviderInvalidRequest
	}
	reservation := &acpStartReservation{cancel: cancel, done: make(chan struct{})}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		cancel()
		return StartResult{}, &OperationError{Class: FailureDefinitive, Code: OperationCodeUnavailable}
	}
	if existing, ok := a.byOp[request.OperationID]; ok {
		a.mu.Unlock()
		cancel()
		return existing, nil
	}
	if a.byRole[request.RoleExecutionID] != nil || a.startingRole[request.RoleExecutionID] != nil ||
		a.startingOp[request.OperationID] != nil {
		a.mu.Unlock()
		cancel()
		return StartResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
	}
	a.startingRole[request.RoleExecutionID] = reservation
	a.startingOp[request.OperationID] = reservation
	a.mu.Unlock()
	defer func() {
		cancel()
		close(reservation.done)
		a.releaseStartReservation(request.RoleExecutionID, request.OperationID, reservation)
	}()
	process, err := a.startProcess()
	if err != nil {
		return StartResult{}, err
	}
	closeOnFailure := func() { _ = process.close(operationCtx) }
	initialized, err := process.call(operationCtx, "initialize", map[string]any{
		"protocolVersion":    1,
		"clientInfo":         map[string]string{"name": "taxion", "version": "0.1"},
		"clientCapabilities": map[string]any{},
	}, nil)
	if err != nil {
		closeOnFailure()
		return StartResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
	}
	var negotiated map[string]json.RawMessage
	var protocolVersion int
	if json.Unmarshal(initialized, &negotiated) != nil ||
		json.Unmarshal(negotiated["protocolVersion"], &protocolVersion) != nil || protocolVersion != 1 {
		closeOnFailure()
		return StartResult{}, &OperationError{Class: FailureDefinitive, Code: OperationCodeUnsupported}
	}
	if err := process.notify(operationCtx, "initialized", map[string]any{}); err != nil {
		closeOnFailure()
		return StartResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
	}
	created, err := process.call(operationCtx, "session/new", map[string]any{
		"cwd":        a.config.WorkingDirectory,
		"mcpServers": []any{},
	}, nil)
	if err != nil {
		closeOnFailure()
		return StartResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
	}
	var sessionReply map[string]json.RawMessage
	var sessionID string
	if json.Unmarshal(created, &sessionReply) != nil ||
		json.Unmarshal(sessionReply["sessionId"], &sessionID) != nil || !validIdentifier(sessionID) {
		closeOnFailure()
		return StartResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
	}
	turnGate := make(chan struct{}, 1)
	turnGate <- struct{}{}
	session := &acpSession{
		roleExecutionID: request.RoleExecutionID,
		sessionID:       sessionID,
		process:         process,
		turnGate:        turnGate,
		turns:           make(map[string]TurnResult),
	}
	if request.TaskID != "" && request.ExecutionPermissionID != "" && a.config.PermissionAuthorizer != nil {
		process.setPermissionHandler(func(permissionCtx context.Context, raw json.RawMessage) bool {
			var permissionRequest struct {
				SessionID string `json:"sessionId"`
				ToolCall  json.RawMessage `json:"toolCall"`
			}
			if json.Unmarshal(raw, &permissionRequest) != nil ||
				permissionRequest.SessionID != session.sessionID ||
				len(bytes.TrimSpace(permissionRequest.ToolCall)) == 0 ||
				bytes.Equal(bytes.TrimSpace(permissionRequest.ToolCall), []byte("null")) {
				return false
			}
			err := a.config.PermissionAuthorizer.Authorize(permissionCtx, ACPToolPermissionRequest{
				OperationID: request.OperationID, JobID: request.JobID,
				RoleExecutionID: request.RoleExecutionID, TaskID: request.TaskID,
				ExecutionPermissionID: request.ExecutionPermissionID,
				SessionID: session.sessionID, RequestSHA256: request.RequestSHA256,
				ProviderIdentity: a.config.Identity,
				ToolCall: append([]byte(nil), permissionRequest.ToolCall...),
			})
			return err == nil && !session.stopping.Load()
		})
	}
	result := StartResult{
		OperationID:    request.OperationID,
		SessionID:      session.sessionID,
		ReceiptID:      acpReceiptID("acp-session-", request.RoleExecutionID),
		EvidenceSHA256: sha256Hex(string(created)),
	}
	a.mu.Lock()
	reservationOwned := a.startingRole[request.RoleExecutionID] == reservation &&
		a.startingOp[request.OperationID] == reservation
	if a.closed || !reservationOwned || a.byRole[request.RoleExecutionID] != nil {
		a.mu.Unlock()
		closeOnFailure()
		return StartResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
	}
	if _, exists := a.byID[session.sessionID]; exists {
		a.mu.Unlock()
		closeOnFailure()
		return StartResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
	}
	delete(a.startingRole, request.RoleExecutionID)
	a.byRole[request.RoleExecutionID] = session
	a.byID[session.sessionID] = session
	a.mu.Unlock()
	initialTurn := TurnRequest{
		OperationID:  request.OperationID,
		TurnID:       initialTurnID,
		SessionID:    session.sessionID,
		PromptSHA256: sha256Hex(initialPrompt),
		Prompt:       initialPrompt,
		MaxTokens:    request.MaxTokens,
		Deadline:     request.Deadline,
	}
	if _, err := a.send(operationCtx, session, initialTurn, nil); err != nil {
		a.detachSession(session)
		session.process.terminate()
		return StartResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
	}
	a.mu.Lock()
	if a.closed || a.byID[session.sessionID] != session {
		a.mu.Unlock()
		session.process.terminate()
		return StartResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
	}
	a.byOp[request.OperationID] = result
	if a.startingOp[request.OperationID] == reservation {
		delete(a.startingOp, request.OperationID)
	}
	a.mu.Unlock()
	return result, nil
}

func (a *ACPAdapter) releaseStartReservation(roleExecutionID, operationID string, reservation *acpStartReservation) {
	a.mu.Lock()
	if a.startingRole[roleExecutionID] == reservation {
		delete(a.startingRole, roleExecutionID)
	}
	if a.startingOp[operationID] == reservation {
		delete(a.startingOp, operationID)
	}
	a.mu.Unlock()
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

func (a *ACPAdapter) Send(ctx context.Context, request TurnRequest, lease CredentialLease) (TurnResult, error) {
	if a == nil || ctx == nil || request.Validate() != nil {
		return TurnResult{}, ErrProviderInvalidRequest
	}
	a.mu.Lock()
	session := a.byID[request.SessionID]
	a.mu.Unlock()
	if session == nil {
		return TurnResult{}, &OperationError{Class: FailureDefinitive, Code: OperationCodeNotFound}
	}
	return a.send(ctx, session, request, lease)
}

func (a *ACPAdapter) send(ctx context.Context, session *acpSession, request TurnRequest, _ CredentialLease) (TurnResult, error) {
	if a == nil || ctx == nil || session == nil || request.Validate() != nil ||
		request.SessionID != session.sessionID {
		return TurnResult{}, ErrProviderInvalidRequest
	}
	operationCtx, cancel := context.WithDeadline(ctx, request.Deadline)
	defer cancel()
	a.mu.Lock()
	owned := !a.closed && a.byID[session.sessionID] == session &&
		a.byRole[session.roleExecutionID] == session
	a.mu.Unlock()
	if !owned || session.stopping.Load() {
		return TurnResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
	}
	session.mu.RLock()
	existing, cached := session.turns[request.OperationID]
	session.mu.RUnlock()
	if cached {
		return existing, nil
	}
	select {
	case <-session.turnGate:
		defer func() { session.turnGate <- struct{}{} }()
	case <-operationCtx.Done():
		return TurnResult{}, operationCtx.Err()
	case <-session.process.stop:
		return TurnResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
	case <-session.process.done:
		return TurnResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
	case <-session.process.failed:
		return TurnResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
	}
	a.mu.Lock()
	owned = !a.closed && a.byID[session.sessionID] == session &&
		a.byRole[session.roleExecutionID] == session
	a.mu.Unlock()
	if !owned || session.stopping.Load() {
		return TurnResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
	}
	session.mu.RLock()
	existing, cached = session.turns[request.OperationID]
	session.mu.RUnlock()
	if cached {
		return existing, nil
	}

	var output strings.Builder
	onUpdate := func(raw json.RawMessage) error {
		var notification struct {
			Method string `json:"method"`
			Params struct {
				SessionID string `json:"sessionId"`
				Update struct {
					SessionUpdate string `json:"sessionUpdate"`
					Content struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
				} `json:"update"`
			} `json:"params"`
		}
		if err := json.Unmarshal(raw, &notification); err != nil {
			return errors.New("invalid ACP session update")
		}
		if notification.Method != "session/update" || notification.Params.SessionID != request.SessionID {
			return errors.New("ACP session update belongs to another Session")
		}
		if notification.Params.Update.SessionUpdate != "agent_message_chunk" ||
			notification.Params.Update.Content.Type != "text" {
			return nil
		}
		chunk := notification.Params.Update.Content.Text
		if output.Len()+len(chunk) > maxACPOutputBytes {
			return errors.New("ACP output limit exceeded")
		}
		output.WriteString(chunk)
		return nil
	}
	response, err := session.process.callForSession(operationCtx, session, "session/prompt", map[string]any{
		"sessionId": request.SessionID,
		"prompt":    []map[string]string{{"type": "text", "text": request.Prompt}},
	}, onUpdate)
	if err != nil {
		if operationCtx.Err() != nil {
			cancelCtx, cancelNotify := context.WithTimeout(context.Background(), 100*time.Millisecond)
			_ = session.process.notify(cancelCtx, "session/cancel", map[string]string{"sessionId": request.SessionID})
			cancelNotify()
		}
		a.detachSession(session)
		session.process.terminate()
		return TurnResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
	}
	var promptReply struct {
		StopReason string `json:"stopReason"`
	}
	if json.Unmarshal(response, &promptReply) != nil || promptReply.StopReason != "end_turn" || output.Len() == 0 {
		return TurnResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
	}
	a.mu.Lock()
	owned = !a.closed && a.byID[session.sessionID] == session &&
		a.byRole[session.roleExecutionID] == session
	a.mu.Unlock()
	if !owned || session.stopping.Load() {
		return TurnResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
	}
	result := TurnResult{
		OperationID: request.OperationID, TurnID: request.TurnID,
		SessionID: request.SessionID, ReceiptID: acpReceiptID("acp-turn-", request.TurnID),
		Status: "COMPLETED", Output: output.String(),
		EvidenceSHA256: sha256Hex(string(response) + "\n" + output.String()),
	}
	session.mu.Lock()
	session.turns[request.OperationID] = result
	session.mu.Unlock()
	return result, nil
}

func (a *ACPAdapter) Result(ctx context.Context, request ResultRequest, _ CredentialLease) (Result, error) {
	if ctx == nil || request.Validate() != nil {
		return Result{}, ErrProviderInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	a.mu.Lock()
	session := a.byID[request.SessionID]
	a.mu.Unlock()
	if session == nil {
		return Result{}, ErrUnsupportedCapability
	}
	session.mu.RLock()
	defer session.mu.RUnlock()
	for _, turn := range session.turns {
		if turn.Status == "COMPLETED" && turn.OperationID == request.OperationID &&
			(request.TurnID == "" || turn.TurnID == request.TurnID) {
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
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	sessions := make([]*acpSession, 0, len(a.byID))
	for _, session := range a.byID {
		sessions = append(sessions, session)
	}
	reservations := make(map[*acpStartReservation]struct{}, len(a.startingRole)+len(a.startingOp))
	for _, reservation := range a.startingRole {
		reservations[reservation] = struct{}{}
	}
	for _, reservation := range a.startingOp {
		reservations[reservation] = struct{}{}
	}
	for reservation := range reservations {
		reservation.cancel()
	}
	a.startingRole = make(map[string]*acpStartReservation)
	a.startingOp = make(map[string]*acpStartReservation)
	a.byID = make(map[string]*acpSession)
	a.byRole = make(map[string]*acpSession)
	a.byOp = make(map[string]StartResult)
	a.mu.Unlock()

	var wait sync.WaitGroup
	errorsCh := make(chan error, len(sessions))
	for _, session := range sessions {
		wait.Add(1)
		go func(session *acpSession) {
			defer wait.Done()
			if err := session.process.close(context.Background()); err != nil {
				errorsCh <- err
			}
		}(session)
	}
	wait.Wait()
	close(errorsCh)
	var joined error
	for err := range errorsCh {
		joined = errors.Join(joined, err)
	}
	for reservation := range reservations {
		<-reservation.done
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
	// Leave Stderr nil so os/exec connects it directly to the null device.
	stdin, err := command.StdinPipe()
	if err != nil {
		cleanupHome()
		return nil, errors.New("start ACP stdin pipe failed")
	}
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		_ = stdin.Close()
		cleanupHome()
		return nil, errors.New("start ACP stdout pipe failed")
	}
	command.Stdout = stdoutWrite
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
		_ = stdoutRead.Close()
		_ = stdoutWrite.Close()
		cleanupHome()
		return nil, errors.New("start Copilot CLI ACP process failed")
	}
	_ = stdoutWrite.Close()
	for index := range env {
		env[index] = ""
	}
	command.Env = nil
	process := newACPProcess(command, stdin)
	go process.read(stdoutRead)
	go func() {
		_ = command.Wait()
		// Preserve buffered protocol replies, but do not let a descendant that
		// inherited stdout retain the process lifecycle or isolated HOME forever.
		timer := time.NewTimer(250 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-process.readerDone:
		case <-timer.C:
			_ = stdoutRead.Close()
			<-process.readerDone
		}
		_ = stdoutRead.Close()
		cleanupHome()
		close(process.done)
	}()
	return process, nil
}

func newACPProcess(command *exec.Cmd, stdin io.WriteCloser) *acpProcess {
	writeGate := make(chan struct{}, 1)
	writeGate <- struct{}{}
	return &acpProcess{
		cmd: command, stdin: stdin, updates: make(chan json.RawMessage, 256),
		done: make(chan struct{}), readerDone: make(chan struct{}),
		stop: make(chan struct{}), failed: make(chan struct{}),
		updateWake: make(chan struct{}, 1), writeGate: writeGate,
	}
}

func (p *acpProcess) setPermissionHandler(handler func(context.Context, json.RawMessage) bool) {
	p.permissionMu.Lock()
	p.permissionHandler = handler
	p.permissionMu.Unlock()
}

func (p *acpProcess) currentPermissionHandler() func(context.Context, json.RawMessage) bool {
	p.permissionMu.RLock()
	defer p.permissionMu.RUnlock()
	return p.permissionHandler
}

func (p *acpProcess) read(stdout io.Reader) {
	defer close(p.readerDone)
	defer p.closePending()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		var message acpMessage
		if err := json.Unmarshal(line, &message); err != nil || validateACPMessage(message) != nil {
			p.fail(errors.New("invalid ACP JSON-RPC message"))
			return
		}
		if message.Method != "" {
			if len(message.ID) != 0 {
				response := map[string]any{
					"jsonrpc": "2.0",
					"id":      json.RawMessage(message.ID),
				}
				if message.Method == "session/request_permission" || message.Method == "requestPermission" {
					outcome := map[string]string{"outcome": "cancelled"}
					var permissionParams struct {
						Options []struct {
							OptionID string `json:"optionId"`
							Kind     string `json:"kind"`
						} `json:"options"`
					}
					allowed := false
					if json.Unmarshal(message.Params, &permissionParams) == nil {
						if handler := p.currentPermissionHandler(); handler != nil {
							ctx, cancel := context.WithTimeout(context.Background(), time.Second)
							allowed = handler(ctx, message.Params)
							cancel()
						}
					}
					if allowed {
						for _, option := range permissionParams.Options {
							if option.Kind == "allow_once" && option.OptionID != "" {
								outcome = map[string]string{"outcome": "selected", "optionId": option.OptionID}
								break
							}
						}
					}
					response["result"] = map[string]any{"outcome": outcome}
				} else {
					response["error"] = map[string]any{"code": -32601, "message": "Method not found"}
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				err := p.write(ctx, response)
				cancel()
				if err != nil {
					p.fail(errors.New("ACP server request response failed"))
					return
				}
				continue
			}
			if message.Method == "session/update" {
				var notification struct {
					Params struct {
						Update struct {
							SessionUpdate string `json:"sessionUpdate"`
						} `json:"update"`
					} `json:"params"`
				}
				if json.Unmarshal(message.Params, &notification) != nil || notification.Params.Update.SessionUpdate == "" {
					p.fail(errors.New("invalid ACP session update"))
					return
				}
				if notification.Params.Update.SessionUpdate != "agent_message_chunk" {
					continue
				}
				lineBytes := int64(len(line))
				if lineBytes > maxACPQueuedUpdateBytes {
					p.fail(errors.New("ACP update frame exceeds the queued byte budget"))
					return
				}
				for {
					p.updateMu.Lock()
					if p.updateConsumers == 0 {
						p.updateMu.Unlock()
						p.fail(errors.New("ACP agent output arrived without an active Turn consumer"))
						return
					}
					if len(p.updates) < cap(p.updates) && p.updateBytes+lineBytes <= maxACPQueuedUpdateBytes {
						p.updateBytes += lineBytes
						p.updates <- line
						p.updateMu.Unlock()
						break
					}
					p.updateMu.Unlock()
					select {
					case <-p.updateWake:
						continue
					case <-p.stop:
						return
					case <-p.failed:
						return
					}
				}
			}
			continue
		}
		if waiter, ok := p.pending.LoadAndDelete(string(message.ID)); ok {
			select {
			case waiter.(chan acpMessage) <- message:
			default:
				p.fail(errors.New("duplicate ACP JSON-RPC response"))
				return
			}
		}
	}
	if err := scanner.Err(); err != nil {
		p.fail(errors.New("ACP stdout read failed"))
		return
	}
	if !p.closing.Load() {
		p.fail(errors.New("ACP stdout closed"))
	}
}


func (p *acpProcess) closePending() {
	p.pending.Range(func(key, value any) bool {
		if waiter, ok := p.pending.LoadAndDelete(key); ok {
			close(waiter.(chan acpMessage))
		}
		return true
	})
}

func (p *acpProcess) fail(err error) {
	p.failOnce.Do(func() {
		p.failMu.Lock()
		p.failErr = err
		p.failMu.Unlock()
		close(p.failed)
		p.terminate()
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

func (p *acpProcess) terminate() {
	p.stopOnce.Do(func() {
		p.closing.Store(true)
		close(p.stop)
		if p.stdin != nil {
			_ = p.stdin.Close()
		}
		if p.cmd != nil && p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
	})
}

func (p *acpProcess) signalUpdateWake() {
	select {
	case p.updateWake <- struct{}{}:
	default:
	}
}

func (p *acpProcess) releaseUpdate(update json.RawMessage) {
	p.updateMu.Lock()
	p.updateBytes -= int64(len(update))
	if p.updateBytes < 0 {
		p.updateBytes = 0
	}
	p.updateMu.Unlock()
	p.signalUpdateWake()
}

func (p *acpProcess) beginUpdateConsumer() {
	p.updateMu.Lock()
	p.updateConsumers++
	p.updateMu.Unlock()
}

func (p *acpProcess) endUpdateConsumer() error {
	p.updateMu.Lock()
	if p.updateConsumers > 0 {
		p.updateConsumers--
	}
	idle := p.updateConsumers == 0
	pending := len(p.updates)
	p.updateMu.Unlock()
	p.signalUpdateWake()
	if idle && pending > 0 {
		err := errors.New("ACP agent output remained after the Turn completed")
		p.fail(err)
		return err
	}
	return nil
}

func (p *acpProcess) drainUpdates(ctx context.Context, onUpdate func(json.RawMessage) error) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		select {
		case update := <-p.updates:
			p.releaseUpdate(update)
			if onUpdate != nil {
				if err := onUpdate(update); err != nil {
					p.fail(errors.New("ACP update processing failed"))
					return err
				}
			}
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
}


// The operation deadline takes precedence: a response received after expiry is not accepted.
func (p *acpProcess) handleResponse(response acpMessage, ok bool, ctx context.Context, onUpdate func(json.RawMessage) error) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := p.drainUpdates(ctx, onUpdate); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("ACP process exited")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(response.Error)) > 0 && !bytes.Equal(bytes.TrimSpace(response.Error), []byte("null")) {
		return nil, errors.New("ACP request failed")
	}
	if len(bytes.TrimSpace(response.Result)) == 0 {
		return nil, errors.New("ACP response result is missing")
	}
	return response.Result, nil
}

func (p *acpProcess) receiveResponse(waiter <-chan acpMessage, ctx context.Context, onUpdate func(json.RawMessage) error) (json.RawMessage, error, bool) {
	select {
	case response, ok := <-waiter:
		result, err := p.handleResponse(response, ok, ctx, onUpdate)
		return result, err, true
	default:
		return nil, nil, false
	}
}

func (p *acpProcess) call(ctx context.Context, method string, params any, onUpdate func(json.RawMessage) error) (json.RawMessage, error) {
	return p.callForSession(ctx, nil, method, params, onUpdate)
}

func (p *acpProcess) callForSession(ctx context.Context, session *acpSession, method string, params any, onUpdate func(json.RawMessage) error) (result json.RawMessage, callErr error) {
	if ctx == nil {
		return nil, ErrProviderInvalidRequest
	}
	if err := p.failure(); err != nil {
		return nil, errors.New("ACP transport failed")
	}
	if onUpdate != nil {
		p.beginUpdateConsumer()
		defer func() {
			if err := p.endUpdateConsumer(); err != nil && callErr == nil {
				result = nil
				callErr = err
			}
		}()
	}
	id := strconv.FormatUint(p.nextID.Add(1), 10)
	waiter := make(chan acpMessage, 1)
	p.pending.Store(id, waiter)
	if err := p.write(ctx, map[string]any{
		"jsonrpc": "2.0", "id": json.Number(id), "method": method, "params": params,
	}, session); err != nil {
		p.pending.Delete(id)
		return nil, err
	}
	for {
		if response, err, ok := p.receiveResponse(waiter, ctx, onUpdate); ok {
			return response, err
		}
		select {
		case response, ok := <-waiter:
			return p.handleResponse(response, ok, ctx, onUpdate)
		case update := <-p.updates:
			p.releaseUpdate(update)
			if onUpdate != nil {
				if err := onUpdate(update); err != nil {
					p.pending.Delete(id)
					p.fail(errors.New("ACP update processing failed"))
					return nil, err
				}
			}
		case <-ctx.Done():
			p.pending.Delete(id)
			return nil, ctx.Err()
		case <-p.failed:
			if response, err, ok := p.receiveResponse(waiter, ctx, onUpdate); ok {
				return response, err
			}
			p.pending.Delete(id)
			return nil, errors.New("ACP transport failed")
		case <-p.stop:
			if response, err, ok := p.receiveResponse(waiter, ctx, onUpdate); ok {
				return response, err
			}
			p.pending.Delete(id)
			return nil, errors.New("ACP transport closed")
		case <-p.done:
			if response, err, ok := p.receiveResponse(waiter, ctx, onUpdate); ok {
				return response, err
			}
			p.pending.Delete(id)
			return nil, errors.New("ACP process exited")
		}
	}
}

func (p *acpProcess) notify(ctx context.Context, method string, params any) error {
	return p.write(ctx, map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (p *acpProcess) write(ctx context.Context, message any, session ...*acpSession) error {
	if ctx == nil {
		return ErrProviderInvalidRequest
	}
	encoded, err := json.Marshal(message)
	if err != nil {
		return err
	}
	frame := append(encoded, '\n')
	select {
	case <-p.writeGate:
	case <-ctx.Done():
		return ctx.Err()
	case <-p.failed:
		return errors.New("ACP transport failed")
	case <-p.stop:
		return errors.New("ACP transport closed")
	case <-p.done:
		return errors.New("ACP process exited")
	}
	defer func() { p.writeGate <- struct{}{} }()
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(session) > 0 && session[0] != nil && session[0].stopping.Load() {
		return errors.New("ACP Session is stopping")
	}
	if p.failure() != nil {
		return errors.New("ACP transport failed")
	}
	written := make(chan error, 1)
	go func() {
		n, writeErr := p.stdin.Write(frame)
		if writeErr == nil && n != len(frame) {
			writeErr = io.ErrShortWrite
		}
		written <- writeErr
	}()
	select {
	case writeErr := <-written:
		if writeErr != nil {
			p.fail(errors.New("ACP stdin write failed"))
			return writeErr
		}
		return nil
	case <-ctx.Done():
		p.fail(errors.New("ACP stdin write interrupted"))
		return ctx.Err()
	case <-p.failed:
		return errors.New("ACP transport failed")
	case <-p.stop:
		return errors.New("ACP transport closed")
	case <-p.done:
		return errors.New("ACP process exited")
	}
}

func (p *acpProcess) close(ctx context.Context) error {
	if ctx == nil {
		return ErrProviderInvalidRequest
	}
	p.closing.Store(true)
	if p.stdin != nil {
		_ = p.stdin.Close()
	}
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
		p.terminate()
		return ctx.Err()
	case <-timer.C:
		p.terminate()
		return errors.New("ACP process termination requested; exit unconfirmed")
	}
}
