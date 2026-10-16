// Package openacp connects a role-scoped, isolated OpenCode subprocess over ACP.
package openacp

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/tools"
)

type Config struct {
	Role             string
	SystemPrompt     string
	WorkingDirectory string
	CLIPath          string
	CLIHash          string
	ServerConfig     string
	ServerConfigHash string
	Configuration    json.RawMessage
	FileAccess       *core.FileAccessPolicy
	TrackedFiles     []string
	ResponseSchema   map[string]any
	BudgetGuidance   *core.BudgetGuidancePolicy
	ToolPolicy       *tools.Policy
	AvailableTools   []string
}

type response struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code int `json:"code"`
	} `json:"error"`
}

type Adapter struct {
	config Config
	root   string
	before map[string]fileSnapshot
	cmd    *exec.Cmd
	input  io.WriteCloser
	done   chan struct{}

	opMu            sync.Mutex
	writeMu         sync.Mutex
	mu              sync.Mutex
	nextID          uint64
	pending         map[string]chan response
	session         string
	model           string
	history         []runtime.Event
	wake            chan struct{}
	output          strings.Builder
	busy            bool
	stopped         bool
	closed          bool
	failure         error
	preservationErr error
	turn            int64
	turnDone        chan struct{}

	launch func(context.Context, string, string, bool) (*exec.Cmd, error)
}

