package openacp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/hiono/taxion/internal/core"
)

func TestContextSnapshotsOnlyGrantExactReadOnlySourcePaths(t *testing.T) {
	document, err := core.CaptureContextDocument(core.ContextDocument{
		ID: "design", Kind: "design", Source: "human:design", Version: "2",
		Text: strings.Repeat("原文🙂\"\\\u0000", 2000) + "\r\nLast original condition.",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"CODER", "REVIEWER"} {
		t.Run(role, func(t *testing.T) {
			config := testConfig(t, role)
			config.ContextDocuments = []core.ContextDocument{document}
			adapter, err := New(config)
			if err != nil {
				t.Fatal(err)
			}
			defer adapter.Close()
			config.ContextDocuments[0].Text = "Caller substitution."
			adapter.root = t.TempDir()
			permission := map[string]any{"*": "deny", "bash": "deny", "read": "ask"}
			prompt, err := adapter.prepareContext(permission)
			if err != nil {
				t.Fatal(err)
			}
			path := "/config/taxion-context/0000.txt"
			read := permission["read"].(map[string]string)
			if !reflect.DeepEqual(read, map[string]string{"*": "deny", "config/taxion-context/0000.txt": "allow"}) ||
				permission["*"] != "deny" || permission["bash"] != "deny" ||
				!strings.Contains(prompt, document.Hash) || !strings.Contains(prompt, path) ||
				strings.Contains(prompt, document.Text) {
				t.Fatal("native source index broadened product or config access", prompt, permission)
			}
			data, err := os.ReadFile(filepath.Join(adapter.root, "config/taxion-context/0000.txt"))
			reconstructed, readErr := readNativeContextChunks(data)
			if err != nil || readErr != nil || reconstructed != document.Text {
				t.Fatal("native reader's source is incomplete or replaced", err)
			}
			info, err := os.Stat(filepath.Join(adapter.root, "config/taxion-context/0000.txt"))
			if err != nil || info.Mode().Perm() != 0400 {
				t.Fatal("source snapshot is writable", err)
			}
		})
	}
}

func readNativeContextChunks(data []byte) (string, error) {
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	var original strings.Builder
	offset := 0
	for index, line := range lines[1:] {
		// OpenCode read applies a 2000 UTF-16-character per-line ceiling and a
		// byte ceiling per response. Eight-line pages stay below both.
		if len(utf16.Encode([]rune(line))) > 2000 || len(line)*8 > 50*1024 {
			return "", fmt.Errorf("native read would truncate source chunk")
		}
		var page struct {
			Offset     int    `json:"offset"`
			NextOffset int    `json:"nextOffset"`
			EOF        bool   `json:"eof"`
			Text       string `json:"text"`
		}
		if err := json.Unmarshal([]byte(line), &page); err != nil {
			return "", err
		}
		if page.Offset != offset || page.NextOffset != offset+len([]rune(page.Text)) ||
			page.EOF != (index == len(lines)-2) {
			return "", fmt.Errorf("native source pagination is incomplete")
		}
		original.WriteString(page.Text)
		offset = page.NextOffset
	}
	return original.String(), nil
}

func TestLegacyOpenACPContextSurfaceAndHashUnchanged(t *testing.T) {
	adapter := &Adapter{config: Config{Role: "REVIEWER", SystemPrompt: "contract", WorkingDirectory: "/workspace"}}
	// Captured from the pre-context-access whole-Config encoding.
	legacy := `{"adapter":"openacp-isolated-v14","Role":"REVIEWER","SystemPrompt":"contract","WorkingDirectory":"/workspace","CLIPath":"","CLIHash":"","ServerConfig":"","ServerConfigHash":"","Configuration":null,"FileAccess":null,"TrackedFiles":null,"ResponseSchema":null,"BudgetGuidance":null,"ToolPolicy":null,"AvailableTools":null}`
	if adapter.ConfigHash() != hashBytes([]byte(legacy)) {
		t.Fatal("legacy ACP configuration hash changed")
	}
	permission := map[string]any{"read": "ask"}
	before := map[string]any{"read": "ask"}
	prompt, err := adapter.prepareContext(permission)
	if err != nil || prompt != "" || !reflect.DeepEqual(permission, before) {
		t.Fatal("legacy ACP prompt or native permission surface changed", err)
	}
}
