package cli

import (
	"context"
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
	if args[0] != "job" || len(args) < 2 {
		fmt.Fprintln(stderr, "expected: taxion job <create|show|list>")
		return 2
	}

	switch args[1] {
	case "create":
		return createJob(ctx, args[2:], stdout, stderr)
	case "show":
		return showJob(ctx, args[2:], stdout, stderr)
	case "list":
		return listJobs(ctx, args[2:], stdout, stderr)
	default:
		fmt.Fprintln(stderr, "expected: taxion job <create|show|list>")
		return 2
	}
}

func createJob(ctx context.Context, args []string, stdout, stderr io.Writer) int {
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
	receipt, err := database.CreateJob(ctx, job, commandID)
	if err != nil {
		return report(stderr, err)
	}
	fmt.Fprintf(stdout, "Job created\nID: %s\nState: %s\nRevision: %d\n", receipt.JobID, job.State, receipt.Revision)
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
		request := strings.Join(strings.Fields(job.Request), " ")
		if len(request) > 72 {
			request = request[:69] + "..."
		}
		fmt.Fprintf(writer, "%s\t%s\t%d\t%s\t%s\n", job.ID, job.State, job.Revision, job.UpdatedAt.Format(time.RFC3339), request)
	}
	if err := writer.Flush(); err != nil {
		return report(stderr, err)
	}
	return 0
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
  taxion job create --request "Describe the work you want to accomplish"
  taxion job show <job-id>
  taxion job list
  taxion version

Options:
  --store <path>   Override the local Job database path
  --limit <n>      Maximum Jobs to list (1-1000)
`)
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
