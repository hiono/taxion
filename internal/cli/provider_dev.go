package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/provider"
)

func loginACPProvider(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("taxion dev provider login", flag.ContinueOnError)
	flags.SetOutput(stderr)
	command := flags.String("command", "copilot", "Copilot CLI executable")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return 2
	}
	commandPath, err := exec.LookPath(*command)
	if err != nil {
		fmt.Fprintln(stderr, "Copilot CLI was not found in PATH")
		return 1
	}
	profile, err := copilotProfileDirectory()
	if err != nil {
		return report(stderr, err)
	}
	login := exec.CommandContext(ctx, commandPath, "login")
	login.Stdin = os.Stdin
	login.Stdout = stdout
	login.Stderr = stderr
	login.Env = copilotProfileEnvironment(profile)
	if err := login.Run(); err != nil {
		fmt.Fprintln(stderr, "Copilot CLI login failed")
		return 1
	}
	fmt.Fprintf(stdout, "Copilot CLI profile: %s\n", profile)
	return 0
}

func runACPProvider(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("taxion dev provider run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	prompt := flags.String("prompt", "", "Natural-language prompt")
	command := flags.String("command", "copilot", "Copilot CLI executable")
	workingDirectory := flags.String("working-directory", ".", "Provider working directory")
	timeout := flags.Duration("timeout", 10*time.Minute, "Provider request timeout")
	maxTokens := flags.Uint64("max-tokens", 8192, "Maximum output tokens")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*prompt) == "" || *timeout <= 0 || *maxTokens == 0 {
		fmt.Fprintln(stderr, "usage: taxion dev provider run --prompt \"... \" [--timeout 10m] [--max-tokens 8192]")
		return 2
	}
	commandPath, err := exec.LookPath(*command)
	if err != nil {
		fmt.Fprintln(stderr, "Copilot CLI was not found in PATH")
		return 1
	}
	profile, err := copilotProfileDirectory()
	if err != nil {
		return report(stderr, err)
	}
	workingDir, err := filepath.Abs(*workingDirectory)
	if err != nil {
		return report(stderr, err)
	}
	info, err := os.Stat(workingDir)
	if err != nil || !info.IsDir() {
		fmt.Fprintln(stderr, "Provider working directory is unavailable")
		return 1
	}

	requestHash := sha256Hex(*prompt)
	configurationHash := sha256Hex(strings.Join([]string{commandPath, "--acp --stdio", workingDir, profile}, "\n"))
	identity := provider.Identity{
		ProviderID: "copilot-cli", AccountID: "local-profile", SDKVersion: "acp-v1",
		ConfigurationSHA256: configurationHash,
	}
	adapter, err := provider.NewACPAdapter(provider.ACPConfig{
		Command: commandPath, Args: []string{"--acp", "--stdio"},
		WorkingDirectory: workingDir, HomeDirectory: profile, Identity: identity,
	})
	if err != nil {
		return report(stderr, err)
	}
	jobID, err := domain.NewID("job")
	if err != nil {
		_ = adapter.Close()
		return report(stderr, err)
	}
	operationID, err := domain.NewID("op")
	if err != nil {
		_ = adapter.Close()
		return report(stderr, err)
	}
	roleExecutionID, err := domain.NewID("exec")
	if err != nil {
		_ = adapter.Close()
		return report(stderr, err)
	}
	request := provider.StartRequest{
		OperationID: operationID, JobID: jobID, RoleExecutionID: roleExecutionID,
		RequestSHA256: requestHash, PromptSHA256: requestHash, Prompt: *prompt,
		MaxTurns: 1, MaxTokens: *maxTokens, Deadline: time.Now().Add(*timeout),
	}
	if err := request.Validate(); err != nil {
		_ = adapter.Close()
		return report(stderr, err)
	}
	start, runErr := adapter.Start(ctx, request, nil)
	if runErr != nil {
		_ = adapter.Close()
		return report(stderr, runErr)
	}
	result, resultErr := adapter.Result(ctx, provider.ResultRequest{
		OperationID: start.OperationID, SessionID: start.SessionID,
	})
	closeErr := adapter.Close()
	if resultErr != nil {
		return report(stderr, resultErr)
	}
	if closeErr != nil {
		return report(stderr, errors.New("Copilot CLI process did not close cleanly"))
	}
	fmt.Fprintf(stdout, "Provider: %s\nSession: %s\n\n%s\n", identity.ProviderID, start.SessionID, result.Content)
	return 0
}

func copilotProfileDirectory() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	profile := filepath.Join(configDir, "taxion", "providers", "copilot-cli")
	if err := os.MkdirAll(profile, 0700); err != nil {
		return "", errors.New("create private Copilot CLI profile failed")
	}
	info, err := os.Lstat(profile)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("Copilot CLI profile must be a real directory")
	}
	if err := os.Chmod(profile, 0700); err != nil {
		return "", errors.New("restrict Copilot CLI profile permissions failed")
	}
	return profile, nil
}

func copilotProfileEnvironment(profile string) []string {
	blocked := map[string]struct{}{
		"HOME": {}, "XDG_CONFIG_HOME": {}, "XDG_CACHE_HOME": {}, "XDG_DATA_HOME": {},
		"COPILOT_GITHUB_TOKEN": {}, "GH_TOKEN": {}, "GITHUB_TOKEN": {},
	}
	environment := make([]string, 0, len(os.Environ())+4)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if _, skip := blocked[name]; !skip {
			environment = append(environment, entry)
		}
	}
	environment = append(environment,
		"HOME="+profile,
		"XDG_CONFIG_HOME="+filepath.Join(profile, ".config"),
		"XDG_CACHE_HOME="+filepath.Join(profile, ".cache"),
		"XDG_DATA_HOME="+filepath.Join(profile, ".local", "share"),
	)
	return environment
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
