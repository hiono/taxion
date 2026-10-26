package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/memory"
	"github.com/mattn/go-isatty"
	"gopkg.in/yaml.v3"
)

type setupValues map[string]string

func (v setupValues) String() string { return "" }
func (v setupValues) Set(text string) error {
	name, value, ok := strings.Cut(text, "=")
	if !ok {
		return fmt.Errorf("--set requires FIELD=VALUE (identifiers only; never tokens)")
	}
	if _, duplicate := v[name]; duplicate {
		return fmt.Errorf("--set repeats a field")
	}
	if _, err := config.ValuesLayer("input", map[string]string{name: value}); err != nil {
		return err
	}
	v[name] = value
	return nil
}

func terminalInput(input *os.File) bool {
	return isatty.IsTerminal(input.Fd())
}

func setupContainsCredential(text string) bool {
	for _, rule := range memory.DefaultRules {
		if (rule.Name == "token" || rule.Name == "key") && rule.Re.MatchString(text) {
			return true
		}
	}
	return false
}

var setupFieldHelp = map[string]string{
	"project.id":               "Local project identifier (no whitespace).",
	"repository.provider":      "Choose github, gitlab, or bitbucket. For a self-managed GitLab host, choose gitlab.",
	"repository.host":          "Git server hostname, with an optional port; no URL scheme.",
	"repository.path":          "Repository namespace/path from the Git remote.",
	"repository.account":       "Account used for this Git repository; not necessarily your AI runtime account.",
	"repository.credentialRef": "Authentication reference, not a token: gh:active, glab:active, or env:VARIABLE_NAME.",
	"runtime.provider":         "Choose copilot or opencode; select the backend you intend to use.",
	"runtime.account":          "Account used by the selected AI runtime; do not enter a password or token.",
	"runtime.credentialRef":    "Authentication reference, not a token: copilot:logged-in or opencode-managed.",
	"runtime.contractRef":      "Reference to the runtime usage contract or entitlement you have confirmed. Setup does not verify eligibility.",
	"billing.contract":         "Declared contract: provider-limit, metered, or unknown. This does not change provider settings.",
	"billing.overage":          "Declared paid overage: denied, allowed, or unknown.",
	"billing.autoRecharge":     "Declared automatic recharge: disabled, enabled, or unknown.",
	"billing.paidFallback":     "Declared automatic paid account/provider fallback: disabled, enabled, or unknown.",
	"execution.requester":      "Identifier for the person requesting work.",
	"execution.worker":         "Identifier for the worker executing work.",
	"execution.approvalRef":    "Reference to your execution approval policy or record. This does not approve a Task or budget.",
	"runtime.acpCommand":       "Absolute path to the approved OpenCode executable.",
	"runtime.acpCommandHash":   "Optional executable pin in sha256:HEX format; omit to allow CLI updates at the configured path.",
	"runtime.acpConfig":        "Absolute path to the approved OpenCode server configuration.",
	"runtime.acpConfigHash":    "Approved server configuration hash in sha256:HEX format.",
}

