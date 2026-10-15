package main

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"

	"github.com/hiono/taxion/internal/portable"
)

func workspaceMigrationCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("workspace requires snapshot, inspect-snapshot or restore")
	}
	command := args[0]
	if command != "snapshot" && command != "inspect-snapshot" && command != "restore" {
		return fmt.Errorf("unknown workspace migration command")
	}
	fs := flag.NewFlagSet("workspace "+command, flag.ContinueOnError)
	var input, output, hash, actor, source string
	if command != "snapshot" {
		fs.StringVar(&input, "file", "", "private portable SQLite snapshot")
	}
	if command != "inspect-snapshot" {
		fs.StringVar(&output, "output", "", "fresh absolute local destination")
	}
	if command == "restore" {
		fs.StringVar(&hash, "expected-hash", "", "reviewed snapshot SHA256")
		fs.StringVar(&actor, "actor", "", "approving local administrator")
		fs.StringVar(&source, "source", "", "approval source reference")
	}
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 || command != "snapshot" && input == "" ||
		command != "inspect-snapshot" && !filepath.IsAbs(output) {
		return fmt.Errorf("workspace %s requires explicit files, absolute output and no positional arguments", command)
	}
	ctx := context.Background()
	var report portable.Report
	var err error
	switch command {
	case "snapshot":
		if err := validateWorkspaceStorePath(); err != nil {
			return err
		}
		workspace, pathErr := filepath.Abs(workspaceDBPath())
		if pathErr != nil {
			return pathErr
		}
		mem, pathErr := filepath.Abs(memStorePath())
		if pathErr != nil {
			return pathErr
		}
		report, err = portable.Create(ctx, workspace, mem, output)
	case "inspect-snapshot":
		report, err = portable.Inspect(ctx, input)
	case "restore":
		report, err = portable.Restore(ctx, input, portable.Approval{
			Hash: hash, Destination: output, Actor: actor, Source: source,
		})
	}
	if err != nil {
		return err
	}
	return printJSON(report)
}
