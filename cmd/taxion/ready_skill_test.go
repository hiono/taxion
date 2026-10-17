package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestReadySkillInstallCheckAndPreserveEdits(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PATH", "")
	t.Setenv("TAXION_RUNTIME_PROVIDER", "invalid")
	var output bytes.Buffer
	if err := readyCmd([]string{"--install-skill"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(output.String(), readyPrompt) {
		t.Fatal("installation omitted current ready contract")
	}
	path := filepath.Join(home, readySkillPath)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	document, err := parseReadySkill(original)
	if err != nil || document.Metadata.Version != readySkillVersion || readySkillCompatibility(document) != nil {
		t.Fatal("bundled compatibility", document, err)
	}
	for _, action := range []string{"--check-skill", "--install-skill", "--update-skill"} {
		output.Reset()
		if err := readyCmd([]string{action}, &output); err != nil {
			t.Fatal(action, err)
		}
		if action == "--check-skill" {
			var report struct {
				Compatible bool     `json:"compatible"`
				Accepted   []string `json:"acceptedSkillVersions"`
			}
			if err := json.Unmarshal(output.Bytes(), &report); err != nil ||
				!report.Compatible || len(report.Accepted) != 1 || report.Accepted[0] != readySkillVersion {
				t.Fatal("missing machine-readable compatibility", output.String(), err)
			}
		}
	}
	edited := append(append([]byte{}, original...), []byte("\nUser edit\n")...)
	if err := os.WriteFile(path, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"--check-skill", "--install-skill", "--update-skill"} {
		if err := readyCmd([]string{action}, &output); err == nil {
			t.Fatal("accepted overwritten/modified skill", action)
		}
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, edited) {
			t.Fatal("user edit was overwritten", err)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatal("temporary install files left behind", err)
	}
}

func writeReadySkillFixture(t *testing.T, path string, document readySkillDocument, body string) {
	t.Helper()
	var err error
	document.Metadata.ContentHash, err = readySkillHash(document, body)
	if err != nil {
		t.Fatal(err)
	}
	header, err := yaml.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\n"+string(header)+"---\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadySkillRejectsUnknownVersionsContractsAndStaleReady(t *testing.T) {
	for _, kind := range []string{"future-minor", "future-major", "old-version", "cli-contract", "ready-contract"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			var output bytes.Buffer
			if err := readyCmd([]string{"--install-skill"}, &output); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(home, readySkillPath)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			document, err := parseReadySkill(data)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "future-minor":
				document.Metadata.Version = "1.2.0"
			case "future-major":
				document.Metadata.Version = "2.0.0"
			case "old-version":
				document.Metadata.Version = "1.0.0"
			case "cli-contract":
				document.Metadata.CLIContract = "taxion.cli.v2"
			case "ready-contract":
				document.Metadata.ReadyHash = readyDigest([]byte("old ready"))
			}
			writeReadySkillFixture(t, path, document, readySkillBody)
			output.Reset()
			if err := readyCmd([]string{"--check-skill"}, &output); err == nil ||
				!strings.Contains(output.String(), `"compatible":false`) {
				t.Fatal("incompatible skill passed", output.String(), err)
			}
			if err := readyCmd([]string{"--install-skill"}, &output); err == nil {
				t.Fatal("implicit update succeeded")
			}
			if err := readyCmd([]string{"--update-skill"}, &output); err != nil {
				t.Fatal("explicit update of intact managed skill", err)
			}
			if err := readyCmd([]string{"--check-skill"}, &output); err != nil {
				t.Fatal("bundled update is incompatible", err)
			}
		})
	}
}

func TestReadySkillPreservesEditedFrontmatter(t *testing.T) {
	original, err := bundledReadySkill()
	if err != nil {
		t.Fatal(err)
	}
	for _, replacement := range []string{
		"# User note\nname: taxion",
		"name: 'taxion'",
		"name: taxion\nunknown-field: value",
		"name: taxion\nname: taxion",
	} {
		t.Run(replacement, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			path := filepath.Join(home, readySkillPath)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			edited := bytes.Replace(original, []byte("name: taxion"), []byte(replacement), 1)
			if err := os.WriteFile(path, edited, 0o644); err != nil {
				t.Fatal(err)
			}
			for _, action := range []string{"--check-skill", "--install-skill", "--update-skill"} {
				var output bytes.Buffer
				if err := readyCmd([]string{action}, &output); err == nil {
					t.Fatal("accepted edited frontmatter", action)
				}
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, edited) {
					t.Fatal("edited frontmatter was overwritten", err)
				}
			}
		})
	}
}

func TestReadySkillMissingForeignAndSymlinkPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	var output bytes.Buffer
	for _, args := range [][]string{
		{"--check-skill"}, {"--update-skill"},
		{"--install-skill", "--check-skill"}, {"--install-skill", "--update-skill"},
	} {
		if err := readyCmd(args, &output); err == nil {
			t.Fatal("invalid or missing skill was accepted", args)
		}
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatal("read-only check or invalid arguments wrote to home", err)
	}
	t.Run("symlink", func(t *testing.T) {
		outside := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(home, ".agents")); err != nil {
			if runtime.GOOS == "windows" {
				t.Skip("symlink creation is unavailable without developer mode or privilege")
			}
			t.Fatal(err)
		}
		if err := readyCmd([]string{"--install-skill"}, &output); err == nil {
			t.Fatal("followed symlink directory")
		}
		entries, err := os.ReadDir(outside)
		if err != nil || len(entries) != 0 {
			t.Fatal("wrote outside managed path", err)
		}
	})
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, readySkillPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("User-owned skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := readyCmd([]string{"--update-skill"}, &output); err == nil {
		t.Fatal("overwrote foreign skill")
	}
	if err := readyCmd(nil, rejectingConfigWriter{}); err == nil {
		t.Fatal("ignored output failure")
	}
}

func TestReadySkillReportsOutputFailureAfterInstallation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := readyCmd([]string{"--install-skill"}, rejectingConfigWriter{}); err == nil {
		t.Fatal("ignored installation output failure")
	}
	var output bytes.Buffer
	if err := readyCmd([]string{"--check-skill"}, &output); err != nil {
		t.Fatal("successful file installation was not preserved", err)
	}
	if err := readyCmd([]string{"--check-skill"}, rejectingConfigWriter{}); err == nil {
		t.Fatal("ignored compatibility output failure")
	}
}
