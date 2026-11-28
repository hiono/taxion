package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInitInstallsSkillWithoutOverwriting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".agents", "skills", "taxion-local", "SKILL.md")
	var out, errOut bytes.Buffer
	run := func() int {
		out.Reset()
		errOut.Reset()
		return Run(context.Background(), []string{"init", "--dir", dir}, &out, &errOut, VersionInfo{})
	}
	if code := run(); code != 0 {
		t.Fatalf("init = %d: %s", code, errOut.String())
	}
	content, err := os.ReadFile(path)
	executable, execErr := os.Executable()
	pathJSON, jsonErr := json.Marshal(executable)
	binary, binaryErr := os.ReadFile(executable)
	hash := sha256.Sum256(binary)
	if err != nil || execErr != nil || jsonErr != nil || binaryErr != nil || !bytes.Contains(content, pathJSON) || !bytes.Contains(content, []byte(fmt.Sprintf("%x", hash))) || !bytes.Contains(content, agentSkill) {
		t.Fatalf("skill content: %q, err: %v", content, err)
	}
	if code := run(); code != 0 {
		t.Fatalf("repeat init = %d: %s", code, errOut.String())
	}
	if err := os.WriteFile(path, []byte("custom"), 0644); err != nil {
		t.Fatal(err)
	}
	if code := run(); code != 1 {
		t.Fatalf("modified skill init = %d, want 1", code)
	}
	content, err = os.ReadFile(path)
	if err != nil || string(content) != "custom" {
		t.Fatalf("modified skill overwritten: %q, err: %v", content, err)
	}
}

func TestReadyAndDoctor(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"ready"}, &out, &errOut, VersionInfo{Version: "0.1.1"})
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	quoted := "'" + strings.ReplaceAll(executable, "'", "'\\''") + "'"
	if code != 0 || !strings.Contains(out.String(), quoted+" run --request") || !strings.Contains(out.String(), "0.1.1") {
		t.Fatalf("ready = %d, output = %q, error = %q", code, out.String(), errOut.String())
	}
	t.Setenv("PATH", t.TempDir())
	out.Reset()
	errOut.Reset()
	code = Run(context.Background(), []string{"doctor"}, &out, &errOut, VersionInfo{})
	if code != 1 || !strings.Contains(errOut.String(), "copilot not found") ||
		!strings.Contains(errOut.String(), "git not found") || !strings.Contains(errOut.String(), "diff not found") {
		t.Fatalf("doctor = %d, output = %q, error = %q", code, out.String(), errOut.String())
	}
	if runtime.GOOS != "linux" {
		return
	}
	bin := t.TempDir()
	for _, name := range []string{"copilot", "git", "diff"} {
		if err := os.WriteFile(filepath.Join(bin, name), nil, 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	out.Reset()
	errOut.Reset()
	code = Run(context.Background(), []string{"doctor"}, &out, &errOut, VersionInfo{})
	if code != 0 || !strings.Contains(out.String(), "Authentication, provider access and successful execution are not checked") {
		t.Fatalf("doctor = %d, output = %q, error = %q", code, out.String(), errOut.String())
	}
}
