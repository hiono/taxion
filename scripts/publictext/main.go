// publictext checks public files or renders an explicit review report without store access.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/publictext"
)

func run(args []string, output, warnings io.Writer) error {
	flags := flag.NewFlagSet("publictext", flag.ContinueOnError)
	flags.SetOutput(warnings)
	report := flags.String("report", "", "JSON review report to render; does not publish or approve")
	document := flags.Bool("document", false, "check technical Markdown under repository docs/; no symlinks")
	repository := flags.Bool("repository", false, "check repository docs, templates, release header and public golden output")
	references := flags.String("context", "", "JSON statement timestamp and state claims; verifies supported GitHub references through gh")
	progress := flags.String("progress", "", "explicit progress state to render; does not publish")
	reference := flags.String("reference", "", "existing result URL for completed or published progress")
	at := flags.String("at", "", "RFC3339 timestamp of the statement (default: now)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *report != "" && (*repository || flags.NArg() != 0 || *document) {
		return fmt.Errorf("report cannot be combined with file checks")
	}
	if *document && *repository {
		return fmt.Errorf("document cannot override repository public-message contexts")
	}
	if *progress == "" && (*reference != "" || *at != "") ||
		*progress != "" && (*report != "" || *references != "" || *repository || *document || flags.NArg() != 0) ||
		*references != "" && (*repository || *document) {
		return fmt.Errorf("temporal context and progress options cannot override document, repository or report modes")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var statement publictext.StatementContext
	if *references != "" {
		data, err := os.ReadFile(*references)
		if err != nil {
			return err
		}
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&statement); err != nil {
			return err
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return fmt.Errorf("statement context must contain exactly one JSON object")
		}
		if statement.At.IsZero() || statement.At.After(time.Now().UTC()) {
			return fmt.Errorf("statement context requires a nonfuture timestamp")
		}
	}
	digests := strings.Fields(os.Getenv("TAXION_PUBLIC_PRIVATE_IDENTIFIER_SHA256"))
	if len(digests) == 0 {
		if *repository {
			return fmt.Errorf("repository validation requires TAXION_PUBLIC_PRIVATE_IDENTIFIER_SHA256; protected identifier coverage is unavailable")
		}
		fmt.Fprintln(warnings, "Protected identifier list unavailable; no private-name coverage asserted.")
	}
	if *references == "" && *progress == "" {
		fmt.Fprintln(warnings, "Remote reference existence, chronology and state were not verified; use -context before publication.")
	}
	check := func(text string, doc bool) error {
		if err := publictext.Check(text, doc); err != nil {
			return err
		}
		if *references != "" {
			if err := publictext.CheckReferences(ctx, text, statement, resolveGitHub); err != nil {
				return err
			}
		}
		return publictext.CheckIdentifiers(text, digests)
	}
	if *progress != "" {
		when := time.Now().UTC()
		if *at != "" {
			var err error
			when, err = time.Parse(time.RFC3339, *at)
			if err != nil {
				return err
			}
		}
		body, err := publictext.RenderProgress(ctx, *progress, *reference, when, resolveGitHub)
		if err != nil {
			return err
		}
		if err := check(body, false); err != nil {
			return err
		}
		_, err = fmt.Fprint(output, body)
		return err
	}
	if *report != "" {
		file, err := os.Open(*report)
		if err != nil {
			return err
		}
		defer file.Close()
		input, err := io.ReadAll(io.LimitReader(file, 65537))
		if err != nil {
			return err
		}
		if len(input) > 65536 {
			return fmt.Errorf("review report exceeds 64 KiB")
		}
		decoder := json.NewDecoder(strings.NewReader(string(input)))
		decoder.DisallowUnknownFields()
		var review publictext.Review
		if err := decoder.Decode(&review); err != nil {
			return err
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return fmt.Errorf("review report must contain exactly one JSON object")
		}
		body, err := publictext.RenderReview(review)
		if err != nil {
			return err
		}
		if err := check(body, false); err != nil {
			return err
		}
		_, err = fmt.Fprint(output, body)
		return err
	}
	files := flags.Args()
	if *repository {
		for _, root := range []string{"docs", ".github", "internal/publictext/testdata"} {
			err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !entry.IsDir() && (strings.HasSuffix(path, ".md") || strings.HasSuffix(path, ".yml")) {
					files = append(files, path)
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
		files = append(files, "README.md", "CONTRIBUTING.md", "SECURITY.md", ".goreleaser.yaml")
	}
	if len(files) == 0 {
		return fmt.Errorf("provide files, -repository or -report FILE")
	}
	for _, path := range files {
		doc := *document || *repository && strings.HasPrefix(path, "docs"+string(filepath.Separator))
		if doc {
			clean := filepath.Clean(path)
			if filepath.IsAbs(clean) || !strings.HasPrefix(clean, "docs"+string(filepath.Separator)) || filepath.Ext(clean) != ".md" {
				return fmt.Errorf("document mode requires repository docs/ Markdown paths")
			}
			resolved, err := filepath.EvalSymlinks(clean)
			if err != nil {
				return err
			}
			if filepath.Clean(resolved) != clean {
				return fmt.Errorf("document mode does not follow symlinked paths")
			}
			path = clean
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := check(string(data), doc); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	if *repository && len(digests) != 0 {
		listing, err := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard").Output()
		if err != nil {
			return fmt.Errorf("cannot enumerate repository text for protected identifier scan: %w", err)
		}
		for _, path := range strings.Split(string(listing), "\x00") {
			switch filepath.Ext(path) {
			case ".go", ".py", ".js", ".ts", ".tsx", ".json", ".toml", ".md", ".html", ".yml", ".yaml", ".txt", ".mod", ".sum":
			default:
				continue
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if err := publictext.CheckIdentifiers(string(data), digests); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		}
	}
	_, err := fmt.Fprintf(output, "Public communication policy, path and secret-pattern checks passed for %d files.\n", len(files))
	return err
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
