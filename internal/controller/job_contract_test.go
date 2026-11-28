package controller

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/auth"
	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/store"
)

func validControllerJobContract(deadline time.Time) domain.JobContract {
	return domain.JobContract{
		Purpose:            "Deliver the approved feature",
		Constraints:        []string{"Preserve authentication boundaries"},
		TargetRepositories: []string{"https://github.com/example/project"},
		Workspaces:         []string{"workspace/main"},
		AcceptanceCriteria: []string{"All required Linux checks pass"},
		RequiredEvidence:   []string{"Linux CI result"},

		ConcurrencyCeiling:      1,
		Deadline:                deadline,
		ProhibitedEffects:       []string{"publish"},
		ApprovalRequiredEffects: []string{"external.delete"},
		ConfigurationSHA256:     strings.Repeat("a", 64),
	}
}

func startContractPlanner(t *testing.T) (*JobContractController, *store.Store, auth.AuthenticatedPrincipal, string, string, domain.ProviderResultObservation, domain.JobContract) {
	return startContractPlannerWithResultContent(t, "")
}

func startContractPlannerWithResultContent(t *testing.T, resultContent string) (*JobContractController, *store.Store, auth.AuthenticatedPrincipal, string, string, domain.ProviderResultObservation, domain.JobContract) {
	t.Helper()
	runtime := &fakePlanningProviderRuntime{}
	providerController, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	_, _, _, _, err := providerController.StartPlanning(
		context.Background(), jobID, grantID,
		"cmd_cccccccccccccccccccccccccccccccc", documents, principal,
	)
	if err != nil {
		t.Fatalf("StartPlanning: %v", err)
	}
	job, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob before Planner Turn: %v", err)
	}
	_, turn, err := database.AddPlanningTurnIntent(context.Background(), jobID, job.Revision,
		domain.RoleExecutionID("cmd_cccccccccccccccccccccccccccccccc"), "session_1",
		"cmd_11111111111111111111111111111111", strings.Repeat("a", 64), principal)
	if err != nil {
		t.Fatalf("AddPlanningTurnIntent: %v", err)
	}
	job, err = database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob before Planner Turn observation: %v", err)
	}
	if _, _, err := database.AddPlanningTurnObservation(context.Background(), jobID, job.Revision,
		turn.ID, "cmd_22222222222222222222222222222222", domain.PlanningTurnCompleted,
		strings.Repeat("b", 64), false, principal); err != nil {
		t.Fatalf("AddPlanningTurnObservation: %v", err)
	}
	job, err = database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob before Planner Result: %v", err)
	}
	contract := validControllerJobContract(time.Now().UTC().Add(2 * time.Hour))
	content := resultContent
	if content == "" {
		contentBytes, err := json.Marshal(contract)
		if err != nil {
			t.Fatalf("Marshal Planner JobContract: %v", err)
		}
		content = string(contentBytes)
	}
	_, sourceResult, err := database.AddProviderResultObservation(context.Background(), jobID, job.Revision,
		domain.RoleExecutionID("cmd_cccccccccccccccccccccccccccccccc"), turn.OperationID, turn.ID,
		"session_1", content, sha256String(content), true, strings.Repeat("c", 64), principal)
	if err != nil {
		t.Fatalf("AddProviderResultObservation: %v", err)
	}
	contractController, err := NewJobContractController(database)
	if err != nil {
		t.Fatalf("NewJobContractController: %v", err)
	}
	return contractController, database, principal, jobID, grantID, sourceResult, contract
}

