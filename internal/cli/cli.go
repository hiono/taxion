package cli

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"github.com/hiono/taxion/internal/auth"
	"github.com/hiono/taxion/internal/controller"
	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/store"
)

type VersionInfo struct {
	Version  string
	Revision string
	BuiltAt  string
	Channel  string
}

func Run(ctx context.Context, args []string, stdout, stderr io.Writer, version VersionInfo) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		printUsage(stdout)
		return 0
	}
	if args[0] == "version" {
		fmt.Fprintf(stdout, "taxion %s (%s, %s, %s)\n", valueOr(version.Version, "dev"), valueOr(version.Revision, "unknown"), valueOr(version.BuiltAt, "unknown"), valueOr(version.Channel, "development"))
		return 0
	}
	if args[0] == "auth" {
		if len(args) != 2 || args[1] != "login" {
			fmt.Fprintln(stderr, "コマンドを確認してください: taxion auth login")
			return 2
		}
		return login(ctx, stdout, stderr)
	}
	if args[0] == "dev" {
		if len(args) >= 3 && args[1] == "job" && args[2] == "create" {
			return createLocalJob(ctx, args[3:], stdout, stderr)
		}
		if len(args) >= 3 && args[1] == "provider" {
			switch args[2] {
			case "login":
				return loginACPProvider(ctx, args[3:], stdout, stderr)
			case "run":
				return runACPProvider(ctx, args[3:], stdout, stderr)
			}
		}
		fmt.Fprintln(stderr, "commands: taxion dev job create, taxion dev provider login, taxion dev provider run")
		return 2
	}
	if args[0] != "job" || len(args) < 2 {
		fmt.Fprintln(stderr, "コマンドを確認してください: taxion auth login、taxion dev job create、taxion job <create|authorize-planning|start-planning|approve|amend|show|list>")
		return 2
	}

	switch args[1] {
	case "create":
		return createTrustedJob(ctx, args[2:], stdout, stderr)
	case "authorize-planning":
		return authorizePlanning(ctx, args[2:], stdout, stderr)
	case "start-planning":
		return startPlanning(ctx, args[2:], stdout, stderr)
	case "approve":
		return approveJob(ctx, args[2:], stdout, stderr)
	case "amend":
		return amendJob(ctx, args[2:], stdout, stderr)
	case "show":
		return showJob(ctx, args[2:], stdout, stderr)
	case "list":
		return listJobs(ctx, args[2:], stdout, stderr)
	default:
		fmt.Fprintln(stderr, "コマンドを確認してください: taxion job <authorize-planning|start-planning|approve|amend|show|list>")
		return 2
	}
}

func login(ctx context.Context, stdout, stderr io.Writer) int {
	principal, err := authenticate(ctx, stdout)
	if err != nil {
		return report(stderr, err)
	}
	fmt.Fprintf(stdout, "\nOIDC認証を検証しました。\nHUMAN Principal ID: %s\nTokenとログインSessionは保存していません。\n", principal.PrincipalID())
	return 0
}

func authenticate(ctx context.Context, output io.Writer) (auth.AuthenticatedPrincipal, error) {
	config, err := auth.ConfigFromEnv()
	if err != nil {
		return auth.AuthenticatedPrincipal{}, err
	}
	authenticator, err := auth.NewOIDCAuthenticator(config)
	if err != nil {
		return auth.AuthenticatedPrincipal{}, err
	}
	return authenticator.Authenticate(ctx, output)
}

func createTrustedJob(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("taxion job create", flag.ContinueOnError)
	flags.SetOutput(stderr)
	request := flags.String("request", "", "Original natural-language request")
	path := flags.String("store", "", "Override the local Job database path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "unexpected positional arguments")
		return 2
	}
	if strings.TrimSpace(*request) == "" {
		fmt.Fprintln(stderr, "job request is required; use --request")
		return 2
	}
	if !utf8.ValidString(*request) {
		fmt.Fprintln(stderr, "job request must be valid UTF-8")
		return 2
	}
	dbPath, err := resolveStorePath(*path)
	if err != nil {
		return report(stderr, err)
	}
	receivedAt := time.Now().UTC()
	commandID, err := domain.NewID("cmd")
	if err != nil {
		return report(stderr, err)
	}
	principal, err := authenticate(ctx, stdout)
	if err != nil {
		return report(stderr, err)
	}
	return createTrustedJobWithPrincipal(ctx, *request, dbPath, commandID, receivedAt, principal, stdout, stderr)
}

