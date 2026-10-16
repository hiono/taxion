package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
)

func pinned(t *testing.T, path string) Asset {
	t.Helper()
	hash, err := Hash(path)
	if err != nil {
		t.Fatal(err)
	}
	return Asset{path, hash}
}

func TestCopyApprovedDirectory(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "copy")
	if err := os.Mkdir(source, 0750); err != nil {
		t.Fatal(err)
	}
	// Deleted entries can leave a larger directory allocation than a fresh copy.
	for i := range 100 {
		path := filepath.Join(source, fmt.Sprintf("%03d-%s", i, strings.Repeat("x", 120)))
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 100 {
		if err := os.Remove(filepath.Join(source, fmt.Sprintf("%03d-%s", i, strings.Repeat("x", 120)))); err != nil {
			t.Fatal(err)
		}
	}
	subdirectory := filepath.Join(source, "read-only")
	if err := os.Mkdir(subdirectory, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(subdirectory, "SKILL.md")
	if err := os.WriteFile(path, []byte("approved skill\n"), 0440); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(subdirectory, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.Chmod(subdirectory, 0755)
		os.Chmod(filepath.Join(destination, "read-only"), 0755)
	})
	asset := pinned(t, source)
	if err := CopyDirectory(asset, destination); err != nil {
		t.Fatal(err)
	}
	if err := CheckAsset(asset, true); err != nil {
		t.Fatal("copy changed the approved source", err)
	}
	copied := filepath.Join(destination, "read-only", "SKILL.md")
	if pinned(t, path).Hash != pinned(t, copied).Hash {
		t.Fatal("copied file bytes or mode differ")
	}
	for relative, expected := range map[string]os.FileMode{".": 0750, "read-only": 0555} {
		info, err := os.Stat(filepath.Join(destination, relative))
		if err != nil || info.Mode().Perm() != expected {
			t.Fatal("copied directory mode differs", relative, err)
		}
	}
	if err := CopyDirectory(asset, destination); err == nil {
		t.Fatal("copy overwrote an existing destination")
	}
	asset.Hash = "sha256:" + strings.Repeat("0", 64)
	rejected := filepath.Join(root, "rejected")
	if err := CopyDirectory(asset, rejected); err == nil {
		t.Fatal("copy accepted an unapproved source")
	}
	if _, err := os.Stat(rejected); !os.IsNotExist(err) {
		t.Fatal("rejected copy created a destination", err)
	}
}

func fixturePolicy(t *testing.T) Policy {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	return Policy{EvidenceDirectory: filepath.Join(t.TempDir(), "private"), MCP: []MCPTool{{
		Name: "mcp_fixture_read", Command: pinned(t, executable),
		Args: []string{"-test.run=TestMCPProcess", "--", "taxion-mcp-fixture"}, Tool: "read",
		Arguments: json.RawMessage(`{"value":"synthetic"}`),
	}}}
}

func TestMCPProcess(t *testing.T) {
	if !slices.Contains(os.Args, "taxion-mcp-fixture") {
		return
	}
	if os.Getenv("TAXION_TEST_PRIVATE") != "" {
		os.Exit(12)
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request struct {
			ID     *int   `json:"id"`
			Method string `json:"method"`
			Params struct {
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(13)
		}
		if request.ID == nil {
			continue
		}
		result := map[string]any{"protocolVersion": "2024-11-05"}
		if request.Method == "tools/call" {
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "synthetic source v1"}},
				"isError": request.Params.Arguments["value"] == "failure"}
			fmt.Fprintln(os.Stderr, "private diagnostic retained locally")
		}
		if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": *request.ID, "result": result}); err != nil {
			os.Exit(14)
		}
	}
	os.Exit(0)
}

