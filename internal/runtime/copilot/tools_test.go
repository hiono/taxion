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
	if err := os.WriteFile(filepath.Join(skills, "SKILL.md"),
		[]byte("---\nname: fixture-reading\ndescription: Synthetic reading instructions.\n---\nRead the supplied document.\n"), 0600); err != nil {
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
	if os.Getenv("TAXION_TEST_LOCAL_PLUGIN_DISCOVERY") != "1" {
		t.Skip("set TAXION_TEST_LOCAL_PLUGIN_DISCOVERY=1 for local model-free plugin discovery")
	}
	command, err := exec.LookPath("copilot")
	if err != nil {
		t.Fatal("requested local plugin discovery requires Copilot CLI", err)
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

func TestApprovedSkillsWithoutModelUse(t *testing.T) {
	for _, role := range []string{"CODER", "REVIEWER"} {
		t.Run(role, func(t *testing.T) {
			config := toolsConfig(t)
			config.Role = role
			config.ToolPolicy.MCP = nil
			config.ToolPolicy.Plugins = nil
			second := t.TempDir()
			if err := os.WriteFile(filepath.Join(second, "SKILL.md"),
				[]byte("---\nname: fixture-second\ndescription: Synthetic test instructions.\n---\nCheck the supplied document.\n"), 0600); err != nil {
				t.Fatal(err)
			}
			hash, err := tools.Hash(second)
			if err != nil {
				t.Fatal(err)
			}
			config.ToolPolicy.Skills = append(config.ToolPolicy.Skills, tools.Asset{Path: second, Hash: hash})
			config.AvailableTools = []string{"view"}
			adapter, err := New(config)
			if err != nil {
				t.Fatal(err)
			}
			defer adapter.Close()
			create, resume := adapter.sessionConfig(nil), adapter.resumeConfig(nil)
			expected := []string{config.ToolPolicy.Skills[0].Path, second}
			if !*create.EnableSkills || !*resume.EnableSkills ||
				!reflect.DeepEqual(create.SkillDirectories, expected) ||
				!reflect.DeepEqual(resume.SkillDirectories, expected) ||
				!slices.Contains(create.AvailableTools, "skill") ||
				!reflect.DeepEqual(create.AvailableTools, resume.AvailableTools) {
				t.Fatal("approved skills are not callable with identical create/resume bindings")
			}
			adapter.sessions["fixture-session"] = &managedSession{
				sdk: &sdk.Session{}, busy: true, watchers: map[*subscription]struct{}{},
			}
			defer delete(adapter.sessions, "fixture-session")
			invocation := sdk.HookInvocation{SessionID: "fixture-session"}
			pre := sdk.PreToolUseHookInput{SessionID: "fixture-session", ToolName: "skill"}
			if output, err := create.Hooks.OnPreToolUse(pre, invocation); err != nil || output != nil {
				t.Fatal("approved skill hook denied", output, err)
			}
			required := true
			for _, request := range []sdk.PermissionRequest{
				&sdk.PermissionRequestCustomTool{ToolName: "skill"},
				&sdk.PermissionRequestCustomTool{ToolName: "skill", ManagedApprovalRequired: &required},
				&sdk.PermissionRequestCustomTool{ToolName: "bash"},
				&sdk.PermissionRequestRead{Path: filepath.Join(second, "SKILL.md")},
			} {
				permission, err := create.OnPermissionRequest(request, sdk.PermissionInvocation{})
				want := false
				if custom, ok := request.(*sdk.PermissionRequestCustomTool); ok {
					want = custom.ToolName == "skill" && custom.ManagedApprovalRequired == nil
				}
				if err != nil || (string(permission.Kind()) == "approve-once") != want {
					t.Fatal("skill widened ordinary permissions or failed its approved request", request, permission, err)
				}
			}
			for _, phase := range []string{"result", "failure"} {
				if phase == "result" {
					_, err = create.Hooks.OnPostToolUse(sdk.PostToolUseHookInput{
						SessionID: "fixture-session", ToolName: "skill",
						ToolResult: map[string]any{"resultType": "success", "textResultForLLM": "synthetic fixture loaded"},
					}, invocation)
				} else {
					_, err = create.Hooks.OnPostToolUseFailure(sdk.PostToolUseFailureHookInput{
						SessionID: "fixture-session", ToolName: "skill", Error: "synthetic fixture failure",
					}, invocation)
				}
				if err != nil {
					t.Fatal("skill evidence missing", phase, err)
				}
			}
			files, err := os.ReadDir(config.ToolPolicy.EvidenceDirectory)
			if err != nil || len(files) != 2 {
				t.Fatal("skill success/failure originals not retained", files, err)
			}
			for _, file := range files {
				body, err := os.ReadFile(filepath.Join(config.ToolPolicy.EvidenceDirectory, file.Name()))
				if err != nil || !strings.Contains(string(body), `"tool":"skill"`) {
					t.Fatal("skill identity missing from evidence", string(body), err)
				}
			}
			if err := os.WriteFile(filepath.Join(second, "SKILL.md"), []byte("changed"), 0600); err != nil {
				t.Fatal(err)
			}
			if output, err := create.Hooks.OnPreToolUse(pre, invocation); err != nil ||
				output == nil || output.PermissionDecision != "deny" {
				t.Fatal("changed skill remained callable", output, err)
			}
			if _, err := adapter.Start(t.Context(), "fixture-task"); err == nil || adapter.started {
				t.Fatal("changed skill started a provider connection", err)
			}
		})
	}
}

func TestSkillToolRequiresApprovedAssets(t *testing.T) {
	config := testConfig(t)
	config.AvailableTools = []string{"skill"}
	if _, err := New(config); err == nil {
		t.Fatal("unapproved skill tool accepted")
	}
	config.ToolPolicy = &tools.Policy{EvidenceDirectory: t.TempDir()}
	if _, err := New(config); err == nil {
		t.Fatal("empty policy authorized skills")
	}
	config.AvailableTools = []string{}
	adapter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	if *adapter.sessionConfig(nil).EnableSkills || slices.Contains(adapter.config.AvailableTools, "skill") {
		t.Fatal("policy without skill assets enabled skills")
	}
	config.ToolPolicy = nil
	plain, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	if *plain.sessionConfig(nil).EnableSkills || *plain.resumeConfig(nil).EnableSkills {
		t.Fatal("unconfigured runtime discovered ambient skills")
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