func createTrustedJobWithPrincipal(ctx context.Context, request, dbPath, commandID string, receivedAt time.Time, principal auth.AuthenticatedPrincipal, stdout, stderr io.Writer) int {
	if err := principal.Validate(); err != nil {
		return report(stderr, err)
	}
	jobID, err := domain.NewID("job")
	if err != nil {
		return report(stderr, err)
	}
	database, err := store.Open(ctx, dbPath)
	if err != nil {
		return report(stderr, err)
	}
	defer database.Close()
	receipt, err := database.CreateTrustedDraftJob(ctx, jobID, request, commandID, receivedAt, principal)
	if err != nil {
		return report(stderr, err)
	}
	fmt.Fprintf(stdout, "Trusted Job created\nID: %s\nState: %s\nRevision: %d\nHUMAN Principal ID: %s\n", receipt.JobID, domain.JobDraft, receipt.Revision, principal.PrincipalID())
	return 0
}

func createLocalJob(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("taxion dev job create", flag.ContinueOnError)
	flags.SetOutput(stderr)
	request := flags.String("request", "", "Original natural-language request")
	path := flags.String("store", "", "Override the local Job database path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "unexpected positional arguments")
		return 2
	}
	if strings.TrimSpace(*request) == "" {
		fmt.Fprintln(stderr, "job request is required; use --request")
		return 2
	}
	dbPath, err := resolveStorePath(*path)
	if err != nil {
		return report(stderr, err)
	}
	database, err := store.Open(ctx, dbPath)
	if err != nil {
		return report(stderr, err)
	}
	defer database.Close()

	jobID, err := domain.NewID("job")
	if err != nil {
		return report(stderr, err)
	}
	commandID, err := domain.NewID("cmd")
	if err != nil {
		return report(stderr, err)
	}
	job, err := domain.NewDraftJob(jobID, *request, time.Now().UTC())
	if err != nil {
		return report(stderr, err)
	}
	receipt, err := database.CreateLocalDraftJob(ctx, job, commandID)
	if err != nil {
		return report(stderr, err)
	}
	fmt.Fprintf(stdout, "Job created\nID: %s\nState: %s\nRevision: %d\n", receipt.JobID, job.State, receipt.Revision)
	return 0
}