func TestApprovedMCPExactArgumentsAndEvidence(t *testing.T) {
	policy := fixturePolicy(t)
	t.Setenv("TAXION_TEST_PRIVATE", "not-in-child")
	for _, args := range []any{map[string]any{"value": "changed"}, map[string]any{"value": "synthetic", "path": "/etc/passwd"}, nil} {
		if _, err := policy.CallMCP(t.Context(), policy.MCP[0].Name, args); err == nil {
			t.Fatal("unapproved request reached MCP")
		}
	}
	files, err := os.ReadDir(policy.EvidenceDirectory)
	if err != nil && !os.IsNotExist(err) || len(files) != 0 {
		t.Fatal("rejected arguments started a process", files, err)
	}
	body, err := policy.CallMCP(t.Context(), policy.MCP[0].Name, map[string]any{"value": "synthetic"})
	if err != nil || !strings.Contains(string(body), "synthetic source v1") {
		t.Fatal("approved real stdio MCP failed", string(body), err)
	}
	policy.MCP[0].Arguments = json.RawMessage(`{"value":"failure"}`)
	if _, err := policy.CallMCP(t.Context(), policy.MCP[0].Name, map[string]any{"value": "failure"}); err == nil {
		t.Fatal("MCP isError became success")
	}
	files, err = os.ReadDir(policy.EvidenceDirectory)
	if err != nil || len(files) != 2 {
		t.Fatal("MCP originals missing", len(files), err)
	}
	for _, file := range files {
		path := filepath.Join(policy.EvidenceDirectory, file.Name())
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("evidence permissions", err)
		}
		data, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(data), "private diagnostic retained locally") ||
			!strings.Contains(string(data), `"exit":0`) || !strings.Contains(string(data), `"stdout":`) {
			t.Fatal("MCP discarded raw response, status or stderr", string(data), err)
		}
	}
}

