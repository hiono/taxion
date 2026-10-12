package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/hiono/taxion/internal/gitguard"
)

func checkGitIndex(args []string, output io.Writer) error {
	if len(args) > 0 && args[0] == "hook" {
		return gitHook(args[1:], output)
	}
	if len(args) > 0 && args[0] == "check-range" {
		fs := flag.NewFlagSet("git check-range", flag.ContinueOnError)
		base := fs.String("base", "", "full acknowledged remote base SHA")
		head := fs.String("head", "", "full unpublished head SHA")
		scanner := fs.String("scanner", "betterleaks", "trusted Betterleaks 1.9.0 executable")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return fmt.Errorf("git check-range accepts no positional arguments")
		}
		directory, err := os.Getwd()
		if err != nil {
			return err
		}
		if err := gitguard.CheckRange(context.Background(), directory, *base, *head, *scanner); err != nil {
			return err
		}
		_, err = fmt.Fprintf(output, "All commit trees and messages in %s..%s passed local publication checks. Publish this exact range.\n", *base, *head)
		return err
	}
	if len(args) == 0 || args[0] != "check-index" {
		return fmt.Errorf("git requires check-index, check-range or hook")
	}
	fs := flag.NewFlagSet("git check-index", flag.ContinueOnError)
	secrets := fs.Bool("secrets", false, "scan indexed contents locally with Betterleaks 1.9.0")
	scanner := fs.String("scanner", "betterleaks", "trusted Betterleaks executable (--secrets only)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("git check-index accepts no positional arguments")
	}
	scannerSelected := false
	fs.Visit(func(option *flag.Flag) {
		if option.Name == "scanner" {
			scannerSelected = true
		}
	})
	if scannerSelected && !*secrets {
		return fmt.Errorf("git check-index --scanner requires --secrets")
	}
	directory, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("locate commit worktree: %w", err)
	}
	if *secrets {
		if err := gitguard.CheckSecrets(context.Background(), directory, *scanner); err != nil {
			return err
		}
		_, err = fmt.Fprintln(output, "Commit index passed prohibited-path and local Betterleaks 1.9.0 content checks. This does not guarantee absence of secrets; rerun after changing the index.")
		return err
	}
	if err := gitguard.CheckIndex(context.Background(), directory); err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, "Commit index contains no prohibited .taxion or dotenv paths. Secret content scanning has not been performed.")
	return err
}

func gitHook(args []string, output io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("git hook requires install [--scanner PATH | --paths-only], status, or uninstall")
	}
	var scannerPath string
	switch args[0] {
	case "install":
		fs := flag.NewFlagSet("git hook install", flag.ContinueOnError)
		pathsOnly := fs.Bool("paths-only", false, "explicitly install without secret content scanning")
		scanner := fs.String("scanner", "betterleaks", "trusted Betterleaks 1.9.0 executable")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return fmt.Errorf("git hook install accepts no positional arguments")
		}
		scannerSelected := false
		fs.Visit(func(option *flag.Flag) {
			scannerSelected = scannerSelected || option.Name == "scanner"
		})
		if *pathsOnly && scannerSelected {
			return fmt.Errorf("git hook install --paths-only cannot select a scanner")
		}
		if !*pathsOnly {
			path, err := exec.LookPath(*scanner)
			if err != nil {
				return fmt.Errorf("required Betterleaks scanner is unavailable; install version 1.9.0 or explicitly select --paths-only")
			}
			scannerPath, err = filepath.Abs(path)
			if err != nil {
				return fmt.Errorf("resolve hook scanner: %w", err)
			}
		}
	case "status", "uninstall":
		if len(args) != 1 {
			return fmt.Errorf("git hook status and uninstall accept no options or positional arguments")
		}
	default:
		return fmt.Errorf("git hook requires install [--scanner PATH | --paths-only], status, or uninstall")
	}
	directory, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("locate hook worktree: %w", err)
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate Taxion hook executable: %w", err)
	}
	installed, secrets, err := gitguard.ManageConfiguredHook(context.Background(), directory, executable, args[0], scannerPath)
	if err != nil {
		return err
	}
	if installed && secrets {
		_, err = fmt.Fprintln(output, "Taxion pre-commit hook is installed for prohibited paths and local Betterleaks 1.9.0 content checks at Git's effective hooks location (possibly shared). Detection has documented limits; local hooks can be bypassed.")
	} else if installed {
		_, err = fmt.Fprintln(output, "Taxion pre-commit hook is installed for prohibited paths only at Git's effective hooks location (possibly shared). Secret content scanning is not configured; local hooks can be bypassed.")
	} else {
		_, err = fmt.Fprintln(output, "Taxion pre-commit hook is not installed. Commits are not automatically checked by Taxion.")
	}
	return err
}
