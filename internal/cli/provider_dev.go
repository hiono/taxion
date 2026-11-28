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

	"github.com/hiono/taxion/internal/controller"
	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/provider"
	"github.com/hiono/taxion/internal/store"
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
	commandPath, err = filepath.Abs(commandPath)
	if err != nil {
		return report(stderr, err)
	}
	profile, err := copilotProfileDirectory()
	if err != nil {
		return report(stderr, err)
	}
	login := exec.CommandContext(ctx, commandPath, "login", "--device-code")
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
	backend := flags.String("provider", "copilot", "Provider: copilot or opencode")
	command := flags.String("command", "", "Provider CLI executable")
	workingDirectory := flags.String("working-directory", ".", "Provider working directory")
	timeout := flags.Duration("timeout", 10*time.Minute, "Provider request timeout")
	localJobID := flags.String("job", "", "Local untrusted Job ID")
	storePath := flags.String("store", "", "Override the local Job database path")
	consult := flags.Bool("consult", false, "Ask another agent once before answering the local Job")
	review := flags.Bool("review", false, "Run a local planner, coder, and reviewer conversation")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*prompt) == "" || *timeout <= 0 ||
		(*backend != "copilot" && *backend != "opencode") ||
		((*consult || *review) && *localJobID == "") || (*consult && *review) {
		fmt.Fprintln(stderr, "usage: taxion dev provider run --prompt \"...\" [--provider copilot|opencode] [--job <local-job-id> [--consult|--review]] [--timeout 10m]")
		return 2
	}
	cliName, providerID := "Copilot CLI", "copilot-cli"
	cliArgs := []string{"--acp", "--stdio"}
	executable := *command
	if *backend == "opencode" {
		cliName, providerID = "OpenCode CLI", "opencode"
		cliArgs = []string{"acp"}
	}
	if executable == "" {
		executable = *backend
	}
	commandPath, err := exec.LookPath(executable)
	if err != nil {
		fmt.Fprintf(stderr, "%s was not found in PATH\n", cliName)
		return 1
	}
	commandPath, err = filepath.Abs(commandPath)
	if err != nil {
		return report(stderr, err)
	}
	var profile string
	if *backend == "opencode" {
		profile, err = os.MkdirTemp("", "taxion-opencode-")
		if err != nil {
			return report(stderr, err)
		}
		defer os.RemoveAll(profile)
		configDir := filepath.Join(profile, ".config", "opencode")
		if err := os.MkdirAll(configDir, 0700); err != nil {
			return report(stderr, err)
		}
		if err := os.WriteFile(filepath.Join(configDir, "opencode.json"),
			[]byte(`{"model":"opencode/big-pickle"}`), 0600); err != nil {
			return report(stderr, err)
		}
	} else {
		profile, err = copilotProfileDirectory()
		if err != nil {
			return report(stderr, err)
		}
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

	promptText := *prompt
	var communication *controller.CommunicationController
	var question controller.CommunicationOutcome
	if *localJobID != "" {
		dbPath, err := resolveStorePath(*storePath)
		if err != nil {
			return report(stderr, err)
		}
		database, err := store.Open(ctx, dbPath)
		if err != nil {
			return report(stderr, err)
		}
		defer database.Close()
		job, err := database.GetJob(ctx, *localJobID)
		if err != nil {
			return report(stderr, err)
		}
		if job.IntakeProvenance != nil || job.State != domain.JobDraft {
			return report(stderr, errors.New("dev provider conversation requires an untrusted local DRAFT Job"))
		}
		communication, err = controller.NewCommunicationController(database)
		if err != nil {
			return report(stderr, err)
		}
		commandID, err := domain.NewID("cmd")
		if err != nil {
			return report(stderr, err)
		}
		question, err = communication.SendFromController(ctx, controller.SystemMessageRequest{
			JobID: job.ID, CommandID: commandID,
			Recipient: domain.ActorRef{Kind: domain.ActorOrchestrator, ID: domain.OrchestratorActorID},
			Body:      *prompt,
		})
		if err != nil {
			return report(stderr, err)
		}
		job, err = database.GetJob(ctx, job.ID)
		if err != nil {
			return report(stderr, err)
		}
		promptText, err = localJobPrompt(job, question.Message.ID)
		if err != nil {
			return report(stderr, err)
		}
	}

	conversationContext := promptText
	maxTurns := uint32(1)
	if *consult {
		promptText += "\n\nBefore answering, write one question for another agent to help with this request. Return only the question."
		maxTurns = 2
	} else if *review {
		promptText += "\n\nAct as the planner. Give the coder a concise plan for this request. Return only the plan."
		maxTurns = 2
	}
	requestHash := sha256Hex(promptText)
	configurationHash := sha256Hex(strings.Join([]string{commandPath, strings.Join(cliArgs, " "), workingDir, profile}, "\n"))
	identity := provider.Identity{
		ProviderID: providerID, AccountID: "local-profile", SDKVersion: "acp-v1",
		ConfigurationSHA256: configurationHash,
	}
	config := provider.ACPConfig{
		Command: commandPath, Args: cliArgs,
		WorkingDirectory: workingDir, HomeDirectory: profile, Identity: identity,
	}
	if *backend == "opencode" {
		config.RequiredModel = "opencode/big-pickle"
	}
	adapter, err := provider.NewACPAdapter(config)
	if err != nil {
		return report(stderr, err)
	}
	jobID, err := domain.NewID("job")
	if err != nil {
		_ = adapter.Close()
		return report(stderr, err)
	}
	if *localJobID != "" {
		jobID = *localJobID
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
		RequestSHA256: requestHash, PromptSHA256: requestHash, Prompt: promptText,
		MaxTurns: maxTurns, MaxTokens: ^uint64(0), Deadline: time.Now().Add(*timeout),
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
	}, nil)
	if resultErr != nil {
		_ = adapter.Close()
		return report(stderr, resultErr)
	}
	if *consult || *review {
		replyToID := question.Message.ID
		record := func(label, body string) error {
			commandID, err := domain.NewID("cmd")
			if err != nil {
				return err
			}
			outcome, err := communication.SendFromOrchestrator(ctx, controller.SystemMessageRequest{
				JobID: jobID, CommandID: commandID,
				Recipient:        domain.ActorRef{Kind: domain.ActorController, ID: domain.ControllerActorID},
				ReplyToMessageID: replyToID,
				Body:             label + "\n" + body,
			})
			if err == nil {
				replyToID = outcome.Message.ID
				fmt.Fprintf(stdout, "%s\n%s\n\n", label, body)
			}
			return err
		}
		if *review {
			if err := record("Planner plan:", result.Content); err != nil {
				_ = adapter.Close()
				return report(stderr, err)
			}
			if err := reviewLocalJob(ctx, adapter, request, conversationContext, result.Content, record); err != nil {
				_ = adapter.Close()
				return report(stderr, err)
			}
			if err := adapter.Close(); err != nil {
				return report(stderr, fmt.Errorf("%s process did not close cleanly", cliName))
			}
			fmt.Fprintln(stdout, "Local Job conversation saved (not an authorized Job execution)")
			return 0
		}
		if err := record("Main agent question:", result.Content); err != nil {
			_ = adapter.Close()
			return report(stderr, err)
		}
		final, err := consultAgent(ctx, adapter, request, start.SessionID, conversationContext, result.Content, record)
		if err != nil {
			_ = adapter.Close()
			return report(stderr, err)
		}
		if err := record("Main agent answer:", final); err != nil {
			_ = adapter.Close()
			return report(stderr, err)
		}
		if err := adapter.Close(); err != nil {
			return report(stderr, fmt.Errorf("%s process did not close cleanly", cliName))
		}
		fmt.Fprintln(stdout, "Local Job conversation saved (not an authorized Job execution)")
		return 0
	}
	var saveErr error
	if communication != nil {
		commandID, err := domain.NewID("cmd")
		if err == nil {
			_, err = communication.SendFromOrchestrator(ctx, controller.SystemMessageRequest{
				JobID: jobID, CommandID: commandID,
				Recipient:        domain.ActorRef{Kind: domain.ActorController, ID: domain.ControllerActorID},
				ReplyToMessageID: question.Message.ID,
				Body:             result.Content,
			})
		}
		saveErr = err
	}
	closeErr := adapter.Close()
	if closeErr != nil {
		closeErr = fmt.Errorf("%s process did not close cleanly", cliName)
	}
	fmt.Fprintf(stdout, "Provider: %s\nSession: %s\nToken usage: not reported by ACP\n\n%s\n", identity.ProviderID, start.SessionID, result.Content)
	if saveErr != nil {
		return report(stderr, errors.Join(saveErr, closeErr))
	}
	if communication != nil {
		fmt.Fprintln(stdout, "\nLocal Job conversation saved (not an authorized Job execution)")
	}
	if closeErr != nil {
		return report(stderr, closeErr)
	}
	return 0
}