func TestAssetsApprovalsAndPluginSurface(t *testing.T) {
	root := t.TempDir()
	skills := filepath.Join(root, "skills")
	if err := os.Mkdir(skills, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(skills, "SKILL.md")
	if err := os.WriteFile(path, []byte("synthetic skill"), 0600); err != nil {
		t.Fatal(err)
	}
	policy := Policy{EvidenceDirectory: filepath.Join(root, "evidence"), Skills: []Asset{pinned(t, skills)}}
	if err := policy.Check(); err != nil {
		t.Fatal(err)
	}
	approval := Approval{Kind: ApprovalKind, TaskID: "task-1", Actor: "human", Roles: map[string]Policy{"REVIEWER": policy}}
	data, err := json.Marshal(approval)
	if err != nil {
		t.Fatal(err)
	}
	decision := core.TaskDecision{ID: "tools", Text: string(data), Source: "human:synthetic"}
	resolved, err := Resolve([]core.TaskDecision{decision}, "task-1", "REVIEWER")
	if err != nil || !Equal(resolved, &policy) {
		t.Fatal("role approval not preserved", err)
	}
	if other, err := Resolve([]core.TaskDecision{decision}, "task-1", "CODER"); err != nil || other != nil {
		t.Fatal("Reviewer approval widened Coder", err)
	}
	if _, err := Resolve([]core.TaskDecision{decision}, "different", "REVIEWER"); err == nil {
		t.Fatal("approval crossed Task identity")
	}
	if _, err := Resolve([]core.TaskDecision{decision, decision}, "task-1", "REVIEWER"); err == nil {
		t.Fatal("ambiguous approvals accepted")
	}
	if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if policy.Check() == nil {
		t.Fatal("changed skill remained authorized")
	}
	if err := os.Symlink(path, filepath.Join(skills, "linked.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := Hash(skills); err == nil {
		t.Fatal("asset tree followed a symlink")
	}
	plugin := t.TempDir()
	if err := os.WriteFile(filepath.Join(plugin, "plugin.json"), []byte(`{"name":"synthetic-skills","version":"1.0.0"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(plugin, "skills"), 0700); err != nil {
		t.Fatal(err)
	}
	policy.Skills = nil
	policy.Plugins = []Asset{pinned(t, plugin)}
	if err := policy.Check(); err != nil {
		t.Fatal("skill-only plugin unavailable", err)
	}
	if err := os.WriteFile(filepath.Join(plugin, "agents.md"), []byte("expand role"), 0600); err != nil {
		t.Fatal(err)
	}
	policy.Plugins[0] = pinned(t, plugin)
	if policy.Check() == nil {
		t.Fatal("plugin expanded the role")
	}
}

func TestRTKRealCompressionPreservesOriginalErrorsAndStatus(t *testing.T) {
	path, err := exec.LookPath("rtk")
	if err != nil {
		t.Skip("RTK opt-in integration requires the locally installed binary")
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	asset := pinned(t, path)
	policy := Policy{EvidenceDirectory: filepath.Join(t.TempDir(), "private"), RTK: &asset, CompressionFilter: "cargo-test"}
	var body strings.Builder
	for i := range 500 {
		fmt.Fprintf(&body, "test case_%d ... ok\n", i)
	}
	body.WriteString("WARNING: synthetic evidence remains important\n")
	body.WriteString("test result: ok. 500 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.01s\n")
	result := map[string]any{"resultType": "success", "textResultForLlm": body.String(), "exitCode": 0,
		"skipLargeOutputProcessing": true, "toolTelemetry": map[string]any{"synthetic": true, "sequence": int64(9007199254740993)}}
	receipt, err := policy.Save(Evidence{Tool: "test", Phase: "result", Input: result})
	if err != nil || !strings.Contains(receipt, "sha256:") {
		t.Fatal("original not durably recorded", receipt, err)
	}
	changed, err := policy.Compress(t.Context(), result)
	if err != nil {
		t.Fatal(err)
	}
	compressed, ok := changed.(map[string]any)
	if !ok || compressed["exitCode"] != json.Number("0") ||
		compressed["skipLargeOutputProcessing"] != true ||
		compressed["toolTelemetry"].(map[string]any)["synthetic"] != true ||
		compressed["toolTelemetry"].(map[string]any)["sequence"] != json.Number("9007199254740993") ||
		!strings.Contains(compressed["textResultForLlm"].(string), "WARNING: synthetic evidence") ||
		len(compressed["textResultForLlm"].(string)) >= len(body.String())/2 {
		t.Fatal("RTK missed compression threshold or lost status/errors", changed)
	}
	legacy := map[string]any{"resultType": "success", "textResultForLLM": body.String(), "exitCode": 0}
	if changed, err := policy.Compress(t.Context(), legacy); err != nil || changed == nil ||
		len(changed.(map[string]any)["textResultForLLM"].(string)) >= len(body.String())/2 {
		t.Fatal("SDK custom tool result no longer compressed", changed, err)
	}
	failure := map[string]any{"resultType": "failure", "textResultForLLM": body.String(), "exitCode": 1}
	if changed, err := policy.Compress(t.Context(), failure); err != nil || changed != nil {
		t.Fatal("failure compressed", changed, err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := policy.Compress(cancelled, result); err == nil {
		t.Fatal("failed compression appeared successful")
	}
}

func TestPrivateToolPublicationFollowsResultProvenance(t *testing.T) {
	approval := Approval{Kind: ApprovalKind, TaskID: "source", Actor: "human", Roles: map[string]Policy{
		"CODER": {EvidenceDirectory: t.TempDir()},
	}}
	data, err := json.Marshal(approval)
	if err != nil {
		t.Fatal(err)
	}
	graph := core.TaskGraph{
		Tasks: []core.GraphTask{{ID: "derived", Decisions: []core.TaskDecision{{ID: "result:private"}}}},
		Dispatches: []core.GraphDispatch{{ID: "private", TaskID: "source",
			Instruction: core.DispatchInstruction{Decisions: []core.TaskDecision{{ID: "tools", Text: string(data), Source: "human"}}}}},
	}
	if CheckPublication(graph, "derived") == nil {
		t.Fatal("private MCP results gained publication through a new Task")
	}
}
