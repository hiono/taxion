package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Evidence struct {
	Session string `json:"session"`
	Tool    string `json:"tool"`
	Phase   string `json:"phase"`
	Input   any    `json:"input"`
}

func privateDirectory(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return fmt.Errorf("taxion: create private tool evidence directory: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	info, statErr := os.Stat(path)
	if err != nil || statErr != nil || resolved != path || !info.IsDir() || info.Mode().Perm() != 0700 {
		return fmt.Errorf("taxion: tool evidence requires a canonical directory with mode 0700")
	}
	return nil
}

// Save precedes presentation changes. The opaque receipt contains no tool text.
func (p Policy) Save(record Evidence) (string, error) {
	data, err := json.Marshal(record)
	if err != nil || len(data) > 16<<20 {
		return "", fmt.Errorf("taxion: tool evidence must be encodable within 16 MiB")
	}
	if err := privateDirectory(p.EvidenceDirectory); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(p.EvidenceDirectory, "tool-*.json")
	if err != nil {
		return "", fmt.Errorf("taxion: create private tool evidence: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return "", fmt.Errorf("taxion: save tool evidence: %w", err)
	}
	if err := file.Sync(); err != nil {
		return "", fmt.Errorf("taxion: sync tool evidence: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("taxion: close tool evidence: %w", err)
	}
	return fmt.Sprintf("%s sha256:%x", filepath.Base(file.Name()), sha256.Sum256(data)), nil
}

// Compress only changes explicitly successful plain text. Structured results
// and failures keep their exact status and contents in the model-facing view.
func (p Policy) Compress(ctx context.Context, result any) (any, error) {
	if p.RTK == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("taxion: encode tool result for compression")
	}
	var object map[string]any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if decoder.Decode(&object) != nil || object["resultType"] != "success" {
		return nil, nil
	}
	textKey := "textResultForLlm"
	if _, exists := object[textKey]; !exists {
		textKey = "textResultForLLM"
	}
	text, ok := object[textKey].(string)
	if !ok || len(text) < 4096 {
		return nil, nil
	}
	// JSON/XML source data must retain its syntax and citations.
	if strings.HasPrefix(strings.TrimSpace(text), "{") || strings.HasPrefix(strings.TrimSpace(text), "[") ||
		strings.HasPrefix(strings.TrimSpace(text), "<") {
		return nil, nil
	}
	if err := CheckAsset(*p.RTK, false); err != nil {
		return nil, err
	}
	if err := privateDirectory(p.EvidenceDirectory); err != nil {
		return nil, err
	}
	home, err := os.MkdirTemp(p.EvidenceDirectory, "rtk-")
	if err != nil {
		return nil, fmt.Errorf("taxion: create isolated RTK directory")
	}
	defer os.RemoveAll(home)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, p.RTK.Path, "pipe", "--filter", p.CompressionFilter)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "XDG_CONFIG_HOME=" + home, "XDG_DATA_HOME=" + home,
		"RTK_TELEMETRY_DISABLED=1", "RTK_NO_TELEMETRY=1"}
	command.Stdin = strings.NewReader(text)
	var stdout, stderr limitedBuffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err = command.Run()
	receipt, saveErr := p.Save(Evidence{Tool: "rtk.pipe", Phase: "compression", Input: struct {
		Stdout string `json:"stdout"`
		Stderr string `json:"stderr"`
		Exit   int    `json:"exit"`
	}{stdout.String(), stderr.String(), exitCode(command)}})
	if saveErr != nil {
		return nil, saveErr
	}
	if err != nil {
		return nil, fmt.Errorf("taxion: RTK compression failed; private evidence %s", receipt)
	}
	if stdout.Len() == 0 || stdout.Len() >= len(text) {
		return nil, nil
	}
	// Keep every error/warning line even if the presentation filter omitted it.
	for _, line := range strings.Split(text, "\n") {
		lower := strings.ToLower(line)
		if (strings.Contains(lower, "error") || strings.Contains(lower, "warn") ||
			strings.Contains(lower, "fail")) && !strings.Contains(stdout.String(), line) {
			fmt.Fprintln(&stdout.Buffer, line)
		}
	}
	object[textKey] = stdout.String()
	return object, nil
}

type limitedBuffer struct{ bytes.Buffer }

func (buffer *limitedBuffer) Write(data []byte) (int, error) {
	if buffer.Len()+len(data) > 16<<20 {
		return 0, fmt.Errorf("taxion: extension output exceeds 16 MiB")
	}
	return buffer.Buffer.Write(data)
}

func exitCode(command *exec.Cmd) int {
	if command.ProcessState == nil {
		return -1
	}
	return command.ProcessState.ExitCode()
}
