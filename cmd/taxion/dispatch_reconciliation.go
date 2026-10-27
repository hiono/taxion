package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/store"
)

func reconcileStoppedDispatchCommand(args []string, output io.Writer) error {
	fs := flag.NewFlagSet("workflow reconcile-stopped", flag.ContinueOnError)
	file := fs.String("file", "", "JSON human decision, quiescence evidence, unknown-delivery acceptance and expectedHash")
	preview := fs.Bool("preview", false, "inspect the stopped first Coder and approved input without changing workspace state")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" || fs.NArg() != 0 {
		return fmt.Errorf("workflow reconcile-stopped requires --file and no positional arguments")
	}
	input, err := os.Open(*file)
	if err != nil {
		return err
	}
	defer input.Close()
	var request controller.DispatchReconciliationRequest
	if err := core.DecodeGraphInput(input, &request); err != nil {
		return err
	}
	if !*preview && request.ExpectedHash == "" {
		return fmt.Errorf("workflow reconcile-stopped requires expectedHash from --preview")
	}
	info, err := os.Stat(workspaceDBPath())
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("workflow reconcile-stopped requires an existing workspace database")
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
	report, err := controller.ReconcileStoppedDispatch(db, request, *preview)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}
