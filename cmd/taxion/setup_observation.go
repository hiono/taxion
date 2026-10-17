package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hiono/taxion/internal/config"
)

type setupObservation struct {
	Root             string            `json:"projectRoot"`
	Configuration    string            `json:"projectConfiguration"`
	Workspace        string            `json:"workspace"`
	WorkspacePresent bool              `json:"workspacePresent"`
	WorkspaceProblem string            `json:"workspaceProblem,omitempty"`
	Sources          map[string]string `json:"sources"`
	Remotes          []string          `json:"remotes"`
	SelectedRemote   string            `json:"selectedRemote,omitempty"`
	Detected         map[string]string `json:"detected"`
	RuntimeAvailable []string          `json:"runtimeAvailable"`
	Missing          []string          `json:"missing"`
	Optional         []string          `json:"optional"`
	Unverified       []string          `json:"unverified"`
	Next             string            `json:"next"`
	Layers           []config.Layer    `json:"-"`
	Selection        config.Resolution `json:"-"`
	Git              bool              `json:"-"`
}

func observeSetup(remote string) (setupObservation, error) {
	report := setupObservation{
		Detected: map[string]string{}, Remotes: []string{}, RuntimeAvailable: []string{}, Missing: []string{},
		Optional:   []string{"execution.budgetGuidance (off/flow/account); runtime.acp* (required only for opencode)"},
		Unverified: []string{"authentication", "contract eligibility", "data transmission permission", "Task and budget approval", "native skill loading", "backend inventory and Dispatch inspection (requires complete configuration and doctor)"},
		Next:       "taxion init or taxion doctor fix; no prior doctor run is required",
	}
	root, err := projectConfigurationDirectory()
	if err != nil {
		return report, err
	}
	report.Root, report.Configuration, report.Workspace = root, filepath.Join(root, ".taxion", "config.yaml"), workspaceDBPath()
	if info, err := os.Lstat(report.Workspace); err == nil {
		if !info.Mode().IsRegular() {
			report.WorkspaceProblem = "workspace is not a regular database file; manual repair is required"
		}
		report.WorkspacePresent = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return report, err
	}
	userDir, err := os.UserConfigDir()
	if err != nil {
		return report, err
	}
	report.Layers, err = config.ReadSetupFiles(userDir, root)
	if err != nil {
		return report, err
	}
	environment, present, err := config.EnvironmentLayer(os.LookupEnv)
	if err != nil {
		return report, err
	}
	if present {
		report.Layers = append(report.Layers, environment)
	}
	report.Selection, err = config.ResolvePartial(report.Layers)
	if err != nil {
		return report, err
	}
	report.Sources = report.Selection.Sources
	existing := report.Selection.Config.Values()
	report.Detected["project.id"] = filepath.Base(root)
	if strings.ContainsAny(report.Detected["project.id"], " \t\r\n") {
		delete(report.Detected, "project.id")
	}
	report.Detected["execution.requester"], report.Detected["execution.worker"] = "human", "local"
	if _, err := os.Lstat(filepath.Join(root, ".git")); err == nil {
		report.Git = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return report, err
	}
	if report.Git && (existing["repository.host"] == "" || existing["repository.path"] == "" || remote != "") {
		raw, err := setupGit(root, "remote")
		if err != nil {
			return report, err
		}
		report.Remotes = strings.Fields(string(raw))
		for _, name := range report.Remotes {
			if setupContainsCredential(name) {
				return report, fmt.Errorf("credential-like Git remote name rejected; repair Git configuration manually (values withheld)")
			}
		}
		if remote == "" && len(report.Remotes) == 1 {
			remote = report.Remotes[0]
		}
		if remote != "" {
			found := false
			for _, name := range report.Remotes {
				found = found || name == remote
			}
			if !found {
				return report, fmt.Errorf("--remote must select an existing Git remote")
			}
			raw, err := setupGit(root, "remote", "get-url", "--", remote)
			if err != nil {
				return report, err
			}
			host, path, err := setupRemote(strings.TrimSpace(string(raw)))
			if err != nil {
				return report, err
			}
			report.SelectedRemote = remote
			report.Detected["repository.host"], report.Detected["repository.path"] = host, path
			switch host {
			case "github.com":
				report.Detected["repository.provider"] = "github"
			case "gitlab.com":
				report.Detected["repository.provider"] = "gitlab"
			case "bitbucket.org":
				report.Detected["repository.provider"] = "bitbucket"
			}
		}
	}
	for _, name := range []string{"copilot", "opencode"} {
		if _, err := exec.LookPath(name); err == nil {
			report.RuntimeAvailable = append(report.RuntimeAvailable, name)
		} else if !errors.Is(err, exec.ErrNotFound) {
			return report, fmt.Errorf("cannot safely resolve %s on PATH; check the executable search path (details withheld)", name)
		}
	}
	for name := range setupFieldHelp {
		if existing[name] == "" && (!strings.HasPrefix(name, "runtime.acp") || existing["runtime.provider"] == "opencode") {
			report.Missing = append(report.Missing, name)
		}
	}
	sort.Strings(report.Missing)
	for _, value := range report.Detected {
		if setupContainsCredential(value) {
			return report, fmt.Errorf("credential-like detected value rejected (values withheld)")
		}
	}
	return report, nil
}

