package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/config"
)

func setupFixture(t *testing.T, git bool) string {
	t.Helper()
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	previous := storePath
	t.Cleanup(func() { storePath = previous })
	if git {
		if output, err := exec.Command("git", "init", "-q").CombinedOutput(); err != nil {
			t.Fatalf("git init: %s: %v", output, err)
		}
		if _, err := setupGit(root, "remote", "add", "origin", "https://gitlab.example.test/group/subgroup/project.git"); err != nil {
			t.Fatal(err)
		}
	}
	storePath = filepath.Join(root, ".taxion", "ledger.json")
	return root
}

func setupArguments() []string {
	return []string{"--set", "repository.provider=gitlab", "--set", "repository.account=scm-user",
		"--set", "runtime.provider=copilot", "--set", "runtime.account=ai-user",
		"--set", "runtime.contractRef=operator-contract", "--set", "execution.approvalRef=operator-reference"}
}

func TestSetupGitLabColdStartAndRepeatWithoutModel(t *testing.T) {
	root := setupFixture(t, true)
	child := filepath.Join(root, "src", "nested")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(child)
	var output bytes.Buffer
	args := append(setupArguments(), "--yes")
	if err := setupCmd(args, strings.NewReader(""), &output, false, true); err != nil {
		t.Fatal(output.String(), err)
	}
	resolved, err := resolveConfiguration()
	if err != nil || resolved.Config.Repository.Host != "gitlab.example.test" ||
		resolved.Config.Repository.Path != "group/subgroup/project" ||
		resolved.Config.Repository.CredentialRef != "glab:active" ||
		resolved.Config.Runtime.Account != "ai-user" {
		t.Fatal("wrong resolved project", resolved, err)
	}
	path := filepath.Join(root, ".taxion", "config.yaml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("configuration is not private", info, err)
	}
	dbBefore, err := os.ReadFile(workspaceDBPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := setupCmd([]string{"--yes"}, strings.NewReader(""), &output, false, true); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	dbAfter, _ := os.ReadFile(workspaceDBPath())
	if !bytes.Equal(before, after) || !bytes.Equal(dbBefore, dbAfter) {
		t.Fatal("repeat setup rewrote configuration or initialized database")
	}
	if err := checkConfig([]string{"check", "--sources"}, &output); err != nil {
		t.Fatal(err)
	}
	if err := doctorCmd([]string{"--cli", doctorFixtureCLI(t), "--json", "--worktree", root}, &output); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(child, ".taxion")); !os.IsNotExist(err) {
		t.Fatal("setup created subdirectory state", err)
	}
}

func TestSetupInteractiveExplainsAndRetriesRequiredFields(t *testing.T) {
	for _, initialize := range []bool{true, false} {
		t.Run(map[bool]string{true: "init", false: "doctor-fix"}[initialize], func(t *testing.T) {
			root := setupFixture(t, true)
			if err := initCmd(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(workspaceDBPath())
			if err != nil {
				t.Fatal(err)
			}
			backup := filepath.Join(root, ".taxion", "config.yaml.bak")
			original := []byte("private backup must not be loaded or overwritten\n")
			if err := os.WriteFile(backup, original, 0o600); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			input := "\nunsupported\ngitlab\n\nscm-user\nai-user\nconfirmed-contract\napproval-record\nyes\n"
			if err := setupCmd([]string{"--set", "runtime.provider=copilot"}, strings.NewReader(input), &output, true, initialize); err != nil {
				t.Fatal(output.String(), err)
			}
			for _, detail := range []string{
				"Choose github, gitlab, or bitbucket", "repository.provider (required)",
				"repository.provider is required", "repository.provider requires github, gitlab, or bitbucket",
				"repository.account is required", "not necessarily your AI runtime account",
				"Setup does not verify eligibility", "This does not approve a Task or budget",
			} {
				if !strings.Contains(output.String(), detail) {
					t.Errorf("missing input guidance: %s", detail)
				}
			}
			selected, err := resolveConfiguration()
			if err != nil || selected.Config.Repository.Provider != "gitlab" ||
				selected.Config.Repository.CredentialRef != "glab:active" ||
				selected.Config.Runtime.ContractRef != "confirmed-contract" {
				t.Fatal(selected, err)
			}
			after, err := os.ReadFile(workspaceDBPath())
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("setup changed existing database", err)
			}
			after, err = os.ReadFile(backup)
			if err != nil || !bytes.Equal(original, after) {
				t.Fatal("setup changed backup configuration", err)
			}
		})
	}
}

