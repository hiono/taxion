package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	sdk "github.com/github/copilot-sdk/go"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/tools"
)

func toolsConfig(t *testing.T) Config {
	t.Helper()
	config := testConfig(t)
	config.Role = "REVIEWER"
	root := t.TempDir()
	skills := filepath.Join(root, "skills")
	if err := os.Mkdir(skills, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skills, "SKILL.md"), []byte("Synthetic reading skill"), 0600); err != nil {
		t.Fatal(err)
	}
	hash, err := tools.Hash(skills)
	if err != nil {
		t.Fatal(err)
	}
	config.ToolPolicy = &tools.Policy{Skills: []tools.Asset{{Path: skills, Hash: hash}},
		EvidenceDirectory: filepath.Join(root, "evidence"),
		MCP: []tools.MCPTool{{Name: "mcp_source_read", Tool: "read", Command: tools.Asset{
			Path: "/approved/server", Hash: "sha256:" + strings.Repeat("a", 64)}, Arguments: json.RawMessage(`{"source":"synthetic"}`)}},
	}
	plugin, err := filepath.Abs("testdata/skill-plugin")
	if err != nil {
		t.Fatal(err)
	}
	pluginHash, err := tools.Hash(plugin)
	if err != nil {
		t.Fatal(err)
	}
	config.ToolPolicy.Plugins = []tools.Asset{{Path: plugin, Hash: pluginHash}}
	return config
}

func TestSDKToolsCreateResumeRoleAndImmutableHash(t *testing.T) {
	config := toolsConfig(t)
	adapter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	create, resume := adapter.sessionConfig(nil), adapter.resumeConfig(nil)
	if !*create.EnableSkills || !*resume.EnableSkills || *create.EnableFileHooks || *resume.EnableFileHooks ||
		*create.EnableConfigDiscovery || *resume.EnableConfigDiscovery || create.Hooks == nil || resume.Hooks == nil ||
		!reflect.DeepEqual(create.SkillDirectories, resume.SkillDirectories) ||
		!reflect.DeepEqual(create.PluginDirectories, resume.PluginDirectories) || len(create.PluginDirectories) != 1 ||
		!slices.Contains(create.AvailableTools, "mcp_source_read") || len(create.Tools) != 1 ||
		create.Tools[0].SkipPermission || create.Tools[0].OverridesBuiltInTool {
		t.Fatal("create/resume tool surface differs or widens execution")
	}

	hash := adapter.ConfigHash()
	config.ToolPolicy.MCP[0].Args = []string{"changed"}
	if adapter.ConfigHash() != hash {
		t.Fatal("caller changed an owned tools policy")
	}
	changed, err := New(config)
	if err != nil || changed.ConfigHash() == hash {
		t.Fatal("new tools version did not change resume identity", err)
	}
	permission, err := create.OnPermissionRequest(&sdk.PermissionRequestCustomTool{ToolName: "mcp_source_read"}, sdk.PermissionInvocation{})
	if err != nil || string(permission.Kind()) != "approve-once" {
		t.Fatal("explicit Reviewer MCP request unavailable", permission, err)
	}
	for _, name := range []string{"write_file", "arbitrary", ManagedCommitToolName} {
		permission, err := create.OnPermissionRequest(&sdk.PermissionRequestCustomTool{ToolName: name}, sdk.PermissionInvocation{})
		if err != nil || string(permission.Kind()) == "approve-once" {
			t.Fatal("MCP expanded Reviewer authority", name, permission, err)
		}
	}
}

func TestLocalPluginDiscoveryWithoutModelUse(t *testing.T) {
	command, err := exec.LookPath("copilot")
	if err != nil {
		t.Skip("Native plugin acceptance requires the locally installed Copilot CLI")
	}
	plugin, err := filepath.Abs("testdata/skill-plugin")
	if err != nil {
		t.Fatal(err)
	}
	body, err := exec.Command(command, "--plugin-dir", plugin, "plugin", "list", "--json").Output()
	if err != nil || !strings.Contains(string(body), "taxion-source-review") {
		t.Fatal("native Copilot plugin discovery failed", err)
	}
}

func TestSDKHooksKeepOriginalFailureAndFailClosed(t *testing.T) {
	config := toolsConfig(t)
	config.ToolPolicy.MCP = nil
	config.AvailableTools = []string{"read_file"}
	authorized := true
	config.AuthorizeTools = func(context.Context, string) error {
		if !authorized {
			return errors.New("approval revoked")
		}
		return nil
	}
	adapter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	adapter.sessions["role-session"] = &managedSession{sdk: &sdk.Session{}, busy: true, watchers: map[*subscription]struct{}{}}
	hooks := adapter.sessionConfig(nil).Hooks
	invocation := sdk.HookInvocation{SessionID: "role-session"}
	input := sdk.PreToolUseHookInput{SessionID: "role-session", ToolName: "read_file"}
	if result, err := hooks.OnPreToolUse(input, invocation); err != nil || result != nil {
		t.Fatal("approved hook bypassed ordinary permission or failed", result, err)
	}
	input.ToolName = "bash"
	if result, err := hooks.OnPreToolUse(input, invocation); err != nil || result.PermissionDecision != "deny" {
		t.Fatal("unknown tool authorized", result, err)
	}
	authorized = false
	input.ToolName = "read_file"
	if result, err := hooks.OnPreToolUse(input, invocation); err != nil || result.PermissionDecision != "deny" {
		t.Fatal("revoked approval remained usable", result, err)
	}
	post, err := hooks.OnPostToolUse(sdk.PostToolUseHookInput{
		SessionID: "role-session", ToolName: "read_file",
		ToolResult: map[string]any{"resultType": "failure", "textResultForLLM": "synthetic private error", "exitCode": 7},
	}, invocation)
	if err != nil || post.ModifiedResult != nil || !strings.Contains(post.AdditionalContext, "sha256:") {
		t.Fatal("failure rewritten or evidence absent", post, err)
	}
	failure, err := hooks.OnPostToolUseFailure(sdk.PostToolUseFailureHookInput{
		SessionID: "role-session", ToolName: "read_file", Error: "synthetic private diagnostic",
	}, invocation)
	if err != nil || !strings.Contains(failure.AdditionalContext, "sha256:") ||
		strings.Contains(failure.AdditionalContext, "private diagnostic") {
		t.Fatal("failure evidence lost or disclosed", failure, err)
	}
	files, err := os.ReadDir(config.ToolPolicy.EvidenceDirectory)
	if err != nil || len(files) != 2 {
		t.Fatal("SDK hooks failed to persist originals", files, err)
	}
	if err := os.Chmod(config.ToolPolicy.EvidenceDirectory, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := hooks.OnPostToolUse(sdk.PostToolUseHookInput{SessionID: "role-session", ToolName: "read_file"}, invocation); err == nil {
		t.Fatal("unprotected evidence directory accepted")
	}
	history := adapter.sessions["role-session"].history
	if len(history) != 1 || history[0].Type != runtime.EventFailed {
		t.Fatal("evidence failure did not reach the controller", history)
	}
}
