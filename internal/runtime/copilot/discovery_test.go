package copilot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/tools"
)

func TestNativeDoctorUsesExecutionContextWithoutModelOrForeignPaths(t *testing.T) {
	home, root, base := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("COPILOT_HOME", base)
	t.Setenv("COPILOT_CUSTOM_INSTRUCTIONS_DIRS", "extra")
	t.Setenv("TAXION_INSPECTION_ROOT", root)
	t.Setenv("TAXION_INSPECTION_HOME", base)
	for path, content := range map[string]string{
		filepath.Join(root, ".git", "HEAD"):                           "ref: refs/heads/main\n",
		filepath.Join(root, ".github/agents/a.agent.md"):              "---\nname: fixture\n---\nRead.",
		filepath.Join(base, "agents/a.md"):                            "---\nname: override\n---\nRead.",
		filepath.Join(root, ".github/agents/broken.md"):               "not frontmatter",
		filepath.Join(root, ".github/hooks/check.json"):               `{"hooks":{"sessionStart":[{"command":"touch SHOULD_NOT_RUN"}]}}`,
		filepath.Join(home, ".pi/agent/skills/foreign/SKILL.md"):      "FOREIGN_BACKEND",
		filepath.Join(home, ".config/opencode/skills/other/SKILL.md"): "FOREIGN_BACKEND",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cli := filepath.Join(t.TempDir(), "copilot")
	script := `#!/bin/sh
[ "$PWD" = "$TAXION_INSPECTION_ROOT" ] || exit 10
[ "$COPILOT_HOME" = "$TAXION_INSPECTION_HOME" ] || exit 11
[ "$1" = --no-auto-update ] || exit 12
shift
[ "$1" = --disable-builtin-mcps ] || exit 15
shift
[ "$1" = --version ] && { printf 'fixture-1\n'; exit 0; }
[ "$2" = list ] && [ "$3" = --json ] || exit 13
case "$1" in
skill) printf '[{"name":"common","source":"project","path":".agents/skills/common","enabled":true},{"name":"disabled","source":"personal","enabled":false}]';;
plugin) printf '[{"name":"fixture-plugin","source":"marketplace","enabled":true}]';;
instruction) printf '[{"id":"AGENTS.md","sourcePath":"AGENTS.md","location":"project","defaultDisabled":false}]';;
mcp) printf '{"mcpServers":{"fixture":{"enabled":true,"source":"user","env":{"TOKEN":"DO_NOT_PRINT"},"headers":{"Authorization":"DO_NOT_PRINT"},"args":["DO_NOT_PRINT"]}}}';;
lsp) printf '[]';;
*) exit 14;;
esac
`
	if err := os.WriteFile(cli, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	config := testConfig(t)
	config.WorkingDirectory, config.CLIPath, config.BaseDirectory = root, cli, ""
	adapter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	if adapter.config.BaseDirectory != base {
		t.Fatal("execution ignores COPILOT_HOME")
	}
	report, err := adapter.Inspect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, unexpected := range []string{"DO_NOT_PRINT", "FOREIGN_BACKEND", ".pi/", ".opencode/", "SHOULD_NOT_RUN"} {
		if strings.Contains(string(body), unexpected) {
			t.Fatal("doctor leaks configuration contents or scans an unselected backend", unexpected)
		}
	}
	if adapter.started || len(adapter.sessions) != 0 || !report.BuiltinMCPsDisabled {
		t.Fatal("doctor starts an SDK session")
	}
	if _, err := os.Stat(filepath.Join(root, "SHOULD_NOT_RUN")); !os.IsNotExist(err) {
		t.Fatal("doctor executes hooks")
	}
	states := map[string]string{}
	for _, asset := range report.Assets {
		states[asset.Kind+":"+asset.Name+":"+asset.Source] = asset.State
	}
	for key, expected := range map[string]string{
		"skill:common:project":              "enabled",
		"skill:disabled:personal":           "disabled",
		"agent:a:project":                   "shadowed",
		"agent:a:Copilot home":              "unavailable",
		"agent:broken.md:project":           "invalid",
		"hook:check.json:project":           "detected",
		"plugin:fixture-plugin:marketplace": "enabled",
	} {
		if states[key] != expected {
			t.Errorf("%s: %s, want %s", key, states[key], expected)
		}
	}
	foundMissing := false
	for _, path := range report.Paths {
		if path.Path == filepath.Join(root, ".agents/skills") {
			foundMissing = path.State == "missing"
		}
	}
	if !foundMissing {
		t.Fatal("doctor hides empty default search locations")
	}
	config.BaseDirectory = t.TempDir()
	override, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer override.Close()
	if override.config.BaseDirectory != config.BaseDirectory {
		t.Fatal("explicit execution base directory lost to environment")
	}
}

func TestDoctorDistinguishesWorkerSkillExclusionFromNativeInventory(t *testing.T) {
	report := DiscoveryReport{DisabledSkills: []string{"taxion"}}
	if err := report.addNativeInventory("skill", []byte(`[{"name":"taxion","enabled":true},{"name":"product-guidance","enabled":true}]`)); err != nil {
		t.Fatal(err)
	}
	if len(report.Assets) != 2 || report.Assets[0].State != "disabled-for-role" ||
		report.Assets[1].State != "enabled" {
		t.Fatal("doctor hides role exclusion or disables unrelated product skills", report.Assets)
	}
}

func TestDoctorNativeInventoryErrorsAreNotEmptySuccess(t *testing.T) {
	var report DiscoveryReport
	for kind, input := range map[string]string{
		"skill": `null`, "plugin": `{}`, "instruction": `[{"label":"DO_NOT_PRINT"}]`, "mcp": `{"secret":"DO_NOT_PRINT"}`,
	} {
		if err := report.addNativeInventory(kind, []byte(input)); err == nil || strings.Contains(err.Error(), "DO_NOT_PRINT") {
			t.Fatal("unsupported native inventory silently accepted or exposed", kind, err)
		}

	}
	config := testConfig(t)
	adapter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	report, err = adapter.Inspect(t.Context())
	if err == nil || len(report.Problems) == 0 {
		t.Fatal("unsupported CLI produces success-shaped empty inventory")
	}
}

func TestPinnedDoctorDoesNotDiscoverAmbientAssets(t *testing.T) {
	config := testConfig(t)
	config.ToolPolicy = &tools.Policy{EvidenceDirectory: t.TempDir()}
	adapter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	report, err := adapter.Inspect(t.Context())
	if err != nil || report.Mode != "pinned-tools-policy" || len(report.Assets) != 0 || len(report.Paths) != 0 {
		t.Fatal("pinned execution and doctor discovery differ", report, err)
	}
}

func TestPinnedDoctorDoesNotClaimExcludedSkillsAreActive(t *testing.T) {
	config := toolsConfig(t)
	config.ToolPolicy.MCP = nil
	adapter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	report, err := adapter.Inspect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.DisabledSkills) != 1 || report.DisabledSkills[0] != "taxion" || len(report.Assets) != 2 {
		t.Fatal("pinned doctor omits role exclusions or approved assets", report)
	}
	for _, asset := range report.Assets {
		if asset.State != "pinned-content" || !strings.Contains(asset.Reason, "activation is not verified") ||
			!strings.Contains(asset.Reason, "disabledSkills still apply") {
			t.Fatal("pinned content mistaken for enabled skills", asset)
		}
	}
}

func TestDoctorFindsMalformedAndSymlinkedLocalSources(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	child := filepath.Join(root, "src")
	skillRoot := t.TempDir()
	for _, directory := range []string{filepath.Join(root, ".git"), child,
		filepath.Join(root, ".agents"), filepath.Join(skillRoot, "broken")} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(skillRoot, "broken/SKILL.md"), []byte("---\nname: [\n---\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(skillRoot, filepath.Join(root, ".agents/skills")); err != nil {
		t.Fatal(err)
	}
	config := testConfig(t)
	config.WorkingDirectory = child
	adapter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	report := DiscoveryReport{}
	if err := adapter.inspectLocalSources(&report); err != nil {
		t.Fatal(err)
	}
	for _, asset := range report.Assets {
		if asset.Kind == "skill" && asset.Name == "broken" && asset.State == "invalid" {
			return
		}
	}
	t.Fatal("subdirectory diagnostics omit malformed skills behind a standard-path symlink", report.Assets)
}