func TestJobContractControllerProposesForAuthorizedPlanner(t *testing.T) {
	controller, database, principal, jobID, _, sourceResult, contract := startContractPlanner(t)
	ctx := context.Background()
	commandID := "cmd_dddddddddddddddddddddddddddddddd"
	roleExecutionID := domain.RoleExecutionID("cmd_cccccccccccccccccccccccccccccccc")
	if _, _, err := controller.Propose(ctx, jobID, roleExecutionID,
		"provider_result_missing", "cmd_91919191919191919191919191919191", principal); err == nil {
		t.Fatal("Controller accepted an unknown source Result")
	}
	before, err := database.GetJob(ctx, jobID)
	if err != nil || len(before.JobContractRevisions) != 0 {
		t.Fatalf("rejected Result provenance changed Job: %#v %v", before.JobContractRevisions, err)
	}
	receipt, revision, err := controller.Propose(
		ctx, jobID, domain.RoleExecutionID("cmd_cccccccccccccccccccccccccccccccc"),
		sourceResult.ID, commandID, principal,
	)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	job, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	_, expectedHash, err := domain.CanonicalizeJobContract(contract, domain.JobContractCanonicalizationVersion)
	if err != nil {
		t.Fatalf("Canonicalize expected Planner JobContract: %v", err)
	}
	if revision.CanonicalHash != expectedHash {
		t.Fatalf("proposal contract was not derived from the final Result: got hash %s want %s", revision.CanonicalHash, expectedHash)
	}
	if receipt.Revision != job.Revision || revision.Revision != 1 ||
		job.State != domain.JobDraft || len(job.JobContractRevisions) != 1 ||
		len(job.JobApprovals) != 0 || job.Goal != revision.Contract.Purpose {
		t.Fatalf("unexpected proposal state: receipt=%#v revision=%#v job=%#v", receipt, revision, job)
	}
}