func TestSetupBlankRequiredInputDoesNotAdvanceOrWrite(t *testing.T) {
	root := setupFixture(t, true)
	var output bytes.Buffer
	err := setupCmd(nil, strings.NewReader("\n\n"), &output, true, true)
	if err == nil || !strings.Contains(err.Error(), "repository.provider") ||
		!strings.Contains(err.Error(), "no files changed") {
		t.Fatal(output.String(), err)
	}
	if strings.Count(output.String(), "repository.provider (required):") != 3 ||
		strings.Contains(output.String(), "repository.account (required):") {
		t.Fatal("blank input advanced to another field", output.String())
	}
	for _, path := range []string{".taxion", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(root, path)); !os.IsNotExist(err) {
			t.Fatal("incomplete interactive setup wrote files", path, err)
		}
	}
}

func TestSetupDoctorFirstAndDirectInitUseCurrentObservation(t *testing.T) {
	var previous map[string]string
	for _, doctorFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "doctor-first", false: "direct-init"}[doctorFirst], func(t *testing.T) {
			root := setupFixture(t, true)
			bin := t.TempDir()
			marker := filepath.Join(root, "runtime-started")
			if err := os.WriteFile(filepath.Join(bin, "copilot"), []byte("#!/bin/sh\ntouch "+marker+"\nexit 1\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			var output bytes.Buffer
			if doctorFirst {
				err := doctorCmd([]string{"--json"}, &output)
				if err == nil || !strings.Contains(err.Error(), "no files changed") {
					t.Fatal("unconfigured doctor reported success", output.String(), err)
				}
				var report struct {
					Setup   setupObservation `json:"setup"`
					Problem string           `json:"problem"`
				}
				if err := json.Unmarshal(output.Bytes(), &report); err != nil {
					t.Fatal(output.String(), err)
				}
				if report.Setup.Root != root || report.Problem == "" ||
					report.Setup.Detected["repository.host"] != "gitlab.example.test" ||
					report.Setup.Selection.Config.Runtime.Provider != "" {
					t.Fatal("unconfigured diagnosis lost observations or selected a runtime", report)
				}
				for _, path := range []string{".taxion", ".gitignore", "runtime-started"} {
					if _, err := os.Stat(filepath.Join(root, path)); !os.IsNotExist(err) {
						t.Fatal("doctor changed files or started a backend", path, err)
					}
				}
				// A previous diagnosis is not a cache or an approval.
				if _, err := setupGit(root, "remote", "set-url", "origin", "https://gitlab.example.test/changed/project.git"); err != nil {
					t.Fatal(err)
				}
			} else if _, err := setupGit(root, "remote", "set-url", "origin", "https://gitlab.example.test/changed/project.git"); err != nil {
				t.Fatal(err)
			}
			output.Reset()
			args := append(setupArguments(), "--set", "project.id=fixture", "--yes")
			if err := setupCmd(args, strings.NewReader(""), &output, false, true); err != nil {
				t.Fatal(output.String(), err)
			}
			selected, err := resolveConfiguration()
			if err != nil || selected.Config.Repository.Path != "changed/project" {
				t.Fatal("init used stale doctor data", selected, err)
			}
			values := selected.Config.Values()
			if previous != nil {
				for name, value := range previous {
					if values[name] != value {
						t.Errorf("%s differs between direct and doctor-first init", name)
					}
				}
			}
			previous = values
			for _, text := range []string{"not saved", "no prior doctor run", "Optional/conditional", "not a complete configuration catalogue", "Unverified:"} {
				if !strings.Contains(output.String(), text) {
					t.Errorf("missing setup status: %s", text)
				}
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("setup ran a runtime executable", err)
			}
		})
	}
}