func authorizePlanning(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("taxion job authorize-planning", flag.ContinueOnError)
	flags.SetOutput(stderr)
	provider := flags.String("provider", "", "Stable Provider identity")
	account := flags.String("account", "", "Stable Provider account identity")
	configurationSHA256 := flags.String("configuration-sha256", "", "SHA-256 of the non-secret Provider configuration")
	repository := flags.String("repository", "", "HTTPS repository URL")
	commit := flags.String("commit", "", "Immutable repository commit SHA")
	readOnlyMode := flags.String("read-only-mode", "", "Read-only context mode: snapshot or documents")
	readOnlyReference := flags.String("read-only-reference", "", "Immutable snapshot or document-set identity")
	readOnlySHA256 := flags.String("read-only-sha256", "", "SHA-256 of the immutable read-only context")
	maxTurns := flags.Uint("max-turns", 0, "Maximum planning turns")
	maxTokens := flags.Uint64("max-tokens", 0, "Maximum planning tokens")
	deadlineText := flags.String("deadline", "", "Grant deadline in RFC3339 format")
	commandID := flags.String("command-id", "", "Command ID for idempotent retry")
	path := flags.String("store", "", "Override the local Job database path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: taxion job authorize-planning --provider id --account id --configuration-sha256 sha256 --repository https-url --commit sha --read-only-mode snapshot|documents --read-only-reference id --read-only-sha256 sha256 --max-turns n --max-tokens n --deadline RFC3339 <job-id>")
		return 2
	}
	if strings.TrimSpace(*provider) == "" || strings.TrimSpace(*account) == "" ||
		strings.TrimSpace(*configurationSHA256) == "" || strings.TrimSpace(*repository) == "" ||
		strings.TrimSpace(*commit) == "" || strings.TrimSpace(*readOnlyReference) == "" ||
		strings.TrimSpace(*readOnlySHA256) == "" || *maxTurns == 0 ||
		*maxTurns > uint(^uint32(0)) || *maxTokens == 0 || strings.TrimSpace(*deadlineText) == "" {
		fmt.Fprintln(stderr, "Provider、account、configuration、repository、read-only boundary、budget、deadlineは必須です")
		return 2
	}
	if *readOnlyMode != domain.PlanningReadOnlyBoundarySnapshot &&
		*readOnlyMode != domain.PlanningReadOnlyBoundaryDocuments {
		fmt.Fprintln(stderr, "read-only-modeはsnapshotまたはdocumentsを指定してください")
		return 2
	}
	deadline, err := time.Parse(time.RFC3339, *deadlineText)
	if err != nil || !deadline.After(time.Now().UTC()) {
		fmt.Fprintln(stderr, "deadlineは現在時刻より後のRFC3339日時を指定してください")
		return 2
	}
	dbPath, err := resolveStorePath(*path)
	if err != nil {
		return report(stderr, err)
	}
	request := domain.PlanningAuthorizationRequest{
		ProviderID:          *provider,
		AccountID:           *account,
		ConfigurationSHA256: *configurationSHA256,
		RepositoryID:        strings.TrimRight(*repository, "/") + "@" + *commit,
		ReadOnlyBoundary: domain.PlanningReadOnlyBoundary{
			Mode:      *readOnlyMode,
			Reference: *readOnlyReference,
			SHA256:    *readOnlySHA256,
		},
		Budget:   domain.PlanningBudget{MaxTurns: uint32(*maxTurns), MaxTokens: *maxTokens},
		Deadline: deadline,
	}
	resolvedCommandID := strings.TrimSpace(*commandID)
	if resolvedCommandID == "" {
		resolvedCommandID, err = domain.NewID("cmd")
		if err != nil {
			return report(stderr, err)
		}
	}
	principal, err := authenticate(ctx, stdout)
	if err != nil {
		return report(stderr, err)
	}
	return authorizePlanningWithPrincipal(ctx, flags.Arg(0), request, resolvedCommandID, dbPath, principal, stdout, stderr)
}

func authorizePlanningWithPrincipal(ctx context.Context, jobID string, request domain.PlanningAuthorizationRequest, commandID, dbPath string, principal auth.AuthenticatedPrincipal, stdout, stderr io.Writer) int {
	if err := principal.Validate(); err != nil {
		return report(stderr, err)
	}
	database, err := store.Open(ctx, dbPath)
	if err != nil {
		return report(stderr, err)
	}
	defer database.Close()
	job, err := database.GetJob(ctx, jobID)
	if err != nil {
		return report(stderr, err)
	}
	issuedAt := time.Now().UTC()
	grant, err := domain.NewPlanningAuthorization(job, principal.PrincipalID(), commandID, request, issuedAt)
	if err != nil {
		return report(stderr, err)
	}
	receipt, err := database.AddPlanningAuthorization(ctx, jobID, job.Revision, commandID, grant, principal)
	if err != nil {
		return report(stderr, err)
	}
	fmt.Fprintf(stdout, "PlanningAuthorization issued\nID: %s\nCommand ID: %s\nJob ID: %s\nState: %s\nRevision: %d\nDeadline: %s\n",
		grant.ID, receipt.CommandID, receipt.JobID, job.State, receipt.Revision, grant.Deadline.Format(time.RFC3339))
	return 0
}

func startPlanning(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("taxion job start-planning", flag.ContinueOnError)
	flags.SetOutput(stderr)
	authorizationID := flags.String("authorization-id", "", "PlanningAuthorization ID")
	commandID := flags.String("command-id", "", "Command ID for idempotent retry")
	path := flags.String("store", "", "Override the local Job database path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 || strings.TrimSpace(*authorizationID) == "" {
		fmt.Fprintln(stderr, "usage: taxion job start-planning --authorization-id <grant-id> [--command-id <id>] [--store path] <job-id>")
		return 2
	}
	dbPath, err := resolveStorePath(*path)
	if err != nil {
		return report(stderr, err)
	}
	resolvedCommandID := strings.TrimSpace(*commandID)
	if resolvedCommandID == "" {
		resolvedCommandID, err = domain.NewID("cmd")
		if err != nil {
			return report(stderr, err)
		}
	}
	principal, err := authenticate(ctx, stdout)
	if err != nil {
		return report(stderr, err)
	}
	return startPlanningWithPrincipal(ctx, flags.Arg(0), strings.TrimSpace(*authorizationID),
		resolvedCommandID, dbPath, principal, stdout, stderr)
}