func setupCmd(args []string, input io.Reader, output io.Writer, interactive, initialize bool) error {
	for _, arg := range args {
		if setupContainsCredential(arg) {
			return fmt.Errorf("setup rejects credential-like input; use identifiers or env:NAME references (values withheld)")
		}
	}
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	flags.SetOutput(output)
	yes := flags.Bool("yes", false, "apply the displayed setup without a confirmation prompt; all missing references must be supplied")
	dryRun := flags.Bool("dry-run", false, "display the setup without changing files or initializing the database")
	remote := flags.String("remote", "", "select a Git remote when several exist")
	values := setupValues{}
	flags.Var(values, "set", "supply a missing configuration FIELD=VALUE; repeat for each field (never secrets)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("setup accepts options only")
	}
	if err := validateWorkspaceStorePath(); err != nil {
		return err
	}
	observation, err := observeSetup(*remote)
	if err != nil {
		return err
	}
	root, layers, selected := observation.Root, observation.Layers, observation.Selection
	existing := selected.Config.Values()
	for name, value := range values {
		if previous := existing[name]; previous != "" && previous != value {
			return fmt.Errorf("%s already has a value; setup never replaces existing selections, edit the owning layer explicitly", name)
		}
	}
	reader := bufio.NewReader(input)
	terminal, isFile := input.(*os.File)
	selectable := interactive && isFile && terminalInput(terminal)
	choose := func(name string, choices []string, suggestion string) (string, error) {
		selected := 0
		for i, value := range choices {
			if value == suggestion {
				selected = i
			}
		}
		return selectSetup(terminal, reader, output, name, choices, selected)
	}
	ask := func(name, suggestion string) (string, error) {
		if !interactive || *yes || *dryRun {
			return "", fmt.Errorf("missing %s; rerun taxion init or taxion doctor fix with --set %s=VALUE (or use an interactive terminal)", name, name)
		}
		help, field := setupFieldHelp[name]
		if field {
			if _, err := fmt.Fprintln(output, help); err != nil {
				return "", err
			}
		}
		for {
			prompt := name + " (required): "
			if suggestion != "" {
				prompt = fmt.Sprintf("%s [%s]: ", name, suggestion)
			}
			if _, err := fmt.Fprint(output, prompt); err != nil {
				return "", err
			}
			text, err := reader.ReadString('\n')
			if err != nil {
				return "", fmt.Errorf("setup input ended before %s was supplied; no files changed: %w", name, err)
			}
			text = strings.TrimSpace(text)
			if text == "" {
				text = suggestion
			}
			if setupContainsCredential(text) {
				return "", fmt.Errorf("setup rejects credential-like input; use references, never tokens (values withheld)")
			}
			if text == "" {
				if _, err := fmt.Fprintf(output, "%s is required; enter a value or press Ctrl-C to cancel. No files changed.\n", name); err != nil {
					return "", err
				}
				continue
			}
			if field {
				if _, err := config.ValuesLayer("input", map[string]string{name: text}); err != nil {
					if _, writeErr := fmt.Fprintf(output, "%s: %v; try again. No files changed.\n", name, err); writeErr != nil {
						return "", writeErr
					}
					continue
				}
			}
			return text, nil
		}
	}
	if err := printSetupObservation(output, observation); err != nil {
		return err
	}
	if observation.SelectedRemote == "" && len(observation.Remotes) > 1 {
		if !interactive || *yes || *dryRun {
			return fmt.Errorf("multiple Git remotes; supply --remote NAME; no files changed")
		}
		var chosen string
		if selectable {
			chosen, err = choose("remote", observation.Remotes, "")
		} else {
			chosen, err = ask("remote (or pass --remote NAME)", "")
		}
		if err != nil {
			return err
		}
		observation, err = observeSetup(chosen)
		if err != nil {
			return err
		}
		if err := printSetupObservation(output, observation); err != nil {
			return err
		}
	}
	defaults, gitRoot := observation.Detected, observation.Git
	for _, name := range []string{"repository.host", "repository.path"} {
		inferred := defaults[name]
		for _, value := range []string{existing[name], values[name]} {
			if inferred != "" && value != "" && value != inferred &&
				(name != "repository.host" || !strings.EqualFold(value, inferred)) {
				return fmt.Errorf("%s conflicts with the selected Git remote; select --remote NAME or edit the owning configuration; no files changed", name)
			}
		}
	}
	// Unrecognised enterprise hosts require a provider choice, not a guess.
	var missing []string
	for _, name := range []string{"project.id", "repository.provider", "repository.host", "repository.path",
		"repository.account", "repository.credentialRef", "runtime.provider", "runtime.account",
		"runtime.credentialRef", "runtime.contractRef", "execution.requester", "execution.worker", "execution.approvalRef",
		"runtime.acpCommand", "runtime.acpConfig", "runtime.acpConfigHash"} {
		if existing[name] != "" || values[name] != "" {
			continue
		}
		current := func(key string) string {
			if values[key] != "" {
				return values[key]
			}
			return existing[key]
		}
		if strings.HasPrefix(name, "runtime.acp") && current("runtime.provider") != "opencode" {
			continue
		}
		suggestion := defaults[name]
		switch name {
		case "repository.credentialRef":
			suggestion = map[string]string{"github": "gh:active", "gitlab": "glab:active"}[current("repository.provider")]
		case "runtime.provider":
			if len(observation.RuntimeAvailable) == 1 {
				suggestion = observation.RuntimeAvailable[0]
			}
		case "runtime.account", "runtime.credentialRef":
			if current("runtime.provider") == "opencode" {
				suggestion = "opencode-managed"
			} else if name == "runtime.credentialRef" && current("runtime.provider") == "copilot" {
				suggestion = "copilot:logged-in"
			}
		}
		// Only factual Git paths and local labels are filled without a choice.
		if suggestion != "" && (defaults[name] != "" || name == "repository.credentialRef" || name == "runtime.credentialRef" ||
			current("runtime.provider") == "opencode" && name == "runtime.account") {
			values[name] = suggestion
		} else {
			if !interactive || *yes || *dryRun {
				missing = append(missing, "--set "+name+"=VALUE")
				continue
			}
			var value string
			var err error
			if selectable && (name == "repository.provider" || name == "runtime.provider") {
				if _, err = fmt.Fprintln(output, setupFieldHelp[name]); err != nil {
					return err
				}
				choices := []string{"github", "gitlab", "bitbucket"}
				if name == "runtime.provider" {
					choices = []string{"copilot", "opencode"}
				}
				value, err = choose(name, choices, suggestion)
			} else {
				value, err = ask(name, suggestion)
			}
			if err != nil {
				return err
			}
			values[name] = value
		}
	}
	if len(missing) != 0 {
		if _, err := fmt.Fprintf(output, "Project root: %s\nMissing references:\n  %s\n", root, strings.Join(missing, "\n  ")); err != nil {
			return err
		}
		return fmt.Errorf("setup incomplete; supply the listed --set FIELD=VALUE options or run interactively; no files changed")
	}
	layer, err := config.ValuesLayer("setup", values)
	if err != nil {
		return err
	}
	final, err := config.Resolve(append(layers, layer))
	if err != nil {
		return err
	}
	for _, value := range final.Config.Values() {
		if setupContainsCredential(value) {
			return fmt.Errorf("setup rejects credential-like values; use credential references, never tokens (values withheld)")
		}
	}
	configPath := filepath.Join(root, ".taxion", "config.yaml")
	original, err := optionalSetupFile(configPath)
	if err != nil {
		return err
	}
	// Environment values remain environment-only. Persist only additions.
	candidate, err := mergeSetupYAML(original, layer.Input)
	if err != nil {
		return err
	}
	if len(original) == 0 && len(values) == 0 {
		candidate = []byte("version: 1\n")
	}
	ignorePath := filepath.Join(root, ".gitignore")
	var ignoreBefore, ignoreAfter []byte
	if gitRoot {
		ignoreBefore, err = optionalSetupFile(ignorePath)
		if err != nil {
			return err
		}
		ignoreAfter = append([]byte(nil), ignoreBefore...)
		for _, path := range []string{".taxion/", ".env"} {
			_, err := setupGit(root, "check-ignore", "-q", "--", path)
			var exit *exec.ExitError
			if err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 1) {
				return err
			}
			if err != nil {
				if len(ignoreAfter) != 0 && ignoreAfter[len(ignoreAfter)-1] != '\n' {
					ignoreAfter = append(ignoreAfter, '\n')
				}
				ignoreAfter = append(ignoreAfter, []byte("/"+path+"\n")...)
			}
		}
	}
	if _, err := fmt.Fprintf(output, "Project root: %s\nConfiguration: %s\nWorkspace: %s\n", root, configPath, workspaceDBPath()); err != nil {
		return err
	}
	var names []string
	for name := range final.Sources {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, err := fmt.Fprintf(output, "%s: %s\n", name, final.Sources[name]); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(output, "Configuration additions (identifiers only):\n%s", layer.Input); err != nil {
		return err
	}
	if !bytes.Equal(ignoreBefore, ignoreAfter) {
		if _, err := fmt.Fprintf(output, "Append to %s:\n%s", ignorePath, ignoreAfter[len(ignoreBefore):]); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(output, "Only missing settings for the selected backend are saved; inherited fields and optional settings are not a complete configuration catalogue.\nNo login, model call, contract eligibility, data transmission or Task/budget approval is performed.\nAfter applying: taxion config check --sources, then taxion doctor. Native inventory is not execution approval."); err != nil {
		return err
	}
	if *dryRun {
		return nil
	}
	if bytes.Equal(original, candidate) && bytes.Equal(ignoreBefore, ignoreAfter) {
		if _, err := resolveConfiguration(); err != nil {
			return fmt.Errorf("configuration still requires manual repair; no files changed: %w", err)
		}
		if initialize {
			return initCmd()
		}
		_, err := fmt.Fprintln(output, "No repairs needed. Authentication, contract eligibility and execution approval remain separate checks.")
		return err
	}
	if !*yes {
		if !interactive {
			return fmt.Errorf("setup not applied; review with --dry-run, then supply --yes to apply")
		}
		if selectable {
			answer, err := choose("Apply displayed setup?", []string{"Cancel", "Apply"}, "Cancel")
			if err != nil {
				return err
			}
			if answer != "Apply" {
				return fmt.Errorf("setup declined; no files changed; existing files and database were preserved")
			}
		} else {
			for {
				answer, err := ask("Apply displayed setup? type yes", "no")
				if err != nil {
					return err
				}
				if strings.EqualFold(answer, "yes") {
					break
				}
				if strings.EqualFold(answer, "no") {
					return fmt.Errorf("setup declined; no files changed; existing files and database were preserved")
				}
				if _, err := fmt.Fprintln(output, "Confirmation not recognized. Type yes to apply, or no (Enter) to cancel. No files changed."); err != nil {
					return err
				}
			}
		}
	}
	directory := filepath.Dir(configPath)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	lockPath := filepath.Join(directory, "setup.lock")
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("setup lock unavailable; another setup may be running; inspect .taxion/setup.lock before manual recovery: %w", err)
	}
	defer os.Remove(lockPath)
	if err := lock.Close(); err != nil {
		return err
	}
	// Recheck trust boundaries after confirmation and before any replacement.
	userDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	if _, err := config.ReadSetupFiles(userDir, root); err != nil {
		return err
	}
	if !bytes.Equal(ignoreBefore, ignoreAfter) {
		if err := replaceSetupFile(ignorePath, ignoreBefore, ignoreAfter, 0o644); err != nil {
			return err
		}
	}
	if err := config.CheckGitPolicy(directory); err != nil {
		return fmt.Errorf("Git protection still fails; no configuration was written (review displayed ignore changes): %w", err)
	}
	if !bytes.Equal(original, candidate) {
		if err := replaceSetupFile(configPath, original, candidate, 0o600); err != nil {
			return fmt.Errorf("configuration was not written; Git protection may already have been saved, review the displayed changes: %w", err)
		}
	}
	if _, err := resolveConfiguration(); err != nil {
		return fmt.Errorf("setup files saved but final configuration check failed; database was not initialized: %w", err)
	}
	if initialize {
		if err := initCmd(); err != nil {
			return fmt.Errorf("configuration saved; workspace initialization failed: %w", err)
		}
	}
	_, err = fmt.Fprintln(output, "Setup complete. Next: taxion config check --sources && taxion doctor. Model execution still requires authentication and explicit Task/budget approval.")
	return err
}

