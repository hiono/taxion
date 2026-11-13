package main

import (
	"bytes"
	"debug/buildinfo"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
)

func TestBuildVersionUsesModuleAndVCSIdentity(t *testing.T) {
	for _, test := range []struct {
		name string
		info debug.BuildInfo
		want string
	}{
		{"release", debug.BuildInfo{Main: debug.Module{Version: "v0.3.0"}}, "v0.3.0"},
		{"pseudo", debug.BuildInfo{Main: debug.Module{Version: "v0.3.1-0.20261017085122-dd352cfe1d8b"}}, "v0.3.1-0.20261017085122-dd352cfe1d8b"},
		{"local", debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc123"}}}, "(devel) revision=abc123"},
		{"dirty", debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc123"}, {Key: "vcs.modified", Value: "true"}}}, "(devel) revision=abc123 modified=true"},
		{"no-vcs", debug.BuildInfo{}, "(devel) (revision unavailable)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := buildVersion(&test.info); got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
	var output bytes.Buffer
	if err := printHelp(&output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "\nVersion: "+currentBuildVersion()+"\n") {
		t.Fatal("help omits current build identity", output.String())
	}
	if err := printHelp(rejectingConfigWriter{}); err == nil {
		t.Fatal("help suppressed output failure")
	}
}

func TestBuildIdentityCLIIsOfflineAndRejectsVersionArguments(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "taxion")
	if output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %s: %v", output, err)
	}
	root := t.TempDir()
	// A broken Git marker and invalid settings must not affect build identity.
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("invalid git marker\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		command := exec.Command(binary, args...)
		command.Dir = root
		command.Env = []string{"PATH=", "HOME=" + root, "XDG_CONFIG_HOME=" + root, "TAXION_RUNTIME_PROVIDER=invalid"}
		output, err := command.CombinedOutput()
		return string(output), err
	}
	version, err := run("--version")
	info, readErr := buildinfo.ReadFile(binary)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if err != nil || version != "taxion "+buildVersion(info)+"\n" || !strings.Contains(version, "revision=") {
		t.Fatal("local build identity is unavailable offline", version, err)
	}
	for _, option := range []string{"--help", "-h", "help"} {
		help, err := run(option)
		if err != nil || !strings.Contains(help, "\nVersion: "+strings.TrimSpace(strings.TrimPrefix(version, "taxion "))+"\n") {
			t.Fatal("help and version identity differ", option, help, err)
		}
	}
	if output, err := run("--version", "unexpected"); err == nil || !strings.Contains(output, "accepts no additional arguments") {
		t.Fatal("version accepted ignored arguments", output, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".taxion")); !os.IsNotExist(err) {
		t.Fatal("build identity created workspace files", err)
	}
}