func reviewLocalJob(ctx context.Context, adapter *provider.ACPAdapter, request provider.StartRequest, jobContext, plan string, record func(string, string) error) error {
	coder, draft, err := startLocalAgent(ctx, adapter, request, "Act as the coder. Work on this request using the plan; provide your complete work as text.\n\n"+
		jobContext+"\n\nPlanner plan:\n"+plan)
	if err != nil {
		return err
	}
	if err := record("Coder work:", draft); err != nil {
		return err
	}
	reviewer, feedback, err := startLocalAgent(ctx, adapter, request, "Act as the reviewer. Review the coder's work against the request and plan; give concrete feedback, or say no changes are needed.\n\n"+
		jobContext+"\n\nPlanner plan:\n"+plan+"\n\nCoder work:\n"+draft)
	if err != nil {
		return err
	}
	if err := record("Reviewer feedback:", feedback); err != nil {
		return err
	}
	revision, err := sendLocalAgent(ctx, adapter, request, coder, "Consider the review below. Revise your work only where warranted and provide the complete result.\n\nReviewer feedback:\n"+feedback)
	if err != nil {
		return err
	}
	if err := record("Coder revision:", revision); err != nil {
		return err
	}
	final, err := sendLocalAgent(ctx, adapter, request, reviewer, "Re-review the coder's revised work against the original request and your previous feedback.\n\nCoder revision:\n"+revision)
	if err != nil {
		return err
	}
	return record("Reviewer re-review:", final)
}