func startPlanningWithPrincipal(ctx context.Context, jobID, authorizationID, commandID, dbPath string,
	principal auth.AuthenticatedPrincipal, stdout, stderr io.Writer) int {
	if err := principal.Validate(); err != nil {
		return report(stderr, err)
	}
	database, err := store.Open(ctx, dbPath)
	if err != nil {
		return report(stderr, err)
	}
	defer database.Close()
	job, err := database.GetJob(ctx, jobID)
	if err != nil {
		return report(stderr, err)
	}
	receipt, execution, intent, err := database.StartTasklessPlanner(
		ctx, jobID, job.Revision, authorizationID, commandID, principal,
	)
	if err != nil {
		return report(stderr, err)
	}
	fmt.Fprintf(stdout,
		"Taskless PLANNER Start intent persisted\nRoleExecution ID: %s\nOperation ID: %s\nPlanningAuthorization ID: %s\nCommand ID: %s\nJob ID: %s\nState: %s\nRevision: %d\nProvider: %s\nAccount: %s\n",
		execution.ID, intent.ID, execution.PlanningAuthorizationID, receipt.CommandID, receipt.JobID,
		job.State, receipt.Revision, intent.ProviderID, intent.AccountID,
	)
	return 0
}

func amendJob(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("taxion job amend", flag.ContinueOnError)
	flags.SetOutput(stderr)
	revision := flags.Uint64("revision", 0, "Exact current approved JobContract revision to amend")
	canonicalHash := flags.String("canonical-hash", "", "Exact current JobContract canonical SHA-256")
	reason := flags.String("reason", "", "Human amendment reason")
	contractFile := flags.String("contract-file", "", "Path to the replacement JobContract JSON file")
	commandID := flags.String("command-id", "", "Command ID for idempotent retry")
	path := flags.String("store", "", "Override the local Job database path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 || *revision == 0 || !utf8.ValidString(*reason) || strings.TrimSpace(*reason) == "" || len(strings.TrimSpace(*reason)) > 2000 || strings.TrimSpace(*contractFile) == "" {
		fmt.Fprintln(stderr, "usage: taxion job amend --revision <n> --canonical-hash <sha256> --reason <text> --contract-file <path> [--command-id <id>] [--store path] <job-id>")
		return 2
	}
	decodedHash, err := hex.DecodeString(*canonicalHash)
	if err != nil || len(decodedHash) != 32 || *canonicalHash != strings.ToLower(*canonicalHash) {
		fmt.Fprintln(stderr, "--canonical-hash must be a lowercase SHA-256 digest")
		return 2
	}
	file, err := os.Open(*contractFile)
	if err != nil {
		return report(stderr, err)
	}
	contractJSON, readErr := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	closeErr := file.Close()
	if readErr != nil {
		return report(stderr, readErr)
	}
	if closeErr != nil {
		return report(stderr, closeErr)
	}
	if len(contractJSON) > 1<<20 {
		fmt.Fprintln(stderr, "JobContract JSON must not exceed 1 MiB")
		return 2
	}
	if !utf8.Valid(contractJSON) {
		fmt.Fprintln(stderr, "JobContract JSON must be valid UTF-8")
		return 2
	}
	contract, err := controller.DecodeJobContractJSON(string(contractJSON))
	if err != nil {
		return report(stderr, err)
	}
	dbPath, err := resolveStorePath(*path)
	if err != nil {
		return report(stderr, err)
	}
	resolvedCommandID := strings.TrimSpace(*commandID)
	if resolvedCommandID == "" {
		resolvedCommandID, err = domain.NewID("cmd")
		if err != nil {
			return report(stderr, err)
		}
	}
	principal, err := authenticate(ctx, stdout)
	if err != nil {
		return report(stderr, err)
	}
	return amendJobWithPrincipal(
		ctx, flags.Arg(0), *revision, *canonicalHash, *reason, contract,
		resolvedCommandID, dbPath, principal, stdout, stderr,
	)
}

