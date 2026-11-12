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

const normalReviewerRecoveryHelp = `
Normal Coder Flow recovery (not artifactSource):
The latest Reviewer must be ISSUED, unacknowledged, with no execution start,
session, runtime configuration, usage, result or runtime receipt. The held Flow
must be STOPPED, unclaimed, with its exact
accepted SUCCEEDED Coder and unchanged contract/candidate. Missing records prove
neither non-delivery nor zero usage. Check real process quiescence separately.
An absent managed worktree is allowed only if its path is absent; a recorded
prepared worktree must pass registration, repository, detached HEAD, unchanged
index and complete target/extra/ignored-file checks. Unknown preparation blocks.

Complete reconciliation JSON (replace placeholders with observed evidence):
{
  "flowId": "OLD_FLOW_ID",
  "dispatchId": "OLD_REVIEWER_DISPATCH_ID",
  "actor": "human",
  "source": "human:ACTUAL_APPROVAL_REFERENCE",
  "reason": "Cancel the issued Reviewer while retaining accepted Coder output and unknown delivery",
  "evidence": ["ACTUAL_NON_SECRET_PROCESS_QUIESCENCE_EVIDENCE"],
  "noActiveExecutionConfirmed": true,
  "acceptDeliveryUncertainty": true
}
Use --preview; verify inputOrigin=accepted-coder-output, inputTreeSha=the full
accepted tree T, preservedReviewWorktree and transitions. Obtain actual approval
of hash; add expectedHash with that exact value and apply without --preview.
State drift requires a new preview, not editing an old instruction.
Expected: Reviewer Dispatch CANCELLED, Reviewer Task BLOCKED, parent remains
WAITING_FOR_REVIEW; old Flow/HOLD, Coder SUCCEEDED, all receipts, usage and failures
remain. No model or validation executes; delivery/usage remain unknown.

Preserve A (original Coder baseline), B (accepted output commit), T (full tree)
and ALL Controller-derived changed paths, not only reportedChangedFiles.
Keep T in a distinct same-repository worktree at B. Use conversation intake
with task.supersedes=OLD_TASK_ID, task.createdFrom=flow:OLD_FLOW_ID,
task.baselineSha=A, targetSha=B, targetTreeSha=T, targetChangedFiles=ALL_PATHS
and reconciliationHash from the applied preview. Do not copy approval decisions.
Read conversation intake/confirm --help for complete contract/configuration inputs.
Approve the exact proposal, then confirm with replacesFlow=OLD_FLOW_ID,
reconciliationHash and the COMPLETE CANCELLED target from workflow feedback-target.
Approve additionalTaskIds=["NEW_FLOW_ID-review-0"] only for the actual review.

Complete confirmation JSON (replace the full selection and target with observed
values; include the approved billing/budgetGuidance/fileAccess when present):
{
  "taskId": "NEW_READY_TASK_ID",
  "expectedHash": "EXACT_PROPOSAL_HASH",
  "actor": "human",
  "source": "human:ACTUAL_APPROVAL_REFERENCE",
  "reason": "Approve the exact retained candidate, review-only successor and actual configuration",
  "configurationDecisionId": "APPROVAL_REF@task:NEW_READY_TASK_ID",
  "additionalTaskIds": ["NEW_FLOW_ID-review-0"],
  "replacesFlow": "OLD_FLOW_ID",
  "reconciliationHash": "EXACT_APPLIED_RECONCILIATION_HASH",
  "target": {
    "dispatchId": "OLD_REVIEWER_DISPATCH_ID",
    "taskId": "OLD_REVIEWER_TASK_ID",
    "repository": "OWNER/REPO",
    "recipient": "REVIEWER",
    "instructionHash": "EXACT_CANCELLED_INSTRUCTION_HASH",
    "status": "CANCELLED",
    "updatedAt": "2099-01-01T00:00:00Z",
    "targetSha": "EXACT_TARGET_COMMIT_B",
    "outputSha": "",
    "resultHash": "EXACT_CANCELLED_RESULT_HASH"
  },
  "selection": {
    "version": 1,
    "project": {"id": "PROJECT_ID"},
    "repository": {"provider": "github", "host": "github.com", "path": "OWNER/REPO", "account": "SCM_ACCOUNT", "credentialRef": "SCM_CREDENTIAL"},
    "runtime": {"provider": "copilot", "account": "RUNTIME_ACCOUNT", "credentialRef": "RUNTIME_CREDENTIAL", "contractRef": "CONTRACT_REF"},
    "execution": {"requester": "REQUESTER", "worker": "WORKER", "approvalRef": "APPROVAL_REF"}
  }
}
No placeholder is approval. Use the actual feedback-target timestamp/hash;
config check --sources supplies the complete configuration, not credential values.
confirm makes the successor READY, without starting a model or Dispatch.

Complete plan/start JSON for the confirmed successor:
{
  "id": "NEW_FLOW_ID",
  "taskId": "NEW_READY_TASK_ID",
  "repository": "OWNER/REPO",
  "reviewRoot": "/absolute/fresh/review-root",
  "continueFromDispatchId": "ACCEPTED_CODER_DISPATCH_ID",
  "maxRepairs": 0,
  "timeoutSeconds": 3600,
  "deadline": "2099-01-01T01:00:00Z",
  "stageBudget": {
    "version": 1,
    "coderSessionCredits": 0,
    "validationSeconds": 600,
    "reviewSeconds": 1800
  },
  "continuationChange": "Actual approval of a fixed future review window; no revival of the old Flow",
  "savedOutputDecision": "Preserve accepted Coder output, full target, validation receipts, failures and cumulative usage",
  "authorization": {
    "id": "NEW_REVIEW_AUTHORIZATION_ID",
    "source": "human:ACTUAL_APPROVAL_REFERENCE",
    "text": "Authorize the exact same-target Reviewer-only plan, fixed deadline and displayed ceilings"
  }
}
The example date/durations are NOT approval. Set the actually approved future
deadline and estimates. timeoutSeconds can only shorten that fixed deadline.
Omitted maxNanoAiu requires the approved external billing contract; a positive
native ceiling also needs explicitly approved reviewNanoAiu reservation.
workflow plan is read-only. Approve its exact stagePlan.hash, add stagePlanHash
and workflow start with the unchanged input. Expired deadlines reject; obtain a
new explicit future successor deadline, never alter/revive the expired Flow.
Expected: one new REVIEWER, zero Coders/repairs, same A/B/T/all paths, fresh
managed worktree. OMIT artifactSource/reuseArtifactValidation; do not validate-input.
workflow run --id NEW_FLOW_ID --builtin-prompts --controller-validation-submission
performs normal preflight and Reviewer result-acceptance validation. It does NOT
replay Coder checks or failed/interrupted/unknown commands to recover their output.
Stop on active ownership/execution, receipts suggesting delivery, target drift,
unrecorded preparation, unresolved feedback, unknown required usage or insufficient
budget/deadline/authorization. Reconcile does not authorize the successor model.
`