func printSetupObservation(output io.Writer, report setupObservation) error {
	if _, err := fmt.Fprintf(output, "Current setup observation (not saved; refreshed on every init/fix/doctor):\nProject root: %s\nConfiguration: %s\nWorkspace: %s (present: %t; existing data will be preserved)\n",
		report.Root, report.Configuration, report.Workspace, report.WorkspacePresent); err != nil {
		return err
	}
	var names []string
	for name := range report.Sources {
		names = append(names, name)
	}
	sort.Strings(names)
	if _, err := fmt.Fprintln(output, "Configured field origins (values withheld):"); err != nil {
		return err
	}
	if len(names) == 0 {
		if _, err := fmt.Fprintln(output, "  none; no configuration fields have been selected"); err != nil {
			return err
		}
	}
	for _, name := range names {
		if _, err := fmt.Fprintf(output, "  %s: %s\n", name, report.Sources[name]); err != nil {
			return err
		}
	}
	names = names[:0]
	for name := range report.Detected {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, err := fmt.Fprintf(output, "Detected/local candidate: %s=%s (not saved or approved)\n", name, report.Detected[name]); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(output, "Git remotes: %v; selected: %s\n", report.Remotes, report.SelectedRemote); err != nil {
		return err
	}
	if provider := report.Selection.Config.Repository.Provider; provider != "" {
		if _, err := fmt.Fprintf(output, "Repository provider: %s (configured; no selection needed).\n", provider); err != nil {
			return err
		}
	} else if _, err := fmt.Fprintln(output, "Repository choices: github, gitlab, bitbucket; a private hostname does not identify its provider."); err != nil {
		return err
	}
	if provider := report.Selection.Config.Runtime.Provider; provider != "" {
		if _, err := fmt.Fprintf(output, "Runtime provider: %s (configured; no selection needed).\n", provider); err != nil {
			return err
		}
	} else if _, err := fmt.Fprintln(output, "Runtime choices: copilot, opencode."); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(output, "Runtime executables found: %v (not an authentication check).\nMissing configured fields (some have candidates above): %v\nOptional/conditional settings: %v\nUnverified: %s\nNext: %s\nAll configuration fields: docs/userguide/configuration.md; taxion config check --sources shows applied origins.\n",
		report.RuntimeAvailable, report.Missing, report.Optional,
		strings.Join(report.Unverified, ", "), report.Next); err != nil {
		return err
	}
	if report.WorkspaceProblem != "" {
		_, err := fmt.Fprintln(output, report.WorkspaceProblem)
		return err
	}
	return nil
}