func amendJobWithPrincipal(
	ctx context.Context, jobID string, basedOnRevision uint64, basedOnCanonicalHash, reason string,
	contract domain.JobContract, commandID, dbPath string, principal auth.AuthenticatedPrincipal, stdout, stderr io.Writer,
) int {
	if err := principal.Validate(); err != nil {
		return report(stderr, err)
	}
	database, err := store.Open(ctx, dbPath)
	if err != nil {
		return report(stderr, err)
	}
	defer database.Close()
	job, err := database.GetJob(ctx, jobID)
	if err != nil {
		return report(stderr, err)
	}
	contractController, err := controller.NewJobContractController(database)
	if err != nil {
		return report(stderr, err)
	}
	receipt, revision, err := contractController.Amend(
		ctx, jobID, job.Revision, commandID, basedOnRevision, basedOnCanonicalHash, reason, contract, principal,
	)
	if err != nil {
		return report(stderr, err)
	}
	currentJob, err := database.GetJob(ctx, jobID)
	if err != nil {
		return report(stderr, err)
	}
	fmt.Fprintf(stdout,
		"JobContract amendment recorded\\nID: %s\\nCommand ID: %s\\nJob ID: %s\\nRevision: %d\\nCanonical Hash: %s\\nOrigin: HUMAN_AMENDMENT\\nState at amendment: DRAFT\\nAmendment Job Revision: %d\\nCurrent Job State: %s\\nCurrent Job Revision: %d\\n",
		revision.ID, receipt.CommandID, receipt.JobID, revision.Revision, revision.CanonicalHash,
		receipt.Revision, currentJob.State, currentJob.Revision,
	)
	return 0
}

