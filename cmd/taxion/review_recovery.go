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

func recoverArtifactReviewCommand(args []string, output io.Writer) error {
	fs := flag.NewFlagSet("workflow recover-review", flag.ContinueOnError)
	fs.SetOutput(output)
	file := fs.String("file", "", "JSON: reconciliation decision/evidence, new taskId/flowId/reviewRoot, absolute deadline, timeoutSeconds, maxNanoAiu, stageBudget; expectedHash on apply")
	preview := fs.Bool("preview", false, "preview reconciliation, identical-candidate contract and Reviewer-only plan as one approval; no store writes or model execution")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage of workflow recover-review:")
		fs.PrintDefaults()
		fmt.Fprintln(fs.Output(), `JSON requires reconciliation:{flowId,dispatchId,actor,source,reason,evidence,
noActiveExecutionConfirmed:true,acceptDeliveryUncertainty:true}, taskId, flowId,
reviewRoot (fresh absolute root), deadline (fixed RFC3339), timeoutSeconds,
maxNanoAiu and stageBudget:{version:1,...}. No Coder/repair allocation.
This combined operation is artifactSource-only. For a normal Flow with an accepted
Coder and an unstarted Reviewer, use workflow reconcile-stopped --help and the
existing continueFromDispatchId successor. A rejection lists confirmed failed
saved-state conditions separately from Git/runtime/budget checks not evaluated.
Do not infer which remaining predicates passed from a composite rejection.
Preview returns one outer hash binding all saved state and the exact bounded request.
After actual approval add expectedHash and apply the same file without --preview.
Controller derives lower hashes, preserves existing audits and commits atomically.
No model or host validation starts; use normal validate-input/run after apply.
Already-approved Flows continue normally, without returning to this operation.
For an EXECUTED artifact Reviewer formally stopped with a CANCELLED Dispatch, set
continueStoppedExecution:true. Requires exact saved controller runtime-stop receipt,
unchanged candidate and prepared review worktree, no active execution/owner or
unresolved feedback and explicit uncertainty acceptance. By default deadline must
be <= the unexpired original Flow deadline. This does not resume a terminal session: it derives a Reviewer-only
successor, reuses the eligible PASSED/diagnostic OBSERVED input receipt without
relabeling it, and carries bounded saved readable output
into its actual instruction. Old Flow, session, usage, transcript and worktree remain.
Missing saved output is stated as unknown, not invented analysis. No Coder, input
validation replay, cumulative usage reset or automatic deadline extension is authorized.
For an expired original deadline or an explicitly requested later window, include:
"deadlineRecontract":{"previousDeadline":"EXACT_SAVED_RFC3339",
"deadline":"SAME_AS_REQUEST_DEADLINE","reason":"EXPLICIT_CHANGE_REASON"}
The new deadline must be later than the old one and still future at apply.
This optional object works for unstarted and formally stopped executed artifact
Reviewers. One outer hash binds both deadlines, reason, candidate, receipt and plan.
Preview first, obtain one approval of that exact material deadline change and apply;
do not ask separately for derived hashes. No old Flow or usage is rewritten.
Use supported feedback resolution/recovery for an outstanding hold/runner; never
edit the database or use unstarted reconcile-stopped for an executed Reviewer.`)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" || fs.NArg() != 0 {
		return fmt.Errorf("workflow recover-review requires --file and no positional arguments")
	}
	input, err := os.Open(*file)
	if err != nil {
		return err
	}
	defer input.Close()
	var request controller.ReviewRecoveryRequest
	if err := core.DecodeGraphInput(input, &request); err != nil {
		return err
	}
	if !*preview && request.ExpectedHash == "" {
		return fmt.Errorf("workflow recover-review requires the single expectedHash from --preview")
	}
	info, err := os.Stat(workspaceDBPath())
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("workflow recover-review requires an existing workspace")
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
	report, err := controller.RecoverArtifactReview(db, storePath, request, *preview)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}