var _ runtime.ConfigurationIdentity = (*Adapter)(nil)
var _ runtime.EventCursor = (*Adapter)(nil)
var _ runtime.ToolPolicyIdentity = (*Adapter)(nil)

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func checkedAsset(path, expected string) ([]byte, error) {
	if !filepath.IsAbs(path) || len(expected) != 71 || !strings.HasPrefix(expected, "sha256:") {
		return nil, fmt.Errorf("openacp: executable and server configuration require absolute paths and SHA256 bindings")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if hashBytes(data) != expected {
		return nil, fmt.Errorf("openacp: approved asset hash changed")
	}
	return data, nil
}

func New(config Config) (*Adapter, error) {
	if !slices.Contains([]string{"CODER", "REVIEWER", "ORCHESTRATOR", "PLANNER"}, config.Role) ||
		strings.TrimSpace(config.SystemPrompt) == "" {
		return nil, fmt.Errorf("openacp: explicit role and system prompt are required")
	}
	root, err := filepath.Abs(config.WorkingDirectory)
	if err != nil {
		return nil, err
	}
	config.WorkingDirectory, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	if config.FileAccess == nil {
		return nil, fmt.Errorf("openacp: explicit fileAccess is required")
	}
	if err := config.FileAccess.Validate(); err != nil {
		return nil, err
	}
	if err := config.BudgetGuidance.Validate(); err != nil {
		return nil, err
	}
	if config.BudgetGuidance != nil && config.BudgetGuidance.Mode == "account" {
		return nil, fmt.Errorf("openacp: account quota guidance is unsupported; approve flow or off guidance")
	}
	if config.ToolPolicy != nil {
		if err := config.ToolPolicy.Check(); err != nil {
			return nil, err
		}
		if len(config.ToolPolicy.Plugins)+len(config.ToolPolicy.MCP) != 0 || config.ToolPolicy.RTK != nil {
			return nil, fmt.Errorf("openacp: executable plugins, MCP and RTK extensions are unsupported")
		}
	}
	for _, name := range config.AvailableTools {
		if !slices.Contains([]string{"view", "read", "glob", "grep", "create", "edit", "apply_patch", "skill"}, name) {
			return nil, fmt.Errorf("openacp: unsupported tool %q; shell, MCP and executable extensions are not available", name)
		}
		if config.Role != "CODER" && slices.Contains([]string{"create", "edit", "apply_patch"}, name) {
			return nil, fmt.Errorf("openacp: read-only role cannot enable editing tools")
		}
	}
	config.AvailableTools = slices.Clone(config.AvailableTools)
	config.FileAccess = core.CloneFileAccessPolicy(config.FileAccess)
	config.BudgetGuidance = core.CloneBudgetGuidance(config.BudgetGuidance)
	config.TrackedFiles = slices.Clone(config.TrackedFiles)
	if config.ToolPolicy != nil {
		config.ToolPolicy = config.ToolPolicy.Clone()
	}
	config.Configuration = slices.Clone(config.Configuration)
	return &Adapter{config: config, pending: map[string]chan response{}, wake: make(chan struct{}), launch: sandboxCommand}, nil
}

func (a *Adapter) Role() string     { return a.config.Role }
func (a *Adapter) Provider() string { return "opencode" }
func (a *Adapter) Worktree() string { return a.config.WorkingDirectory }
func (a *Adapter) ConfigHash() string {
	data, _ := json.Marshal(struct {
		Adapter string `json:"adapter"`
		Config
	}{"openacp-isolated-v2", a.config})
	return hashBytes(data)
}
func (a *Adapter) ExecutionConfiguration() json.RawMessage {
	return slices.Clone(a.config.Configuration)
}
func (a *Adapter) ToolPolicy() *tools.Policy {
	if a.config.ToolPolicy == nil {
		return nil
	}
	return a.config.ToolPolicy.Clone()
}
func (a *Adapter) BudgetGuidancePolicy() *core.BudgetGuidancePolicy {
	return core.CloneBudgetGuidance(a.config.BudgetGuidance)
}

// OpenCode owns authentication and provider eligibility. This preflight checks
// only the explicitly approved local assets, never an account or a credential.
func (a *Adapter) CheckAuthentication(ctx context.Context, expectedAccount string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if expectedAccount != "opencode-managed" {
		return fmt.Errorf("openacp: account authentication is unverified; use the explicit opencode-managed reference")
	}
	if _, err := checkedAsset(a.config.CLIPath, a.config.CLIHash); err != nil {
		return err
	}
	_, _, err := a.serverSettings()
	return err
}
func (a *Adapter) CheckRepositoryAccess(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Stat(a.Worktree())
	if err == nil && !info.IsDir() {
		return fmt.Errorf("openacp: worktree is not a directory")
	}
	return err
}
func (a *Adapter) CheckServiceAccess(ctx context.Context, session string) error {
	return a.Resume(ctx, session)
}

func (a *Adapter) serverSettings() (map[string]json.RawMessage, string, error) {
	server, err := checkedAsset(a.config.ServerConfig, a.config.ServerConfigHash)
	if err != nil {
		return nil, "", err
	}
	// Operator-supplied model configuration is opaque and is never selected or
	// changed through ACP. Executable configuration cannot enter the sandbox.
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(server, &settings); err != nil || len(settings) == 0 {
		return nil, "", fmt.Errorf("openacp: server configuration requires a JSON object")
	}
	for key := range settings {
		if key != "model" {
			return nil, "", fmt.Errorf("openacp: isolated server configuration supports only the operator-prepared model field")
		}
	}
	var model string
	if json.Unmarshal(settings["model"], &model) != nil || strings.TrimSpace(model) == "" {
		return nil, "", fmt.Errorf("openacp: operator-prepared model must be a nonempty string")
	}
	return settings, model, nil
}

func (a *Adapter) prepare() error {
	binary, err := checkedAsset(a.config.CLIPath, a.config.CLIHash)
	if err != nil {
		return err
	}
	settings, model, err := a.serverSettings()
	if err != nil {
		return err
	}
	a.model = model
	a.root, err = os.MkdirTemp("", "taxion-openacp-")
	if err != nil {
		return err
	}
	for _, dir := range []string{"workspace", "config/opencode", "agent"} {
		if err := os.MkdirAll(filepath.Join(a.root, dir), 0700); err != nil {
			return err
		}
	}
	// Copy, rather than bind, the executable so a concurrent replacement of the
	// approved host binary cannot change the process after its hash is checked.
	if err := os.WriteFile(filepath.Join(a.root, "agent/opencode"), binary, 0700); err != nil {
		return err
	}
	a.before, err = copyWorkspace(a.config, filepath.Join(a.root, "workspace"))
	if err != nil {
		return err
	}
	permission := map[string]string{"*": "deny"}
	// Keep native definitions available without granting access: every ask is
	// cancelled by the ACP client, and the namespace contains no shell.
	for _, name := range []string{"bash", "read", "glob", "grep"} {
		permission[name] = "ask"
	}
	if slices.Contains(a.config.AvailableTools, "view") || slices.Contains(a.config.AvailableTools, "read") {
		permission["read"], permission["glob"], permission["grep"] = "allow", "allow", "allow"
	}
	if a.config.Role == "CODER" && (slices.Contains(a.config.AvailableTools, "create") ||
		slices.Contains(a.config.AvailableTools, "edit") || slices.Contains(a.config.AvailableTools, "apply_patch")) {
		permission["edit"] = "allow"
	}
	if a.config.ToolPolicy != nil && len(a.config.ToolPolicy.Skills) != 0 {
		permission["skill"] = "allow"
		for _, asset := range a.config.ToolPolicy.Skills {
			name := filepath.Base(asset.Path)
			if err := copySkill(asset, filepath.Join(a.root, "config/opencode/skills", name)); err != nil {
				return err
			}
		}
	}
	agent, err := json.Marshal(map[string]any{
		"mode": "primary", "permission": permission, "steps": 12,
	})
	if err != nil {
		return err
	}
	settings["agent"], err = json.Marshal(map[string]json.RawMessage{"taxion": agent})
	if err != nil {
		return err
	}
	settings["default_agent"] = json.RawMessage(`"taxion"`)
	settings["$schema"] = json.RawMessage(`"https://opencode.ai/config.json"`)
	settings["permission"], err = json.Marshal(permission)
	if err != nil {
		return err
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(a.root, "config/opencode/.gitignore"), []byte("*\n"), 0600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(a.root, "config/opencode/opencode.json"), data, 0600)
}

func (a *Adapter) Start(ctx context.Context, taskID string) (string, error) {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	a.mu.Lock()
	already := a.session != "" || a.cmd != nil || a.closed
	a.mu.Unlock()
	if already || taskID == "" {
		return "", fmt.Errorf("openacp: Start requires a fresh adapter and a Task")
	}
	if _, bounded := ctx.Deadline(); !bounded {
		return "", fmt.Errorf("openacp: an explicit execution deadline is required; quota and total token caps are unsupported")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := a.prepare(); err != nil {
		return "", err
	}
	// The subprocess outlives Start's request context, but Close/Stop always
	// kills its namespace and waits for exit before reporting cancellation.
	cmd, err := a.launch(context.Background(), a.root, filepath.Join(a.root, "agent/opencode"), a.Role() != "CODER")
	if err != nil {
		return "", err
	}
	input, err := cmd.StdinPipe()
	if err != nil {
		return "", err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		return "", errors.Join(err, input.Close())
	}
	if cmd.Stderr == nil {
		cmd.Stderr = io.Discard
	}
	if err := cmd.Start(); err != nil {
		return "", errors.Join(err, input.Close(), output.Close())
	}
	a.mu.Lock()
	a.cmd, a.input, a.done = cmd, input, make(chan struct{})
	a.mu.Unlock()
	go func() {
		a.read(output)
		err := cmd.Wait()
		a.mu.Lock()
		if !a.stopped && !a.closed && a.failure == nil {
			a.failure = fmt.Errorf("openacp: subprocess exited before close: %v", err)
		}
		close(a.done)
		close(a.wake)
		a.wake = make(chan struct{})
		a.mu.Unlock()
	}()
	var initialized struct {
		ProtocolVersion int `json:"protocolVersion"`
		AgentInfo       struct {
			Version string `json:"version"`
		} `json:"agentInfo"`
	}
	if err := a.call(ctx, "initialize", map[string]any{
		"protocolVersion": 1, "clientInfo": map[string]string{"name": "taxion", "version": "1"},
		"clientCapabilities": map[string]any{},
	}, &initialized); err != nil {
		return "", errors.Join(err, a.terminate())
	}
	if initialized.ProtocolVersion != 1 || initialized.AgentInfo.Version != "1.19.0" {
		return "", errors.Join(fmt.Errorf("openacp: requires ACP v1 and verified OpenCode 1.19.0"), a.terminate())
	}
	var session struct {
		ID      string `json:"sessionId"`
		Options []struct {
			ID       string `json:"id"`
			Category string `json:"category"`
			Value    string `json:"currentValue"`
		} `json:"configOptions"`
	}
	if err := a.call(ctx, "session/new", map[string]any{"cwd": "/workspace", "mcpServers": []any{}}, &session); err != nil {
		return "", errors.Join(err, a.terminate())
	}
	mode, observedModel := "", ""
	for _, option := range session.Options {
		if option.Category == "model" {
			observedModel = option.Value
		}
		if option.ID == "mode" {
			mode = option.Value
		}
	}
	if session.ID == "" || observedModel != a.model || mode != "taxion" {
		return "", errors.Join(fmt.Errorf("openacp: session did not confirm its model and isolated taxion agent"), a.terminate())
	}
	a.mu.Lock()
	a.session = session.ID
	a.mu.Unlock()
	return session.ID, nil
}

func (a *Adapter) call(ctx context.Context, method string, params, result any) error {
	a.mu.Lock()
	a.nextID++
	number := a.nextID
	id := fmt.Sprint(number)
	ch := make(chan response, 1)
	a.pending[id] = ch
	done := a.done
	a.mu.Unlock()
	defer func() { a.mu.Lock(); delete(a.pending, id); a.mu.Unlock() }()
	if err := a.writeContext(ctx, map[string]any{"jsonrpc": "2.0", "id": number, "method": method, "params": params}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return fmt.Errorf("openacp: connection closed during %s", method)
	case message := <-ch:
		if message.Error != nil {
			return fmt.Errorf("openacp: %s rejected (RPC code %d; server details withheld)", method, message.Error.Code)
		}
		if result != nil {
			if err := json.Unmarshal(message.Result, result); err != nil {
				return fmt.Errorf("openacp: malformed %s response: %w", method, err)
			}
		}
		return nil
	}
}

func (a *Adapter) writeContext(ctx context.Context, message any) error {
	written := make(chan error, 1)
	go func() { written <- a.write(message) }()
	select {
	case err := <-written:
		return err
	case <-ctx.Done():
		return errors.Join(ctx.Err(), a.terminate())
	}
}

func (a *Adapter) write(message any) error {
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	if a.input == nil {
		return fmt.Errorf("openacp: connection is not initialized")
	}
	return json.NewEncoder(a.input).Encode(message)
}

func (a *Adapter) read(output io.Reader) {
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 4096), 8*1024*1024)
	for scanner.Scan() {
		var message struct {
			response
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			a.protocolFailure("malformed ACP JSON")
			return
		}
		if message.Method != "" {
			if len(message.ID) != 0 {
				// No client-side filesystem, terminal or permission escalation
				// is offered. Native tools remain inside the sealed namespace.
				var reply any
				if message.Method == "session/request_permission" {
					reply = map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": map[string]any{"outcome": map[string]string{"outcome": "cancelled"}}}
				} else {
					reply = map[string]any{"jsonrpc": "2.0", "id": message.ID, "error": map[string]any{"code": -32601, "message": "Client operation unsupported"}}
				}
				if err := a.write(reply); err != nil {
					a.protocolFailure("ACP client response failed")
					return
				}
			} else if message.Method == "session/update" {
				a.update(message.Params)
			}
			continue
		}
		a.mu.Lock()
		pending := a.pending[string(message.ID)]
		a.mu.Unlock()
		if pending != nil {
			select {
			case pending <- message.response:
			default:
				a.protocolFailure("duplicate ACP response")
				return
			}
		}
	}
	if err := scanner.Err(); err != nil {
		a.protocolFailure("ACP frame exceeded limit or stream read failed")
	}
}

