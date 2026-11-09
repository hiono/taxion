package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime/controller"
)

func clarificationCmd(args []string, output io.Writer) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(output, "Usage of conversation clarification:\n  observe --task ID [--dispatch ID]\n  request --file FILE\n  answer --file FILE\n  history\n\nObservation only; no approval, Dispatch, HOLD or runtime stop.")
		return flag.ErrHelp
	}
	action := args[0]
	if action != "observe" && action != "request" && action != "answer" && action != "history" {
		return fmt.Errorf("unknown clarification action")
	}
	fs := flag.NewFlagSet("conversation clarification "+action, flag.ContinueOnError)
	fs.SetOutput(output)
	var taskID, dispatchID, file string
	if action == "observe" {
		fs.StringVar(&taskID, "task", "", "saved Task identity")
		fs.StringVar(&dispatchID, "dispatch", "", "saved sender Dispatch identity")
	} else if action != "history" {
		fs.StringVar(&file, "file", "", "strict JSON request or {id, answer} input")
	}
	fs.Usage = func() {
		fmt.Fprintf(output, "Usage of conversation clarification %s:\n", action)
		fmt.Fprintln(output, "Durable advisory clarification. Records observation only; never approves, dispatches or stops a runtime.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 ||
		action == "observe" && taskID == "" ||
		(action == "request" || action == "answer") && file == "" {
		return fmt.Errorf("clarification observe requires --task [--dispatch]; request/answer require --file; history accepts no flags")
	}
	db, _, err := loadWorkspace()
	if err != nil {
		return err
	}
	defer db.Close()
	var result any
	switch action {
	case "observe":
		result, err = controller.ObserveClarification(db, taskID, dispatchID)
	case "history":
		result, err = controller.ClarificationHistory(db)
	default:
		input, openErr := os.Open(file)
		if openErr != nil {
			return openErr
		}
		defer input.Close()
		if action == "request" {
			var request controller.ClarificationRequest
			if err = core.DecodeGraphInput(input, &request); err == nil {
				result, err = controller.RequestClarification(db, request)
			}
		} else {
			var request struct {
				ID     string                         `json:"id"`
				Answer controller.ClarificationAnswer `json:"answer"`
			}
			if err = core.DecodeGraphInput(input, &request); err == nil {
				result, err = controller.AnswerClarification(db, request.ID, request.Answer)
			}
		}
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(result)
}
