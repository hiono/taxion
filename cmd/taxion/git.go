package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/hiono/taxion/internal/gitguard"
)

func checkGitIndex(args []string, output io.Writer) error {
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