func (a *Adapter) protocolFailure(reason string) {
	a.mu.Lock()
	a.failure = errors.New("openacp: " + reason)
	a.mu.Unlock()
	a.emit(runtime.Event{Type: runtime.EventFailed, Payload: reason})
	if a.cmd != nil && a.cmd.Process != nil {
		_ = a.cmd.Process.Kill()
	}
}

func (a *Adapter) update(params json.RawMessage) {
	var event struct {
		Session string `json:"sessionId"`
		Update  struct {
			Type    string `json:"sessionUpdate"`
			Content struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"update"`
	}
	if json.Unmarshal(params, &event) != nil {
		a.protocolFailure("malformed session update")
		return
	}
	a.mu.Lock()
	matches := event.Session == a.session && a.busy
	if matches && event.Update.Type == "agent_message_chunk" && event.Update.Content.Type == "text" {
		if a.output.Len()+len(event.Update.Content.Text) > 256*1024 {
			a.mu.Unlock()
			a.protocolFailure("agent output exceeds 256 KiB")
			return
		}
		a.output.WriteString(event.Update.Content.Text)
	}
	a.mu.Unlock()
	if !matches {
		return
	}
	if event.Update.Type == "usage_update" {
		// This is cumulative cost/context size, not per-call token usage.
		a.emit(runtime.Event{Type: runtime.EventContextUsage, Payload: string(params)})
	} else if event.Update.Type == "agent_message_chunk" {
		a.emit(runtime.Event{Type: runtime.EventOutputDelta, Payload: event.Update.Content.Text})
	} else if event.Update.Type == "tool_call" || event.Update.Type == "tool_call_update" {
		a.emit(runtime.Event{Type: runtime.EventExecution, Payload: string(params)})
	}
}

func (a *Adapter) Send(ctx context.Context, session, prompt string) error {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	if _, bounded := ctx.Deadline(); !bounded {
		return fmt.Errorf("openacp: an explicit prompt deadline is required")
	}
	if err := a.Resume(ctx, session); err != nil {
		return err
	}
	schema, err := json.Marshal(a.config.ResponseSchema)
	if err != nil {
		return err
	}
	a.mu.Lock()
	if a.busy {
		a.mu.Unlock()
		return fmt.Errorf("openacp: session already has an active turn")
	}
	a.busy = true
	a.output.Reset()
	a.turn++
	turn := a.turn
	a.turnDone = make(chan struct{})
	turnDone := a.turnDone
	a.mu.Unlock()
	prompt = a.config.SystemPrompt + "\n\nWorkspace paths in the saved contract identify the HOST worktree. Its authorized snapshot is at /workspace. Work only there; no Git metadata or shell is provided. The host executes approved validation commands after submission. Return only one JSON object conforming to this schema:\n" +
		string(schema) + "\n\n" + prompt
	a.emit(runtime.Event{Type: "acp_capabilities", Payload: `{"transport":"stdio","isolation":"linux-namespaces-copy","providerAuthentication":"unverified","quota":"unsupported","crossProcessResume":"unsupported","shell":"unsupported","usage":"latest-assistant-message-not-total","modelOperation":"unsupported"}`})
	a.emit(runtime.Event{Type: runtime.EventTurnStarted})
	go func() {
		defer func() {
			a.mu.Lock()
			a.busy = false
			close(turnDone)
			close(a.wake)
			a.wake = make(chan struct{})
			a.mu.Unlock()
		}()
		var result struct {
			StopReason string `json:"stopReason"`
			Usage      *struct {
				Input      *int64 `json:"inputTokens"`
				Output     *int64 `json:"outputTokens"`
				CacheRead  *int64 `json:"cachedReadTokens"`
				CacheWrite *int64 `json:"cachedWriteTokens"`
			} `json:"usage"`
		}
		err := a.call(ctx, "session/prompt", map[string]any{"sessionId": session,
			"prompt": []any{map[string]string{"type": "text", "text": prompt}}}, &result)
		a.mu.Lock()
		failed := err != nil && ctx.Err() == nil && !a.stopped
		a.mu.Unlock()
		if err != nil {
			err = errors.Join(err, a.terminate())
		}
		if exportErr := exportWorkspace(a.config, filepath.Join(a.root, "workspace"), a.before); exportErr != nil {
			err = errors.Join(err, exportErr)
			a.mu.Lock()
			a.preservationErr = fmt.Errorf("openacp: partial output export failed; isolated files retained at %s: %w", a.root, exportErr)
			a.mu.Unlock()
		} else if err == nil {
			a.before, err = copyWorkspace(a.config, filepath.Join(a.root, "workspace"))
		}
		a.mu.Lock()
		output := a.output.String()
		stopped := a.stopped
		preservationErr := a.preservationErr
		a.mu.Unlock()
		if result.Usage != nil {
			status := "unavailable"
			payload, marshalErr := json.Marshal(core.DispatchUsage{Model: a.model, Scope: "latest-assistant-message",
				InputTokens: result.Usage.Input, OutputTokens: result.Usage.Output,
				CacheReadTokens: result.Usage.CacheRead, CacheWriteTokens: result.Usage.CacheWrite,
				AICreditsStatus: &status, Accounting: &core.UsageAccountingIdentity{
					SourceSessionID: session, Sequence: turn, UsageID: fmt.Sprintf("acp-prompt-%d-latest-message", turn)},
			})
			err = errors.Join(err, marshalErr)
			if marshalErr == nil {
				a.emit(runtime.Event{Type: runtime.EventUsage, Payload: string(payload)})
			}
		} else {
			a.emit(runtime.Event{Type: "acp_usage_unavailable", Payload: `{"scope":"latest-assistant-message","reason":"ACP prompt response omitted usage; not zero/free/unlimited"}`})
		}
		switch {
		case preservationErr != nil:
			a.emit(runtime.Event{Type: runtime.EventFailed, Payload: preservationErr.Error()})
		case failed:
			a.emit(runtime.Event{Type: runtime.EventFailed, Payload: err.Error()})
		case stopped || ctx.Err() != nil:
			a.emit(runtime.Event{Type: runtime.EventCancelled, Payload: "ACP process terminated; approved partial files exported"})
		case err != nil:
			a.emit(runtime.Event{Type: runtime.EventFailed, Payload: err.Error()})
		case result.StopReason != "end_turn":
			a.emit(runtime.Event{Type: runtime.EventFailed, Payload: "ACP prompt did not finish normally: " + result.StopReason})
		default:
			output = strings.TrimSpace(output)
			if strings.HasPrefix(output, "```json\n") && strings.HasSuffix(output, "\n```") {
				output = strings.TrimSuffix(strings.TrimPrefix(output, "```json\n"), "\n```")
			}
			a.emit(runtime.Event{Type: runtime.EventOutput, Payload: output})
			a.emit(runtime.Event{Type: runtime.EventTurnFinished})
		}
	}()
	return nil
}

func (a *Adapter) Resume(ctx context.Context, session string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || a.stopped || a.session == "" || a.session != session || a.failure != nil {
		return fmt.Errorf("openacp: session unavailable; cross-process/provider resume is unsupported; use an approved successor Task")
	}
	return nil
}

func (a *Adapter) terminate() error {
	a.mu.Lock()
	a.stopped = true
	cmd, done := a.cmd, a.done
	a.mu.Unlock()
	if cmd == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	default:
	}
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("openacp: process termination unconfirmed: %w", err)
	}
	select {
	case <-done:
		return nil
	case <-time.After(10 * time.Second):
		return fmt.Errorf("openacp: process termination unconfirmed; do not start a successor")
	}
}

func (a *Adapter) Stop(ctx context.Context, session string) error {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	a.mu.Lock()
	matches := a.session != "" && a.session == session
	a.mu.Unlock()
	if !matches {
		return fmt.Errorf("openacp: cannot cancel an unknown or previous-runtime session")
	}
	// session/cancel is a notification with no completion acknowledgement.
	// Termination and reaping, not that notification, prove the old run stopped.
	notifyContext, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	notifyErr := a.writeContext(notifyContext, map[string]any{"jsonrpc": "2.0", "method": "session/cancel", "params": map[string]string{"sessionId": session}})
	terminateErr := a.terminate()
	terminateErr = errors.Join(terminateErr, a.waitTurn(ctx))
	a.mu.Lock()
	terminateErr = errors.Join(terminateErr, a.preservationErr)
	a.mu.Unlock()
	if terminateErr == nil {
		return nil
	}
	return errors.Join(notifyErr, terminateErr)
}

func (a *Adapter) Close() error {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	a.mu.Lock()
	a.closed = true
	a.mu.Unlock()
	if err := a.terminate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.waitTurn(ctx); err != nil {
		return err
	}
	a.mu.Lock()
	preservationErr := a.preservationErr
	a.mu.Unlock()
	if preservationErr != nil {
		return preservationErr
	}
	if a.root != "" {
		if err := os.RemoveAll(a.root); err != nil {
			return fmt.Errorf("openacp: isolated workspace cleanup failed: %w", err)
		}
	}
	return nil
}

func (a *Adapter) waitTurn(ctx context.Context) error {
	a.mu.Lock()
	done := a.turnDone
	a.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("openacp: partial output preservation is unconfirmed: %w", ctx.Err())
	}
}

func (a *Adapter) emit(event runtime.Event) {
	a.mu.Lock()
	defer a.mu.Unlock()
	event.Sequence = uint64(len(a.history) + 1)
	a.history = append(a.history, event)
	close(a.wake)
	a.wake = make(chan struct{})
}
func (a *Adapter) EventCursor(session string) (uint64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session != session || session == "" {
		return 0, fmt.Errorf("openacp: unknown session cursor")
	}
	return uint64(len(a.history)), nil
}
func (a *Adapter) Events(ctx context.Context, session string) (<-chan runtime.Event, error) {
	return a.EventsAfter(ctx, session, 0)
}
func (a *Adapter) EventsAfter(ctx context.Context, session string, cursor uint64) (<-chan runtime.Event, error) {
	a.mu.Lock()
	if a.session != session || session == "" || cursor > uint64(len(a.history)) {
		a.mu.Unlock()
		return nil, fmt.Errorf("openacp: invalid event session or cursor")
	}
	a.mu.Unlock()
	out := make(chan runtime.Event)
	go func() {
		defer close(out)
		for {
			a.mu.Lock()
			history := slices.Clone(a.history[cursor:])
			wake, stopped, closed, busy := a.wake, a.stopped, a.closed, a.busy
			a.mu.Unlock()
			for _, event := range history {
				select {
				case out <- event:
					cursor++
				case <-ctx.Done():
					return
				}
			}
			if (stopped || closed) && !busy {
				return
			}
			select {
			case <-wake:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}