func approveJob(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("taxion job approve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	revision := flags.Uint64("revision", 0, "Exact JobContract revision to approve")
	canonicalHash := flags.String("canonical-hash", "", "Exact JobContract canonical SHA-256")
	canonicalizationVersion := flags.Uint("canonicalization-version", 0, "Exact JobContract canonicalization version")
	reason := flags.String("reason", "", "Human approval reason")
	commandID := flags.String("command-id", "", "Command ID for idempotent retry")
	path := flags.String("store", "", "Override the local Job database path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 || *revision == 0 || *canonicalizationVersion == 0 ||
		*canonicalizationVersion > uint(^uint32(0)) || strings.TrimSpace(*reason) == "" {
		fmt.Fprintln(stderr, "usage: taxion job approve --revision <n> --canonical-hash <sha256> --canonicalization-version <n> --reason <text> [--command-id <id>] [--store path] <job-id>")
		return 2
	}
	decodedHash, err := hex.DecodeString(*canonicalHash)
	if err != nil || len(decodedHash) != 32 || *canonicalHash != strings.ToLower(*canonicalHash) {
		fmt.Fprintln(stderr, "--canonical-hash must be a lowercase SHA-256 digest")
		return 2
	}
	dbPath, err := resolveStorePath(*path)
	if err != nil {
		return report(stderr, err)
	}
	resolvedCommandID := strings.TrimSpace(*commandID)
	if resolvedCommandID == "" {
		resolvedCommandID, err = domain.NewID("cmd")
		if err != nil {
			return report(stderr, err)
		}
	}
	principal, err := authenticate(ctx, stdout)
	if err != nil {
		return report(stderr, err)
	}
	return approveJobWithPrincipal(
		ctx, flags.Arg(0), *revision, *canonicalHash, uint32(*canonicalizationVersion),
		*reason, resolvedCommandID, dbPath, principal, stdout, stderr,
	)
}

func approveJobWithPrincipal(
	ctx context.Context, jobID string, revision uint64, canonicalHash string,
	canonicalizationVersion uint32, reason, commandID, dbPath string,
	principal auth.AuthenticatedPrincipal, stdout, stderr io.Writer,
) int {
	if err := principal.Validate(); err != nil {
		return report(stderr, err)
	}
	database, err := store.Open(ctx, dbPath)
	if err != nil {
		return report(stderr, err)
	}
	defer database.Close()
	contractController, err := controller.NewJobContractController(database)
	if err != nil {
		return report(stderr, err)
	}
	receipt, approval, err := contractController.Approve(
		ctx, jobID, commandID, revision, canonicalHash,
		canonicalizationVersion, reason, principal,
	)
	if err != nil {
		return report(stderr, err)
	}
	fmt.Fprintf(stdout,
		"JobApproval recorded\nID: %s\nCommand ID: %s\nJob ID: %s\nContract Revision: %d\nCanonical Hash: %s\nState: ACTIVE\nRevision: %d\n",
		approval.ID, receipt.CommandID, receipt.JobID, approval.ContractRevision,
		approval.CanonicalHash, receipt.Revision,
	)
	return 0
}

func showJob(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("taxion job show", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("store", "", "Override the local Job database path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: taxion job show [--store path] <job-id>")
		return 2
	}
	dbPath, err := resolveStorePath(*path)
	if err != nil {
		return report(stderr, err)
	}
	database, err := store.Open(ctx, dbPath)
	if err != nil {
		return report(stderr, err)
	}
	defer database.Close()

	job, err := database.GetJob(ctx, flags.Arg(0))
	if err != nil {
		return report(stderr, err)
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(job); err != nil {
		return report(stderr, err)
	}
	return 0
}

func listJobs(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("taxion job list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("store", "", "Override the local Job database path")
	limit := flags.Int("limit", 50, "Maximum number of Jobs to display (1-1000)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "unexpected positional arguments")
		return 2
	}
	dbPath, err := resolveStorePath(*path)
	if err != nil {
		return report(stderr, err)
	}
	database, err := store.Open(ctx, dbPath)
	if err != nil {
		return report(stderr, err)
	}
	defer database.Close()

	jobs, err := database.ListJobs(ctx, *limit)
	if err != nil {
		return report(stderr, err)
	}
	writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "ID\tSTATE\tREVISION\tUPDATED\tREQUEST")
	for _, job := range jobs {
		request := summarizeRequest(job.Request)
		fmt.Fprintf(writer, "%s\t%s\t%d\t%s\t%s\n", job.ID, job.State, job.Revision, job.UpdatedAt.Format(time.RFC3339), request)
	}
	if err := writer.Flush(); err != nil {
		return report(stderr, err)
	}
	return 0
}

func summarizeRequest(request string) string {
	normalized := strings.Join(strings.Fields(request), " ")
	runes := []rune(normalized)
	if len(runes) > 72 {
		return string(runes[:69]) + "..."
	}
	return normalized
}

func resolveStorePath(override string) (string, error) {
	if strings.TrimSpace(override) != "" {
		return override, nil
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(configDir, "taxion", "jobs.sqlite"), nil
}

func report(stderr io.Writer, err error) int {
	if errors.Is(err, store.ErrNotFound) {
		fmt.Fprintln(stderr, "Job not found")
		return 1
	}
	fmt.Fprintln(stderr, err)
	return 1
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `Taxion — turn human intent into continuous collaborative work

Usage:
  taxion auth login
  taxion job create --request "Trusted OIDC-authenticated DRAFT"
  taxion job authorize-planning [scope and budget options] <job-id>
  taxion job start-planning --authorization-id <grant-id> <job-id>
  taxion job approve --revision <n> --canonical-hash <sha256> --canonicalization-version <n> --reason <text> <job-id>
  taxion dev job create --request "Local untrusted DRAFT only"
  taxion dev provider login
  taxion dev provider run --prompt "..."
  taxion job show <job-id>
  taxion job list
  taxion version

Options:
  --store <path>   Override the local Job database path
  --limit <n>      Maximum Jobs to list (1-1000)

Planning authorization options:
  --provider <id> --account <id> --configuration-sha256 <hex>
  --repository <https-url> --commit <40-or-64-hex>
  --read-only-mode <snapshot|documents>
  --read-only-reference <id> --read-only-sha256 <hex>
  --max-turns <n> --max-tokens <n> --deadline <RFC3339>
  --command-id <id>  Reuse the same ID to retry an identical request

OIDC environment:
  TAXION_OIDC_ISSUER
  TAXION_OIDC_CLIENT_ID
  TAXION_OIDC_AUDIENCE       Defaults to client ID
  TAXION_OIDC_REDIRECT_URI   Defaults to http://127.0.0.1:8765/callback
`)
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
