package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/memory"
)

func specificationCmd(args []string) error {
	if len(args) == 0 || args[0] != "hash" {
		return fmt.Errorf("usage: taxion specification hash --file FILE")
	}
	fs := flag.NewFlagSet("specification hash", flag.ContinueOnError)
	path := fs.String("file", "", "structured specification JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *path == "" || fs.NArg() != 0 {
		return fmt.Errorf("specification hash: --file is required; no positional arguments accepted")
	}
	input, err := os.Open(*path)
	if err != nil {
		return err
	}
	defer input.Close()
	var spec core.Specification
	if err := core.DecodeGraphInput(input, &spec); err != nil {
		return err
	}
	hash, err := core.SpecificationHash(spec)
	if err != nil {
		return err
	}
	return printJSON(struct {
		ID   string `json:"id"`
		Hash string `json:"hash"`
	}{spec.ID, hash})
}

func memoryMigrationCmd(command string, args []string) error {
	fs := flag.NewFlagSet("memory "+command, flag.ContinueOnError)
	var input, output, hash, actor, source string
	var sensitive bool
	if command != "snapshot" {
		fs.StringVar(&input, "file", "", "private SQLite snapshot")
	}
	if command != "inspect-snapshot" {
		fs.StringVar(&output, "output", "", "fresh absolute local destination")
	}
	if command == "restore" {
		fs.StringVar(&hash, "expected-hash", "", "reviewed snapshot SHA256")
		fs.StringVar(&actor, "actor", "", "approving local administrator")
		fs.StringVar(&source, "source", "", "approval source reference")
		fs.BoolVar(&sensitive, "approve-sensitive", false, "explicitly approve findings for this local transfer")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("memory %s: positional arguments are not accepted", command)
	}
	if command != "snapshot" && input == "" {
		return fmt.Errorf("memory %s: --file is required", command)
	}
	if command != "inspect-snapshot" && !filepath.IsAbs(output) {
		return fmt.Errorf("memory %s: --output requires a fresh absolute local path", command)
	}
	ctx := context.Background()
	var report memory.SnapshotReport
	var err error
	switch command {
	case "snapshot":
		s, openErr := openMemory()
		if openErr != nil {
			return openErr
		}
		defer s.Close()
		report, err = s.Snapshot(ctx, output)
	case "inspect-snapshot":
		report, err = memory.InspectSnapshot(ctx, input)
	case "restore":
		report, err = memory.RestoreSnapshot(ctx, input, memory.MigrationApproval{
			Hash: hash, Destination: output, Actor: actor, Source: source, ApproveSensitive: sensitive,
		})
	default:
		return fmt.Errorf("unknown Memory migration command")
	}
	if err != nil {
		return err
	}
	return printJSON(report)
}
