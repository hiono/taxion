package copilot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	sdk "github.com/github/copilot-sdk/go"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/tools"
)

func TestContextToolReadsOriginalWithoutProductTools(t *testing.T) {
	document, err := core.CaptureContextDocument(core.ContextDocument{
		ID: "request", Kind: "request", Source: "human:original", Version: "1",
		Text: strings.Repeat("Original text. ", 700) + "Final acceptance condition.",
	})
	if err != nil {
		t.Fatal(err)
	}
	config := testConfig(t)
	config.Role = "REVIEWER"
	config.ToolPolicy = &tools.Policy{EvidenceDirectory: t.TempDir()}
	config.ContextDocuments = []core.ContextDocument{document}
	adapter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	config.ContextDocuments[0].Text = "Caller mutation must not change this session."
	if !reflect.DeepEqual(adapter.config.AvailableTools, []string{ReadContextToolName}) ||
		strings.Contains(adapter.config.SystemPrompt, document.Text) ||
		!strings.Contains(adapter.config.SystemPrompt, "independently compare original requirements") {
		t.Fatal("source access broadened product tools or omitted independent review")
	}
	adapter.sessions["review"] = &managedSession{sdk: &sdk.Session{}, busy: true}
	defer delete(adapter.sessions, "review")
	for _, decide := range []sdk.PermissionHandlerFunc{
		adapter.sessionConfig(nil).OnPermissionRequest, adapter.resumeConfig(nil).OnPermissionRequest,
	} {
		required := true
		for _, permission := range []sdk.PermissionRequest{
			&sdk.PermissionRequestCustomTool{ToolName: ReadContextToolName},
			&sdk.PermissionRequestCustomTool{ToolName: ReadContextToolName, ManagedApprovalRequired: &required},
			&sdk.PermissionRequestCustomTool{ToolName: "write_file"},
			&sdk.PermissionRequestRead{Path: "/private/controller.db"},
		} {
			decision, err := decide(permission, sdk.PermissionInvocation{SessionID: "review"})
			custom, ok := permission.(*sdk.PermissionRequestCustomTool)
			want := ok && custom.ToolName == ReadContextToolName && custom.ManagedApprovalRequired == nil
			if err != nil || (string(decision.Kind()) == "approve-once") != want {
				t.Fatal("source tool cannot pass the actual narrow permission path", permission, decision, err)
			}
		}
	}
	handler := adapter.resumeConfig(nil).Tools[0].Handler
	invocation := sdk.ToolInvocation{SessionID: "review", ToolName: ReadContextToolName, TraceContext: context.Background(),
		Arguments: map[string]any{"id": document.ID, "hash": document.Hash, "offset": 0, "limit": 4096}}
	var original strings.Builder
	for offset := 0; ; {
		invocation.Arguments = map[string]any{"id": document.ID, "hash": document.Hash, "offset": offset, "limit": 4096}
		result, err := handler(invocation)
		var page core.ContextDocumentPage
		if err != nil || json.Unmarshal([]byte(result.TextResultForLLM), &page) != nil {
			t.Fatal("actual SDK source tool failed", err)
		}
		original.WriteString(page.Text)
		if page.EOF {
			break
		}
		offset = page.NextOffset
	}
	if original.String() != document.Text {
		t.Fatal("worker could not retrieve the complete pinned original")
	}
	for _, kind := range []string{"other-source", "write", "agent", "session", "hash", "extra"} {
		bad := invocation
		arguments := map[string]any{"id": document.ID, "hash": document.Hash, "offset": 0, "limit": 1}
		bad.Arguments = arguments
		switch kind {
		case "other-source":
			arguments["id"] = "/private/controller.db"
		case "write":
			bad.ToolName = "write_file"
		case "agent":
			bad.AgentID = "other"
		case "session":
			bad.SessionID = "other"
		case "hash":
			arguments["hash"] = "substituted"
		case "extra":
			arguments["path"] = "/private"
		}
		if _, err := handler(bad); err == nil {
			t.Fatal("source reader escaped its immutable scope", kind)
		}
	}
	if !slices.Contains(adapter.sessionConfig(nil).AvailableTools, ReadContextToolName) {
		t.Fatal("start and resume do not expose the same reader")
	}
}

func TestBrokerAllowsOnlyItsFixedContextReaderAlongsideApprovedOperations(t *testing.T) {
	config := toolsConfig(t)
	config.Role, config.AvailableTools = "CODER", nil
	config.ToolPolicy.Skills, config.ToolPolicy.Plugins = nil, nil
	binding := &config.ToolPolicy.MCP[0]
	binding.CredentialRef, binding.OperationID = "env:TAXION_BROKER_CONTEXT_TEST", "synthetic-mail"
	binding.ExpiresAt = time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
	config.AuthorizeTools = func(context.Context, string) error { return nil }
	t.Setenv("TAXION_BROKER_CONTEXT_TEST", "synthetic-host-only-canary")
	document, err := core.CaptureContextDocument(core.ContextDocument{
		ID: "original", Kind: "request", Source: "human:original", Version: "1", Text: "Original bounded request.",
	})
	if err != nil {
		t.Fatal(err)
	}
	config.ContextDocuments = []core.ContextDocument{document}
	adapter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	for _, decide := range []sdk.PermissionHandlerFunc{
		adapter.sessionConfig(nil).OnPermissionRequest, adapter.resumeConfig(nil).OnPermissionRequest,
	} {
		for _, name := range []string{ReadContextToolName, "view", "bash", "other_tool"} {
			decision, err := decide(&sdk.PermissionRequestCustomTool{ToolName: name}, sdk.PermissionInvocation{})
			if err != nil || (string(decision.Kind()) == "approve-once") != (name == ReadContextToolName) {
				t.Fatal("broker context access widened execution rights or denied originals", name, decision, err)
			}
		}
	}
}

func TestLegacyCopilotContextSurfaceAndHashUnchanged(t *testing.T) {
	config := Config{Role: "CODER", SystemPrompt: "contract", WorkingDirectory: "/workspace",
		CLIPath: "/copilot", BaseDirectory: "/home/copilot", AvailableTools: []string{"view"}}
	adapter := &Adapter{config: config}
	// Captured from the pre-context-access configuration projection.
	legacy := `{"Role":"CODER","SystemPrompt":"contract","WorkingDirectory":"/workspace","CLIPath":"/copilot","BaseDirectory":"/home/copilot","AvailableTools":["view"],"ResponseSchema":null,"PermissionPolicy":"coder-worktree-write-v1","DiscoveryPolicy":"native-cli-discovery-v2","BuiltinMCPPolicy":"disabled-per-process-v1","BrokerCredentialPolicy":"host-only-env-v1","DisabledSkills":["taxion"]}`
	sum := sha256.Sum256([]byte(legacy))
	if adapter.ConfigHash() != hex.EncodeToString(sum[:]) ||
		adapter.sessionConfig(nil).SystemMessage.Content != config.SystemPrompt ||
		!reflect.DeepEqual(adapter.sessionConfig(nil).AvailableTools, config.AvailableTools) ||
		len(adapter.managedTools()) != 0 {
		t.Fatal("legacy configuration hash, prompt or tools changed")
	}
}
