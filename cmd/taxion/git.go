package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/hiono/taxion/internal/gitguard"
)

func checkGitIndex(args []string, output io.Writer) error {
	if len(args) > 0 && args[0] == "hook" {
		return gitHook(args[1:], output)
	}
	if len(args) != 1 || args[0] != "check-index" {
		return fmt.Errorf("git requires check-index with no options or positional arguments")
	}

	directory, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("locate commit worktree: %w", err)
	}
	if err := gitguard.CheckIndex(context.Background(), directory); err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, "Commit index contains no prohibited .taxion or dotenv paths. Secret content scanning has not been performed.")
	return err
}

func gitHook(args []string, output io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("git hook requires install --paths-only, status, or uninstall")
	}
	switch args[0] {
	case "install":
		if len(args) != 2 || args[1] != "--paths-only" {
			return fmt.Errorf("git hook install requires --paths-only; secret content scanning is not yet available")
		}
	case "status", "uninstall":
		if len(args) != 1 {
			return fmt.Errorf("git hook status and uninstall accept no options or positional arguments")
		}
	default:
		return fmt.Errorf("git hook requires install --paths-only, status, or uninstall")
	}
	directory, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("locate hook worktree: %w", err)
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate Taxion hook executable: %w", err)
	}
	installed, err := gitguard.ManageHook(context.Background(), directory, executable, args[0])
	if err != nil {
		return err
	}
	if installed {
		_, err = fmt.Fprintln(output, "Taxion pre-commit hook is installed for prohibited paths only at Git's effective hooks location (possibly shared). Secret content scanning is not configured; local hooks can be bypassed.")
	} else {
		_, err = fmt.Fprintln(output, "Taxion pre-commit hook is not installed. Commits are not automatically checked by Taxion.")
	}
	return err
}
