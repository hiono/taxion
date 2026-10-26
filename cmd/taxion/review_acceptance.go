package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/store"
)

func acceptSavedReviewCommand(ctx context.Context, args []string, output io.Writer) error {
	fs := flag.NewFlagSet("workflow accept-saved-review", flag.ContinueOnError)
	file := fs.String("file", "", "JSON acceptance approval: flowId, actor, source, reason, allowExpired and expectedHash")
	preview := fs.Bool("preview", false, "verify saved results without changing state or running models/validation commands")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" || fs.NArg() != 0 {
		return fmt.Errorf("workflow accept-saved-review requires --file and no positional arguments")
	}
	input, err := os.Open(*file)
	if err != nil {
		return err
	}
	defer input.Close()
	var request controller.ReviewAcceptanceRequest
	if err := core.DecodeGraphInput(input, &request); err != nil {
		return err
	}
	if !*preview && request.ExpectedHash == "" {
		return fmt.Errorf("workflow accept-saved-review requires expectedHash from --preview before applying")
	}
	info, err := os.Stat(workspaceDBPath())
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("workflow accept-saved-review requires an existing workspace database")
	}
	resolved, err := resolveConfiguration()
	if err != nil {
		return err
	}
	configuration, err := approvalConfigurationFor(resolved.Config, resolved.Sources)
	if err != nil {
		return err
	}
	var db *store.Store
	if *preview {
		db, err = store.OpenReadOnly(workspaceDBPath())
	} else {
		db, err = store.Open(workspaceDBPath())
	}
	if err != nil {
		return err
	}
	defer db.Close()
	report, err := controller.AcceptSavedReview(ctx, db, storePath, request, configuration, *preview)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}