func TestJobContractControllerRejectsPrincipalMismatchAndExpiredGrant(t *testing.T) {
	controller, database, principal, jobID, grantID, sourceResult, _ := startContractPlanner(t)
	ctx := context.Background()
	otherPrincipal := newProviderTestPrincipal(t)
	if _, _, err := controller.Propose(
		ctx, jobID, domain.RoleExecutionID("cmd_cccccccccccccccccccccccccccccccc"),
		sourceResult.ID, "cmd_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", otherPrincipal,
	); err == nil {
		t.Fatal("proposal with a different HUMAN Principal was accepted")
	}
	job, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	var grant domain.PlanningAuthorization
	for _, candidate := range job.PlanningAuthorizations {
		if candidate.ID == grantID {
			grant = candidate
			break
		}
	}
	if grant.ID == "" || grant.PrincipalID != principal.PrincipalID() {
		t.Fatalf("expected PlanningAuthorization was not found: %#v", grant)
	}
	controller.now = func() time.Time { return grant.Deadline.Add(time.Second) }
	if _, _, err := controller.Propose(
		ctx, jobID, domain.RoleExecutionID("cmd_cccccccccccccccccccccccccccccccc"),
		sourceResult.ID, "cmd_ffffffffffffffffffffffffffffffff", principal,
	); err == nil {
		t.Fatal("proposal after PlanningAuthorization deadline was accepted")
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil || len(job.JobContractRevisions) != 0 {
		t.Fatalf("rejected proposal changed Job: revisions=%d err=%v", len(job.JobContractRevisions), err)
	}
}

func TestJobContractControllerAllowsExactReplayAfterGrantExpiry(t *testing.T) {
	controller, database, principal, jobID, grantID, sourceResult, _ := startContractPlanner(t)
	ctx := context.Background()
	job, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	var grant domain.PlanningAuthorization
	for _, candidate := range job.PlanningAuthorizations {
		if candidate.ID == grantID {
			grant = candidate
			break
		}
	}
	commandID := "cmd_12121212121212121212121212121212"
	roleExecutionID := domain.RoleExecutionID("cmd_cccccccccccccccccccccccccccccccc")
	firstReceipt, firstRevision, err := controller.Propose(ctx, jobID, roleExecutionID, sourceResult.ID, commandID, principal)
	if err != nil {
		t.Fatalf("initial Propose: %v", err)
	}
	controller.now = func() time.Time { return grant.Deadline.Add(time.Second) }
	retryReceipt, retryRevision, err := controller.Propose(ctx, jobID, roleExecutionID, sourceResult.ID, commandID, principal)
	if err != nil {
		t.Fatalf("idempotent replay after expiry: %v", err)
	}
	if retryReceipt.CommandID != firstReceipt.CommandID ||
		retryReceipt.Revision != firstReceipt.Revision ||
		retryRevision.ID != firstRevision.ID ||
		retryRevision.CanonicalHash != firstRevision.CanonicalHash {
		t.Fatalf("retry did not return original proposal: first=%#v/%#v retry=%#v/%#v",
			firstReceipt, firstRevision, retryReceipt, retryRevision)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil || len(job.JobContractRevisions) != 1 {
		t.Fatalf("idempotent replay appended another revision: revisions=%d err=%v", len(job.JobContractRevisions), err)
	}
}

func TestJobContractControllerRejectsNewProposalAfterApproval(t *testing.T) {
	controller, database, principal, jobID, _, sourceResult, _ := startContractPlanner(t)
	ctx := context.Background()
	roleExecutionID := domain.RoleExecutionID("cmd_cccccccccccccccccccccccccccccccc")
	_, revision, err := controller.Propose(
		ctx, jobID, roleExecutionID, sourceResult.ID, "cmd_23232323232323232323232323232323", principal,
	)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	job, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if _, _, err := database.AddJobApproval(
		ctx, jobID, job.Revision, "cmd_24242424242424242424242424242424",
		revision.Revision, revision.CanonicalHash, revision.CanonicalizationVersion,
		"Approved the exact proposal", principal,
	); err != nil {
		t.Fatalf("AddJobApproval: %v", err)
	}
	if _, _, err := controller.Propose(
		ctx, jobID, roleExecutionID, sourceResult.ID, "cmd_25252525252525252525252525252525", principal,
	); err == nil {
		t.Fatal("new proposal through the DRAFT Planner authority was accepted after approval")
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil || job.State != domain.JobActive || len(job.JobContractRevisions) != 1 || len(job.JobApprovals) != 1 {
		t.Fatalf("rejected ACTIVE amendment changed Job: %#v err=%v", job, err)
	}
}

func TestJobContractControllerRejectsCommandIDReuseByDifferentPlanner(t *testing.T) {
	controller, database, principal, jobID, _, sourceResult, _ := startContractPlanner(t)
	ctx := context.Background()
	commandID := "cmd_34343434343434343434343434343434"
	roleExecutionID := domain.RoleExecutionID("cmd_cccccccccccccccccccccccccccccccc")
	if _, _, err := controller.Propose(ctx, jobID, roleExecutionID, sourceResult.ID, commandID, principal); err != nil {
		t.Fatalf("initial Propose: %v", err)
	}
	if _, _, err := controller.Propose(
		ctx, jobID, "role_exec_00000000000000000000000000000000",
		sourceResult.ID, commandID, principal,
	); err == nil {
		t.Fatal("same CommandID with a different proposer was accepted")
	}
	job, err := database.GetJob(ctx, jobID)
	if err != nil || len(job.JobContractRevisions) != 1 {
		t.Fatalf("CommandID conflict changed Job: revisions=%d err=%v", len(job.JobContractRevisions), err)
	}
}

func TestJobContractControllerApprovesExactRevisionAndReplaysAfterActivation(t *testing.T) {
	controller, database, principal, jobID, _, sourceResult, _ := startContractPlanner(t)
	ctx := context.Background()
	_, revision, err := controller.Propose(
		ctx, jobID, domain.RoleExecutionID("cmd_cccccccccccccccccccccccccccccccc"),
		sourceResult.ID, "cmd_45454545454545454545454545454545", principal,
	)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	approvalCommandID := "cmd_56565656565656565656565656565656"
	firstReceipt, firstApproval, err := controller.Approve(
		ctx, jobID, approvalCommandID, revision.Revision,
		revision.CanonicalHash, revision.CanonicalizationVersion,
		"Reviewed and approved the exact bounded scope", principal,
	)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	job, err := database.GetJob(ctx, jobID)
	if err != nil || job.State != domain.JobActive || len(job.JobApprovals) != 1 ||
		firstApproval.ContractRevision != revision.Revision ||
		firstApproval.CanonicalHash != revision.CanonicalHash ||
		firstApproval.ApproverPrincipalID != principal.PrincipalID() {
		t.Fatalf("approval did not activate exact revision: job=%#v approval=%#v err=%v", job, firstApproval, err)
	}
	retryReceipt, retryApproval, err := controller.Approve(
		ctx, jobID, approvalCommandID, revision.Revision,
		revision.CanonicalHash, revision.CanonicalizationVersion,
		"Reviewed and approved the exact bounded scope", principal,
	)
	if err != nil {
		t.Fatalf("exact approval replay: %v", err)
	}
	if retryReceipt.CommandID != firstReceipt.CommandID ||
		retryReceipt.Revision != firstReceipt.Revision ||
		retryApproval.ID != firstApproval.ID {
		t.Fatalf("approval replay changed receipt or fact: first=%#v/%#v retry=%#v/%#v",
			firstReceipt, firstApproval, retryReceipt, retryApproval)
	}
	if _, _, err := controller.Approve(
		ctx, jobID, "cmd_67676767676767676767676767676767",
		revision.Revision, strings.Repeat("0", 64),
		revision.CanonicalizationVersion, "Wrong hash", principal,
	); err == nil {
		t.Fatal("approval with a different canonical hash was accepted")
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil || job.State != domain.JobActive || len(job.JobApprovals) != 1 {
		t.Fatalf("rejected approval changed Job: %#v err=%v", job, err)
	}
}

func TestDecodeJobContractJSONStrict(t *testing.T) {
	expected := validControllerJobContract(time.Now().UTC().Add(2 * time.Hour))
	encoded, err := json.Marshal(expected)
	if err != nil {
		t.Fatalf("Marshal JobContract: %v", err)
	}
	valid := string(encoded)
	decoded, err := decodeJobContractJSON(valid)
	if err != nil {
		t.Fatalf("decode valid Planner Result: %v", err)
	}
	if decoded.Purpose != expected.Purpose ||
		!decoded.Deadline.Equal(expected.Deadline) ||
		len(decoded.TargetRepositories) != len(expected.TargetRepositories) ||
		decoded.TargetRepositories[0] != expected.TargetRepositories[0] {
		t.Fatalf("decoded JobContract differs: got=%#v want=%#v", decoded, expected)
	}

	cases := []struct {
		name    string
		content string
	}{
		{name: "unknown root key", content: strings.Replace(valid, "{", "{\"unknown\":true,", 1)},
		{name: "non-canonical key casing", content: strings.Replace(valid, "\"purpose\"", "\"Purpose\"", 1)},
		{name: "duplicate root key", content: strings.Replace(valid, "{", "{\"purpose\":\"duplicate\",", 1)},
		{name: "obsolete budget", content: strings.Replace(valid, "{", "{\"budget\":{\"max_tokens\":5},", 1)},
		{name: "trailing JSON value", content: valid + " {}"},
		{name: "fenced Markdown", content: "```json\n" + valid + "\n```"},
		{name: "non-object root", content: "[]"},
		{name: "malformed JSON", content: "{\"purpose\":"},
		{name: "type mismatch", content: strings.Replace(valid, "\"concurrency_ceiling\":1", "\"concurrency_ceiling\":\"one\"", 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decodeJobContractJSON(tc.content); err == nil {
				t.Fatal("invalid Planner Result JSON was accepted")
			}
		})
	}
}

func TestJobContractControllerRejectsMalformedFinalResultWithoutMutation(t *testing.T) {
	controller, database, principal, jobID, _, sourceResult, _ := startContractPlannerWithResultContent(t, "not JSON")
	_, _, err := controller.Propose(
		context.Background(), jobID, domain.RoleExecutionID("cmd_cccccccccccccccccccccccccccccccc"),
		sourceResult.ID, "cmd_98989898989898989898989898989898", principal,
	)
	if err == nil {
		t.Fatal("JobContractController accepted malformed Planner Result JSON")
	}
	job, readErr := database.GetJob(context.Background(), jobID)
	if readErr != nil || len(job.JobContractRevisions) != 0 {
		t.Fatalf("malformed Planner Result changed Job: revisions=%d err=%v", len(job.JobContractRevisions), readErr)
	}
}

func TestJobContractControllerAmendsActiveRevisionAndRequiresReapproval(t *testing.T) {
	ctx := context.Background()
	controller, database, principal, jobID, _, sourceResult, contract := startContractPlanner(t)
	_, proposed, err := controller.Propose(
		ctx, jobID, domain.RoleExecutionID("cmd_cccccccccccccccccccccccccccccccc"),
		sourceResult.ID, "cmd_71717171717171717171717171717171", principal,
	)
	if err != nil {
		t.Fatalf("ProposeJobContract: %v", err)
	}
	job, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before approval: %v", err)
	}
	if _, _, err := controller.Approve(
		ctx, jobID, "cmd_72727272727272727272727272727272",
		proposed.Revision, proposed.CanonicalHash, proposed.CanonicalizationVersion,
		"Approve initial contract", principal,
	); err != nil {
		t.Fatalf("Approve initial contract: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil || job.State != domain.JobActive {
		t.Fatalf("Job did not become ACTIVE: state=%s err=%v", job.State, err)
	}
	amendPrincipal := newProviderTestPrincipal(t)

	amendedContract := contract
	amendedContract.Purpose = "Deliver the explicitly amended feature"
	amendedContract.ConcurrencyCeiling++
	amendedContract.Deadline = time.Now().UTC().Add(3 * time.Hour)
	amendmentCommand := "cmd_73737373737373737373737373737373"
	reason := "Human-approved change to the target scope"
	receipt, amendedRevision, err := controller.Amend(
		ctx, jobID, job.Revision, amendmentCommand, proposed.Revision, proposed.CanonicalHash,
		reason, amendedContract, amendPrincipal,
	)
	if err != nil {
		t.Fatalf("Amend: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil || job.State != domain.JobDraft || len(job.JobContractRevisions) != 2 ||
		len(job.JobApprovals) != 1 || job.Goal != amendedContract.Purpose {
		t.Fatalf("amendment did not return Job to DRAFT: state=%s revisions=%d approvals=%d err=%v",
			job.State, len(job.JobContractRevisions), len(job.JobApprovals), err)
	}
	if amendedRevision.Origin != domain.JobContractRevisionOriginHumanAmendment ||
		amendedRevision.SupersedesRevision != proposed.Revision ||
		amendedRevision.SupersedesCanonicalHash != proposed.CanonicalHash ||
		amendedRevision.AmendedByPrincipalID != amendPrincipal.PrincipalID() ||
		amendedRevision.AmendmentReason != reason {
		t.Fatalf("human amendment provenance mismatch: %#v", amendedRevision)
	}
	if _, _, err := controller.Amend(
		ctx, jobID, job.Revision, "cmd_74747474747474747474747474747474",
		amendedRevision.Revision, amendedRevision.CanonicalHash, reason, contract, principal,
	); err == nil {
		t.Fatal("DRAFT Job accepted a second amendment command")
	}
	if _, _, err := controller.Amend(
		ctx, jobID, job.Revision, "cmd_75757575757575757575757575757575",
		amendedRevision.Revision, amendedRevision.CanonicalHash, reason, amendedContract, auth.AuthenticatedPrincipal{},
	); err == nil {
		t.Fatal("Controller accepted an unauthenticated amendment Principal")
	}
	if _, _, err := controller.Approve(
		ctx, jobID, "cmd_76767676767676767676767676767676",
		amendedRevision.Revision, amendedRevision.CanonicalHash, amendedRevision.CanonicalizationVersion,
		"Approve amended contract", amendPrincipal,
	); err != nil {
		t.Fatalf("Approve amended contract: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil || job.State != domain.JobActive || len(job.JobApprovals) != 2 {
		t.Fatalf("amended revision was not reapproved: state=%s approvals=%d err=%v", job.State, len(job.JobApprovals), err)
	}
	retryReceipt, retryRevision, err := controller.Amend(
		ctx, jobID, job.Revision, amendmentCommand, proposed.Revision, proposed.CanonicalHash,
		reason, amendedContract, amendPrincipal,
	)
	if err != nil || retryReceipt.CommandID != receipt.CommandID ||
		retryReceipt.Revision != receipt.Revision || retryRevision.ID != amendedRevision.ID {
		t.Fatalf("amendment idempotency: %#v %#v %v", retryReceipt, retryRevision, err)
	}
	if _, _, err := controller.Amend(
		ctx, jobID, job.Revision, amendmentCommand, proposed.Revision, proposed.CanonicalHash,
		"Changed reason", amendedContract, amendPrincipal,
	); err != store.ErrIdempotencyConflict {
		t.Fatalf("changed amendment request returned %v, want ErrIdempotencyConflict", err)
	}
	if len(job.ProviderStartIntents) != 1 {
		t.Fatalf("amendment created a Provider operation: %d", len(job.ProviderStartIntents))
	}
}