func consultAgent(ctx context.Context, adapter *provider.ACPAdapter, request provider.StartRequest, mainSession, jobContext, question string, record func(string, string) error) (string, error) {
	_, answer, err := startLocalAgent(ctx, adapter, request, "Answer the main agent's question using this Job context:\n\n"+jobContext+
		"\n\nMain agent question:\n"+question)
	if err != nil {
		return "", err
	}
	if err := record("Consulting agent answer:", answer); err != nil {
		return "", err
	}
	return sendLocalAgent(ctx, adapter, request, mainSession,
		"The other agent answered your question:\n"+answer+"\n\nContinue and answer the original request.")
}

func startLocalAgent(ctx context.Context, adapter *provider.ACPAdapter, request provider.StartRequest, prompt string) (string, string, error) {
	var err error
	request.OperationID, err = domain.NewID("op")
	if err != nil {
		return "", "", err
	}
	request.RoleExecutionID, err = domain.NewID("exec")
	if err != nil {
		return "", "", err
	}
	request.Prompt = prompt
	request.PromptSHA256 = sha256Hex(request.Prompt)
	request.RequestSHA256 = request.PromptSHA256
	start, err := adapter.Start(ctx, request, nil)
	if err != nil {
		return "", "", err
	}
	result, err := adapter.Result(ctx, provider.ResultRequest{
		OperationID: start.OperationID, SessionID: start.SessionID,
	}, nil)
	return start.SessionID, result.Content, err
}

func sendLocalAgent(ctx context.Context, adapter *provider.ACPAdapter, request provider.StartRequest, session, prompt string) (string, error) {
	operationID, err := domain.NewID("op")
	if err != nil {
		return "", err
	}
	turnID, err := domain.NewID("turn")
	if err != nil {
		return "", err
	}
	turn, err := adapter.Send(ctx, provider.TurnRequest{
		OperationID: operationID, TurnID: turnID, SessionID: session,
		Prompt: prompt, PromptSHA256: sha256Hex(prompt),
		MaxTokens: request.MaxTokens, Deadline: request.Deadline,
	}, nil)
	if err != nil {
		return "", err
	}
	if turn.Status != "COMPLETED" {
		return "", errors.New("agent did not complete its response")
	}
	return turn.Output, nil
}

func localJobPrompt(job domain.Job, questionID string) (string, error) {
	var history strings.Builder
	fmt.Fprintf(&history, "Original request:\n%s", job.Request)
	for _, message := range job.JobMessages {
		if message.Sender.Kind == domain.ActorController && message.Recipient.Kind == domain.ActorOrchestrator {
			fmt.Fprintf(&history, "\n\nController:\n%s", message.Body)
		} else if message.Sender.Kind == domain.ActorOrchestrator && message.Recipient.Kind == domain.ActorController {
			fmt.Fprintf(&history, "\n\nProvider response:\n%s", message.Body)
		}
		if message.ID == questionID {
			return history.String(), nil
		}
	}
	return "", errors.New("saved Job question was not found")
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
	// Start from an allowlist: blocking a few known token names still leaks
	// credentials introduced by other SDKs or local tooling.
	allowed := map[string]struct{}{
		"PATH": {}, "TMPDIR": {}, "LANG": {}, "LC_ALL": {}, "LC_CTYPE": {}, "TERM": {},
	}
	environment := make([]string, 0, len(allowed)+4)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if _, keep := allowed[name]; keep {
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
