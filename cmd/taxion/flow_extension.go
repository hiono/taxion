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

func extendFlowCommand(args []string, output io.Writer) error {
	fs := flag.NewFlagSet("workflow extend", flag.ContinueOnError)
	fs.SetOutput(output)
	file := fs.String("file", "", "JSON live Flow extension, exact prior and proposed limits, actor/source/reason; expectedHash on apply")
	preview := fs.Bool("preview", false, "read-only exact approval preview; no execution stop or model")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage of workflow extend:")
		fs.PrintDefaults()
		fmt.Fprintln(fs.Output(), `JSON: flowId, previousDeadline, deadline (fixed RFC3339),
previousMaxNanoAiu, maxNanoAiu, actor, source (actual approval reference), reason.
Use workflow show/budget for exact current limits. Unchanged nano-AIU values are
required even for time-only extension; external/provider budgets remain zero.
Preview first while the RUNNING Flow and its current stage are still authorized.
Display the hash, old/new deadline and cumulative budget in ONE human decision.
After actual consent add expectedHash and apply the SAME file without --preview.
Live output/usage alone does not invalidate consent. Changed instruction, session,
runner, contract, previous limits or request does. A handoff needs a fresh preview.
The operation does not take runner ownership, cancel, resume, replay validation,
start a session, reset usage or add roles/repairs. The existing runner observes
append-only authority and keeps the SAME session across its former deadline.
Only positive native nano-AIU ceilings can be increased; other units are not
converted. For cumulative external requests/tokens/credits, use workflow budget
--file and obtain any real top-up in the same human decision; provider limits,
account reserves and independent caller deadlines are not overridden.
Expired or formally stopped execution uses recover-review, not this operation.
resource_approval_required events ask the outer operator to obtain consent before
the boundary; pending consent never cancels still-authorized work.`)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" || fs.NArg() != 0 {
		return fmt.Errorf("workflow extend requires --file")
	}
	input, err := os.Open(*file)
	if err != nil {
		return err
	}
	defer input.Close()
	var request controller.FlowExtensionRequest
	if err := core.DecodeGraphInput(input, &request); err != nil {
		return err
	}
	var db *store.Store
	if *preview {
		db, err = store.OpenReadOnly(workspaceDBPath())
	} else {
		if request.ExpectedHash == "" {
			return fmt.Errorf("workflow extend requires the exact preview expectedHash after approval")
		}
		if _, err := os.Stat(workspaceDBPath()); err != nil {
			return err
		}
		db, err = store.Open(workspaceDBPath())
	}
	if err != nil {
		return err
	}
	defer db.Close()
	report, err := controller.ExtendReviewFlow(db, request, *preview)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(report)
}
