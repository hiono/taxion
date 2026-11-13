package copilot

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestBuiltinMCPIsolationDoesNotEditUserSettings(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake executable")
	}
	cfg := testConfig(t)
	cfg.BaseDirectory = t.TempDir()
	settings := filepath.Join(cfg.BaseDirectory, "settings.json")
	before := []byte(`{"mcpServers":{"github-mcp-server":{"enabled":true}}}`)
	if err := os.WriteFile(settings, before, 0600); err != nil {
		t.Fatal(err)
	}
	argsPath := filepath.Join(t.TempDir(), "arguments")
	t.Setenv("TAXION_TEST_ARGS_PATH", argsPath)
	cfg.CLIPath = filepath.Join(t.TempDir(), "copilot")
	if err := os.WriteFile(cfg.CLIPath, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$TAXION_TEST_ARGS_PATH\"\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"CODER", "REVIEWER", "PLANNER", "ORCHESTRATOR"} {
		t.Run(role, func(t *testing.T) {
			cfg.Role = role
			adapter, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer adapter.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			if err := adapter.CheckAuthentication(ctx, "fixture"); err == nil {
				t.Fatal("fake runtime unexpectedly authenticated")
			}
			body, err := os.ReadFile(argsPath)
			if err != nil {
				t.Fatal(err)
			}
			args := strings.Fields(string(body))
			if !slices.Contains(args, "--disable-builtin-mcps") || slices.Contains(args, "--offline") {
				t.Fatal("builtin MCP isolation missing or model communication disabled", args)
			}
			after, err := os.ReadFile(settings)
			if err != nil || string(after) != string(before) || len(adapter.sessions) != 0 {
				t.Fatal("process isolation changed user settings or created a session", err)
			}
		})
	}
}
