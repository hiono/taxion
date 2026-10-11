package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime/controller"
)

func workflowCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: taxion workflow {start|advance|show}")
	}
	action := args[0]
	fs := flag.NewFlagSet("workflow "+action, flag.ContinueOnError)
	var file, id string
	switch action {
	case "start":
		fs.StringVar(&file, "file", "", "JSON authorization and explicit review-flow ceilings")
	case "advance", "show":
		fs.StringVar(&id, "id", "", "review flow ID")
	default:
		return fmt.Errorf("unknown workflow action %q", action)
	}
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if len(fs.Args()) != 0 || action == "start" && file == "" || action != "start" && id == "" {
		return fmt.Errorf("workflow start requires --file; advance and show require --id")
	}
	db, state, err := loadWorkspace()
	if err != nil {
		return err
	}
	defer db.Close()
	switch action {
	case "start":
		input, err := os.Open(file)
		if err != nil {
			return err
		}
		defer input.Close()
		var request controller.ReviewFlowRequest
		if err := core.DecodeGraphInput(input, &request); err != nil {
			return err
		}
		flow, err := controller.StartReviewFlow(db, storePath, request)
		if err != nil {
			return err
		}
		return printJSON(flow)
	case "advance":
		flow, err := controller.AdvanceReviewFlow(db, storePath, id)
		if err != nil && !errors.Is(err, controller.ErrFlowStopped) {
			return err
		}
		return errors.Join(printJSON(flow), err)
	default:
		flow, ok := state.Graph.ReviewFlow(id)
		if !state.HasGraph || !ok {
			return fmt.Errorf("workflow %q not found", id)
		}
		return printJSON(flow)
	}
}
