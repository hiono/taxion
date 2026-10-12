package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/forge"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/store"
)

func recordsCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: taxion records {proposal|approve|send|reconcile|show|finding}")
	}
	fs := flag.NewFlagSet("records "+args[0], flag.ContinueOnError)
	file := fs.String("file", "", "exact publication request JSON")
	id := fs.String("id", "", "approved publication ID")
	scanner := fs.String("scanner", "betterleaks", "trusted local Betterleaks executable")
	claim := fs.String("claim", "", "exact previous sender claim")
	stopped := fs.Bool("sender-stopped", false, "confirm previous sender process has ended")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected records arguments")
	}
	db, err := openWorkspace()
	if err != nil {
		return err
	}
	defer db.Close()
	switch args[0] {
	case "finding":
		if *file == "" {
			return fmt.Errorf("finding requires --file")
		}
		input, err := os.Open(*file)
		if err != nil {
			return err
		}
		defer input.Close()
		var record store.FailureRecord
		if err := core.DecodeGraphInput(input, &record); err != nil {
			return err
		}
		saved, err := controller.RecordFailureAnalysis(db, record)
		if err != nil {
			return err
		}
		return printJSON(saved)
	case "proposal", "approve":
		if *file == "" {
			return fmt.Errorf("publication requires --file")
		}
		input, err := os.Open(*file)
		if err != nil {
			return err
		}
		defer input.Close()
		var request controller.PublicationRequest
		if err := core.DecodeGraphInput(input, &request); err != nil {
			return err
		}
		if args[0] == "proposal" {
			hash, err := controller.PublicationHash(request.Update)
			if err != nil {
				return err
			}
			return printJSON(controller.PublicationRequest{Update: request.Update, ExpectedHash: hash})
		}
		update, err := controller.ApprovePublication(context.Background(), db, request, *scanner)
		if err != nil {
			return err
		}
		return printJSON(update)
	case "send":
		update, err := controller.DeliverPublication(context.Background(), db, *id, *scanner, forge.Publisher{})
		if err != nil {
			return err
		}
		return printJSON(update)
	case "reconcile":
		update, err := controller.ReconcilePublication(context.Background(), db, *id, *claim, *stopped, forge.Publisher{})
		if err != nil {
			return err
		}
		return printJSON(update)
	case "show":
		state, err := db.LoadWorkspace()
		if err != nil {
			return err
		}
		return printJSON(state.Records)
	default:
		return fmt.Errorf("unknown records action %q", args[0])
	}
}