const firstCoderRecoveryHelp = `
First Coder cancellation-only recovery:
A stopped, held, unclaimed Flow with its first untouched ISSUED Coder can use
the same reconciliation JSON, with the Coder dispatchId. Stop codes do not form
an authorization allowlist; invalid schemas and unsafe state still fail.
The approved worktree-tree-snapshot input may be baseline-only (empty target
fields) or an explicit retained snapshot. Preserve the exact input and contract.
Require human execution-quiescence evidence, unknown-delivery acceptance and
the exact preview hash. Missing execution/usage records do not prove zero usage.
Cancellation remains available after the old deadline: it does not resume or
extend the Flow, issue a model call, or authorize a successor. Preview reports
inputOrigin=approved-predecessor-input-contract; inputTreeSha may be empty for
baseline-only input. Apply atomically cancels Dispatch, blocks Task and appends
an audit, preserving Flow, authorization, ceilings, deadline and unknown usage.
A successor requires distinct-worktree input verification and a separately
approved contract, future deadline, budget and execution plan.
`

func reconcileStoppedDispatchCommand(args []string, output io.Writer) error {
	fs := flag.NewFlagSet("workflow reconcile-stopped", flag.ContinueOnError)
	fs.SetOutput(output)
	file := fs.String("file", "", "JSON human decision, quiescence evidence, unknown-delivery acceptance and expectedHash")
	preview := fs.Bool("preview", false, "inspect an eligible stopped Coder or unstarted artifact/normal Reviewer; verify and preserve any prepared managed review worktree; no workspace changes")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage of workflow reconcile-stopped:")
		fs.PrintDefaults()
		fmt.Fprint(fs.Output(), firstCoderRecoveryHelp)
		fmt.Fprint(fs.Output(), normalReviewerRecoveryHelp)
	}
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