func TestSetupObservationDoesNotHideInvalidConfiguration(t *testing.T) {
	root := setupFixture(t, true)
	if err := os.MkdirAll(filepath.Join(root, ".taxion"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".taxion", "config.yaml")
	before := []byte("version: 1\nunknown: value\n")
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := doctorCmd([]string{"--json"}, &output); err == nil || output.Len() != 0 {
		t.Fatal("invalid configuration became a success-shaped observation", output.String(), err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("doctor altered invalid configuration", err)
	}
}

func TestSetupObservationListsAvailableChoicesWithoutSelectingOrStartingThem(t *testing.T) {
	for _, available := range [][]string{nil, {"copilot"}, {"opencode"}, {"copilot", "opencode"}} {
		t.Run(strings.Join(available, "+"), func(t *testing.T) {
			setupFixture(t, false)
			bin := t.TempDir()
			for _, name := range available {
				if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 99\n"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", bin)
			report, err := observeSetup("")
			if err != nil || strings.Join(report.RuntimeAvailable, ",") != strings.Join(available, ",") {
				t.Fatal("available choices differ from PATH", report.RuntimeAvailable, err)
			}
			if report.Selection.Config.Runtime.Provider != "" || report.Detected["runtime.provider"] != "" {
				t.Fatal("availability became a selected backend")
			}
		})
	}
}

func TestSetupObservationOutputFailuresDoNotWriteFiles(t *testing.T) {
	for _, action := range []string{"init", "doctor", "doctor-json"} {
		t.Run(action, func(t *testing.T) {
			root := setupFixture(t, true)
			var err error
			if action == "init" {
				err = setupCmd(append(setupArguments(), "--yes"), strings.NewReader(""), rejectingConfigWriter{}, false, true)
			} else {
				var args []string
				if action == "doctor-json" {
					args = []string{"--json"}
				}
				err = doctorCmd(args, rejectingConfigWriter{})
			}
			if err == nil || !strings.Contains(err.Error(), "output unavailable") {
				t.Fatal("output failure was suppressed", err)
			}
			for _, path := range []string{".taxion", ".gitignore"} {
				if _, err := os.Stat(filepath.Join(root, path)); !os.IsNotExist(err) {
					t.Fatal("failed observation wrote files", path, err)
				}
			}
		})
	}
}

func TestSetupPreviewDeclineMissingAndExistingDatabase(t *testing.T) {
	root := setupFixture(t, true)
	var output bytes.Buffer
	if err := setupCmd(nil, strings.NewReader(""), &output, false, true); err == nil ||
		!strings.Contains(output.String(), "runtime.contractRef") ||
		!strings.Contains(output.String(), "execution.approvalRef") {
		t.Fatal("missing references were not reported together", output.String(), err)
	}
	args := append(setupArguments(), "--dry-run")
	if err := setupCmd(args, strings.NewReader(""), &output, false, true); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{".gitignore", ".taxion"} {
		if _, err := os.Stat(filepath.Join(root, path)); !os.IsNotExist(err) {
			t.Fatal("preview created state", path, err)
		}
	}
	if err := setupCmd(setupArguments(), strings.NewReader("no\n"), &output, true, true); err == nil {
		t.Fatal("decline was ignored")
	}
	if _, err := os.Stat(filepath.Join(root, ".taxion")); !os.IsNotExist(err) {
		t.Fatal("decline created state", err)
	}
	if err := initCmd(); err != nil {
		t.Fatal(err)
	}
	dbBefore, _ := os.ReadFile(workspaceDBPath())
	if err := setupCmd(append(setupArguments(), "--yes"), strings.NewReader(""), &output, false, false); err != nil {
		t.Fatal(err)
	}
	dbAfter, _ := os.ReadFile(workspaceDBPath())
	if !bytes.Equal(dbBefore, dbAfter) {
		t.Fatal("doctor fix changed the initialized database")
	}
}

func TestSetupPreservesGlobalProjectEnvironmentAndExclude(t *testing.T) {
	root := setupFixture(t, true)
	global := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "taxion", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(global), 0o700); err != nil {
		t.Fatal(err)
	}
	globalData := []byte("version: 1\nruntime: {provider: copilot, account: ai-user, credentialRef: 'copilot:logged-in', contractRef: global-contract}\n")
	if err := os.WriteFile(global, globalData, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TAXION_EXECUTION_WORKER", "environment-worker")
	if err := os.WriteFile(filepath.Join(root, ".git", "info", "exclude"), []byte("/.taxion/\n/.env\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".taxion"), 0o700); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(root, ".taxion", "config.yaml")
	if err := os.WriteFile(project, []byte("# keep this comment\nversion: 1\nproject: {id: existing-project}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--yes", "--set", "repository.provider=gitlab", "--set", "repository.account=scm-user",
		"--set", "execution.approvalRef=operator-reference"}
	var output bytes.Buffer
	if err := setupCmd(args, strings.NewReader(""), &output, false, false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(project)
	if !bytes.Contains(data, []byte("# keep this comment")) || bytes.Contains(data, []byte("global-contract")) ||
		bytes.Contains(data, []byte("environment-worker")) {
		t.Fatal("setup lost comments or copied lower layers", string(data))
	}
	globalAfter, _ := os.ReadFile(global)
	if !bytes.Equal(globalData, globalAfter) {
		t.Fatal("setup overwrote Global settings")
	}
	if _, err := os.Stat(filepath.Join(root, ".gitignore")); !os.IsNotExist(err) {
		t.Fatal("setup ignored existing exclude rules", err)
	}
	if err := setupCmd([]string{"--yes", "--set", "project.id=replacement"}, strings.NewReader(""), &output, false, false); err == nil {
		t.Fatal("existing project selection was replaced")
	}
	if _, err := os.Stat(workspaceDBPath()); !os.IsNotExist(err) {
		t.Fatal("doctor fix created a database", err)
	}
}

func TestSetupRejectsTrackedSymlinkMalformedAndConcurrentTargets(t *testing.T) {
	for _, kind := range []string{"tracked", "symlink", "malformed", "ignore-symlink", "lock"} {
		t.Run(kind, func(t *testing.T) {
			root := setupFixture(t, true)
			if err := os.MkdirAll(filepath.Join(root, ".taxion"), 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, ".taxion", "config.yaml")
			switch kind {
			case "tracked":
				if err := os.WriteFile(path, []byte("version: 1\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if _, err := setupGit(root, "add", "-f", ".taxion/config.yaml"); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(filepath.Join(t.TempDir(), "not-created"), path); err != nil {
					t.Fatal(err)
				}
			case "malformed":
				if err := os.WriteFile(path, []byte("version: 1\nunknown: secret\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "ignore-symlink":
				if err := os.Symlink(filepath.Join(t.TempDir(), "not-created"), filepath.Join(root, ".gitignore")); err != nil {
					t.Fatal(err)
				}
			case "lock":
				if err := os.WriteFile(filepath.Join(root, ".taxion", "setup.lock"), []byte("owned"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var output bytes.Buffer
			if err := setupCmd(append(setupArguments(), "--yes"), strings.NewReader(""), &output, false, true); err == nil {
				t.Fatal("unsafe setup succeeded", kind)
			}
			if _, err := os.Stat(workspaceDBPath()); !os.IsNotExist(err) {
				t.Fatal("unsafe setup initialized a database", err)
			}
		})
	}
	path := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(path, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := replaceSetupFile(path, []byte("old"), []byte("new"), 0o600); err == nil {
		t.Fatal("concurrent target change was overwritten")
	}
}

func TestSetupRemoteParsingAndAmbiguity(t *testing.T) {
	credential := url.UserPassword("user", "synthetic").String()
	for _, remote := range []string{"https://gitlab.example.test/group/subgroup/repo.git",
		"git@gitlab.example.test:group/subgroup/repo.git", "ssh://git@gitlab.example.test/group/subgroup/repo.git"} {
		host, path, err := setupRemote(remote)
		if err != nil || host != "gitlab.example.test" || path != "group/subgroup/repo" {
			t.Fatal(remote, host, path, err)
		}
	}
	for _, remote := range []string{"/local/repo", "file:///repo", "https://" + credential + "@gitlab.example.test/a/b",
		"ssh://" + credential + "@gitlab.example.test/a/b", "https://gitlab.example.test/a/b?token=secret", "git@host:../bad"} {
		_, _, err := setupRemote(remote)
		if err == nil || strings.Contains(err.Error(), credential) || strings.Contains(err.Error(), "token=secret") {
			t.Fatal("unsafe remote accepted or leaked", err)
		}
	}
	root := setupFixture(t, true)
	if _, err := setupGit(root, "remote", "add", "other", "https://github.com/other/repo"); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := setupCmd(append(setupArguments(), "--yes"), strings.NewReader(""), &output, false, true); err == nil {
		t.Fatal("multiple remotes were guessed")
	}
	args := append(setupArguments(), "--yes", "--remote", "origin")
	if err := setupCmd(args, strings.NewReader(""), &output, false, true); err != nil {
		t.Fatal(err)
	}
}

func TestSetupStandaloneAndInteractiveReferences(t *testing.T) {
	root := setupFixture(t, false)
	args := []string{"--set", "repository.provider=github", "--set", "repository.host=github.com",
		"--set", "repository.path=owner/repo", "--set", "runtime.provider=copilot"}
	// Git and runtime accounts are deliberately separate choices.
	var output bytes.Buffer
	if err := setupCmd(args, strings.NewReader("scm-user\nai-user\ncontract\napproval\nyes\n"), &output, true, true); err != nil {
		t.Fatal(output.String(), err)
	}
	selected, err := resolveConfiguration()
	if err != nil || selected.Config.Project.ID != filepath.Base(root) {
		t.Fatal(selected, err)
	}
	if _, err := config.ResolvePartial(nil); err != nil {
		t.Fatal("empty setup should report missing fields, not fail version parsing", err)
	}
}

func TestDoctorMissingConfigurationIsActionableAndReadOnly(t *testing.T) {
	root := setupFixture(t, true)
	var output bytes.Buffer
	err := doctorCmd(nil, &output)
	if err == nil || !strings.Contains(err.Error(), "taxion doctor fix") {
		t.Fatal("missing setup has no repair guidance", err)
	}
	for _, path := range []string{".taxion", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(root, path)); !os.IsNotExist(err) {
			t.Fatal("doctor modified an unconfigured project", path, err)
		}
	}
}

func TestSetupLinkedWorktreeAndLegacySubdirectoryIsolation(t *testing.T) {
	root := setupFixture(t, true)
	if _, err := setupGit(root, "-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "--allow-empty", "-qm", "initial"); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(t.TempDir(), "linked")
	if _, err := setupGit(root, "worktree", "add", "-q", "--detach", linked); err != nil {
		t.Fatal(err)
	}
	t.Chdir(linked)
	// GIT_* overrides must not redirect setup or normal configuration resolution.
	t.Setenv("GIT_DIR", filepath.Join(root, ".git"))
	path, err := defaultWorkspaceStorePath()
	if err != nil || path != filepath.Join(linked, ".taxion", "ledger.json") {
		t.Fatal("linked root was redirected by ambient Git state", path, err)
	}
	storePath = path
	var output bytes.Buffer
	if err := setupCmd(append(setupArguments(), "--yes"), strings.NewReader(""), &output, false, true); err != nil {
		t.Fatal(output.String(), err)
	}
	if _, err := os.Stat(filepath.Join(root, ".taxion")); !os.IsNotExist(err) {
		t.Fatal("linked setup modified parent workspace", err)
	}
	child := filepath.Join(linked, "nested")
	if err := os.MkdirAll(filepath.Join(child, ".taxion"), 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(child, ".taxion", "config.yaml")
	data := []byte("version: 1\nproject: {id: legacy}\n")
	if err := os.WriteFile(legacy, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(child)
	if _, err := resolveConfiguration(); err == nil || !strings.Contains(err.Error(), "subdirectory") {
		t.Fatal("legacy subdirectory config was silently ignored", err)
	}
	after, _ := os.ReadFile(legacy)
	if !bytes.Equal(data, after) {
		t.Fatal("legacy config was moved or rewritten")
	}
}

func TestSetupFailureAfterSettingsSaveIsRecoverable(t *testing.T) {
	root := setupFixture(t, true)
	// A database path occupied by a directory must not be repaired by deletion.
	if err := os.MkdirAll(workspaceDBPath(), 0o700); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err := setupCmd(append(setupArguments(), "--yes"), strings.NewReader(""), &output, false, true)
	if err == nil || !strings.Contains(err.Error(), "configuration saved; workspace initialization failed") {
		t.Fatal("partial setup hid the saved configuration", err)
	}
	if _, err := resolveConfiguration(); err != nil {
		t.Fatal("saved configuration is unusable", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".taxion", "setup.lock")); !os.IsNotExist(err) {
		t.Fatal("failure leaked setup lock", err)
	}
	if err := os.Remove(workspaceDBPath()); err != nil {
		t.Fatal(err)
	}
	if err := setupCmd([]string{"--yes"}, strings.NewReader(""), &output, false, true); err != nil {
		t.Fatal("retry could not initialize after explicit operator repair", err)
	}
}

func TestSetupRejectsSecretsAndInvalidOptionsWithoutFiles(t *testing.T) {
	root := setupFixture(t, true)
	token := "github_pat_" + strings.Repeat("synthetic", 3)
	for _, extra := range [][]string{
		{"--set", "runtime.contractRef=" + token},
		{"--set", "unknown.field=" + token},
		{"--set", "unknown.field=value"}, {"--unknown"}, {"--set", "project.id=one", "--set", "project.id=two"},
		{"unexpected-position"}, {"--set", "project.id="},
	} {
		var output bytes.Buffer
		args := append([]string{"--yes"}, setupArguments()...)
		// Avoid a duplicate contractRef when testing a credential-like value.
		if strings.Contains(strings.Join(extra, " "), "github_pat_") {
			args = []string{"--yes", "--set", "repository.provider=gitlab", "--set", "repository.account=user",
				"--set", "runtime.provider=copilot", "--set", "runtime.account=user", "--set", "execution.approvalRef=reference"}
		}
		err := setupCmd(append(args, extra...), strings.NewReader(""), &output, false, true)
		if err == nil || strings.Contains(output.String(), "github_pat_") || strings.Contains(err.Error(), "github_pat_") {
			t.Fatal("invalid setup accepted or secret displayed", output.String(), err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".taxion")); !os.IsNotExist(err) {
		t.Fatal("invalid input wrote files", err)
	}
}

func TestSetupCLIColdStartAndOfflineCompletion(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "taxion")
	if output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %s: %v", output, err)
	}
	root := setupFixture(t, true)
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(),
		"XDG_CONFIG_HOME=" + os.Getenv("XDG_CONFIG_HOME")}
	diagnosis := exec.Command(binary, "doctor", "--json")
	diagnosis.Dir, diagnosis.Env = root, env
	report, err := diagnosis.Output()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 || !json.Valid(report) ||
		!bytes.Contains(report, []byte(`"setup":`)) || !bytes.Contains(report, []byte(`"problem":`)) {
		t.Fatal("unconfigured CLI doctor lost JSON or nonzero exit", string(report), err)
	}
	for _, path := range []string{".taxion", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(root, path)); !os.IsNotExist(err) {
			t.Fatal("CLI diagnosis changed files", path, err)
		}
	}
	run := func(args ...string) []byte {
		t.Helper()
		command := exec.Command(binary, args...)
		command.Dir, command.Env = root, env
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("CLI %v: %s: %v", args, output, err)
		}
		return output
	}
	run(append([]string{"init", "--yes"}, setupArguments()...)...)
	run("config", "check", "--sources")
	output := run("doctor", "--cli", doctorFixtureCLI(t), "--json")
	if !bytes.Contains(output, []byte(`"backend":"copilot"`)) {
		t.Fatal("CLI doctor did not report selected backend", string(output))
	}
	// No-op setup and repair need neither a terminal nor a second confirmation.
	run("init")
	run("doctor", "fix")
	command := exec.Command(binary, "completion", "bash")
	command.Dir = root
	command.Env = []string{"PATH=", "TAXION_RUNTIME_PROVIDER=unknown"}
	completion, err := command.CombinedOutput()
	if err != nil || string(completion) != bashCompletion {
		t.Fatal("completion requires Git, HOME or valid settings", string(completion), err)
	}
}

func TestSetupRejectsRemoteConflictAndIncompleteInteractiveInput(t *testing.T) {
	root := setupFixture(t, true)
	var output bytes.Buffer
	args := append(setupArguments(), "--yes", "--set", "repository.host=other.example.test")
	if err := setupCmd(args, strings.NewReader(""), &output, false, true); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatal("partial repository selection was mixed with a different remote", err)
	}
	if err := setupCmd(nil, strings.NewReader("gitlab\n"), &output, true, true); err == nil || !strings.Contains(err.Error(), "input ended") {
		t.Fatal("EOF was treated as approval", err)
	}
	token := "github_pat_" + strings.Repeat("synthetic", 3)
	if err := setupCmd(nil, strings.NewReader(token+"\n"), &output, true, true); err == nil ||
		strings.Contains(err.Error(), "github_pat_") || strings.Contains(output.String(), "github_pat_") {
		t.Fatal("interactive credential-like input was accepted or displayed", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".taxion")); !os.IsNotExist(err) {
		t.Fatal("conflict or EOF changed state", err)
	}
}