func setupGit(root string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	for _, variable := range os.Environ() {
		if !strings.HasPrefix(variable, "GIT_") {
			command.Env = append(command.Env, variable)
		}
	}
	output, err := command.Output()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("setup Git inspection timed out")
	}
	return output, err
}

func setupRemote(raw string) (string, string, error) {
	var host, path string
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" ||
			(u.Scheme != "https" && u.Scheme != "ssh") || u.User != nil && u.Scheme != "ssh" {
			return "", "", fmt.Errorf("unsupported or credential-bearing Git remote; supply repository fields explicitly without copying credentials")
		}
		if u.User != nil {
			if _, password := u.User.Password(); password {
				return "", "", fmt.Errorf("credential-bearing Git remote rejected; values withheld")
			}
		}
		host, path = u.Host, strings.TrimPrefix(u.Path, "/")
	} else if at := strings.IndexByte(raw, '@'); at > 0 {
		host, path, _ = strings.Cut(raw[at+1:], ":")
	}
	path = strings.TrimSuffix(path, ".git")
	if host == "" || path == "" {
		return "", "", fmt.Errorf("remote is not an HTTPS, SSH or SCP repository; supply repository fields explicitly")
	}
	_, err := config.ValuesLayer("remote", map[string]string{"repository.host": host, "repository.path": path})
	if err != nil {
		return "", "", fmt.Errorf("Git remote does not identify a canonical repository; values withheld")
	}
	return host, path, nil
}

func optionalSetupFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("setup refuses a symlink or non-regular file; repair manually")
	}
	return os.ReadFile(path)
}

func mergeSetupYAML(original, additions []byte) ([]byte, error) {
	if len(original) == 0 {
		return additions, nil
	}
	var base, extra yaml.Node
	if err := yaml.Unmarshal(original, &base); err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(additions, &extra); err != nil {
		return nil, err
	}
	var merge func(*yaml.Node, *yaml.Node) error
	changed := false
	merge = func(target, source *yaml.Node) error {
		for i := 0; i < len(source.Content); i += 2 {
			key, value := source.Content[i], source.Content[i+1]
			var found *yaml.Node
			for j := 0; j < len(target.Content); j += 2 {
				if target.Content[j].Value == key.Value {
					found = target.Content[j+1]
					break
				}
			}
			if found == nil {
				target.Content = append(target.Content, key, value)
				changed = true
			} else if found.Kind == yaml.MappingNode && value.Kind == yaml.MappingNode {
				if err := merge(found, value); err != nil {
					return err
				}
			} else if found.Value != value.Value {
				return fmt.Errorf("setup cannot overwrite an existing Project field")
			}
		}
		return nil
	}
	if err := merge(base.Content[0], extra.Content[0]); err != nil {
		return nil, err
	}
	if !changed {
		return original, nil
	}
	return yaml.Marshal(&base)
}

func replaceSetupFile(path string, before, after []byte, mode os.FileMode) error {
	current, err := optionalSetupFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, before) {
		return fmt.Errorf("setup target changed during preparation; nothing overwritten")
	}
	if before != nil && mode != 0o600 {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		mode = info.Mode().Perm()
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".taxion-setup-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(mode); err != nil {
		file.Close()
		return err
	}
	_, writeErr := file.Write(after)
	err = errors.Join(writeErr, file.Sync(), file.Close())
	if err != nil {
		return err
	}
	if before == nil {
		// Link refuses an existing destination rather than overwriting a new file.
		return os.Link(file.Name(), path)
	}
	return os.Rename(file.Name(), path)
}
