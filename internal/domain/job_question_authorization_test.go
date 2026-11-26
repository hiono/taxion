package domain

import (
	"strings"
	"testing"
	"time"
)

func TestJobQuestionAuthorizationBindsOneApprovedQuestionAndPlannerSession(t *testing.T) {
	pinnedRepository := "https://example.com/team/repo.git@" + strings.Repeat("a", 40)
	if !repositoryInJobContract(JobContract{TargetRepositories: []string{pinnedRepository}}, pinnedRepository) {
		t.Fatal("exact JobContract membership rejected an approved commit-pinned repository target")
	}
	job, task, now := approvedJobWithTaskForExecutionPermission(t)
	contract := job.JobContractRevisions[len(job.JobContractRevisions)-1].Contract
	contract.TargetRepositories = append(contract.TargetRepositories, pinnedRepository)
	canonicalContract, canonicalHash, err := CanonicalizeJobContract(
		contract, job.JobContractRevisions[len(job.JobContractRevisions)-1].CanonicalizationVersion,
	)
	if err != nil {
		t.Fatalf("CanonicalizeJobContract with commit-pinned target: %v", err)
	}
	latestRevision := len(job.JobContractRevisions) - 1
	job.JobContractRevisions[latestRevision].Contract = canonicalContract
	job.JobContractRevisions[latestRevision].CanonicalHash = canonicalHash
	job.JobApprovals[len(job.JobApprovals)-1].CanonicalHash = canonicalHash
	task.CanonicalHash = canonicalHash
	task.TargetRepositories = append([]string(nil), canonicalContract.TargetRepositories...)
	for i := range job.Tasks {
		if job.Tasks[i].ID == task.ID {
			job.Tasks[i] = task
			break
		}
	}
	contract = canonicalContract
	if err := job.Validate(); err != nil {
		t.Fatalf("Job.Validate with commit-pinned approved target: %v", err)
	}
	permissionRequest := executionPermissionTestRequest(job, now)
	permissionSHA, err := ExecutionPermissionRequestSHA256(permissionRequest)
	if err != nil {
		t.Fatalf("ExecutionPermissionRequestSHA256: %v", err)
	}
	permission, err := NewExecutionPermission(
		job, permissionRequest, permissionSHA,
		executionPermissionTestAssessment(permissionRequest, permissionSHA, now), now,
	)
	if err != nil {
		t.Fatalf("NewExecutionPermission: %v", err)
	}
	job.ExecutionPermissions = append(job.ExecutionPermissions, permission)
	job.Revision++
	job.UpdatedAt = now

	start, err := NewTaskRoleExecutionStart(
		job, task.ID, permission.ID, "cmd_71717171717171717171717171717171",
		permission.RequestSHA256, now.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("NewTaskRoleExecutionStart: %v", err)
	}
	job.RoleExecutions = append(job.RoleExecutions, start.RoleExecution)
	job.ProviderStartIntents = append(job.ProviderStartIntents, start.ProviderStartIntent)
	job.Revision++
	job.UpdatedAt = start.RoleExecution.CreatedAt

	var planner RoleExecution
	for _, execution := range job.RoleExecutions {
		if execution.Role == RolePlanner {
			planner = execution
			break
		}
	}
	if planner.ID == "" {
		t.Fatal("approved Job fixture has no Planner RoleExecution")
	}
	message, err := NewJobMessage(job, "cmd_72727272727272727272727272727272", JobMessageRequest{
		Sender:    ActorRef{Kind: ActorCoder, ID: start.RoleExecution.ID},
		Recipient: ActorRef{Kind: ActorPlanner, ID: planner.ID},
		Body:      "Please clarify the approved implementation constraint.",
	}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("NewJobMessage: %v", err)
	}
	job.JobMessages = append(job.JobMessages, message)
	job.Revision++
	job.UpdatedAt = message.CreatedAt

	var planningGrant PlanningAuthorization
	for _, grant := range job.PlanningAuthorizations {
		if grant.ID == planner.PlanningAuthorizationID {
			planningGrant = grant
			break
		}
	}
	if planningGrant.ID == "" {
		t.Fatal("Planner PlanningAuthorization was not found")
	}
	startObservation, found := latestProviderStartObservation(job, planner.ProviderStartOperationID)
	if !found || startObservation.Status != ProviderStartAccepted {
		t.Fatal("Planner does not have an accepted Provider Session")
	}
	authorizationTime := now.Add(3 * time.Minute)
	request := JobQuestionAuthorizationRequest{
		JobID: job.ID, CommandID: "cmd_73737373737373737373737373737373",
		QuestionMessageID: message.ID, PlannerRoleExecutionID: planner.ID,
		ProviderSessionID: startObservation.ProviderSessionID,
		RepositoryID:      pinnedRepository, PlanningRepositoryID: planningGrant.RepositoryID,
		ProviderID: planningGrant.ProviderID, AccountID: planningGrant.AccountID,
		ConfigurationSHA256: planningGrant.ConfigurationSHA256,
		ReadOnlyBoundary: PlanningReadOnlyBoundary{
			Mode: PlanningReadOnlyBoundarySnapshot, Reference: "question_snapshot_1", SHA256: strings.Repeat("d", 64),
		},
		Budget:   PlanningBudget{MaxTurns: 2, MaxTokens: 100},
		Deadline: contract.Deadline.Add(-time.Minute),
	}
	authorization, err := NewJobQuestionAuthorization(job, request, authorizationTime)
	if err != nil {
		t.Fatalf("NewJobQuestionAuthorization: %v", err)
	}
	if authorization.JobApprovalID != job.JobApprovals[len(job.JobApprovals)-1].ID ||
		authorization.QuestionMessageID != message.ID ||
		authorization.PlannerRoleExecutionID != planner.ID ||
		authorization.ProviderSessionID != startObservation.ProviderSessionID {
		t.Fatalf("authorization lost its exact authority or question binding: %#v", authorization)
	}
	if err := authorization.ValidateFor(
		job, message.ID, planner.ID, startObservation.ProviderSessionID, authorizationTime.Add(time.Second),
	); err != nil {
		t.Fatalf("ValidateFor exact question and Session: %v", err)
	}
	if err := authorization.ValidateFor(
		job, message.ID, planner.ID, "session_foreign", authorizationTime.Add(time.Second),
	); err == nil {
		t.Fatal("JobQuestionAuthorization authorized a different Provider Session")
	}

	job.JobQuestionAuthorizations = append(job.JobQuestionAuthorizations, authorization)
	job.Revision++
	job.UpdatedAt = authorization.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("Job.Validate with JobQuestionAuthorization: %v", err)
	}

	turnCreatedAt := authorization.CreatedAt.Add(time.Second)
	turn, err := NewJobQuestionTurnIntent(job, JobQuestionTurnIntentRequest{
		JobQuestionAuthorizationID: authorization.ID,
		CommandID:                  "cmd_74747474747474747474747474747474",
		PromptSHA256:               sha256HexDomain("Please provide the approved clarification."),
		ReservedTokens:             50,
	}, turnCreatedAt)
	if err != nil {
		t.Fatalf("NewJobQuestionTurnIntent: %v", err)
	}
	job.JobQuestionTurnIntents = append(job.JobQuestionTurnIntents, turn)
	job.Revision++
	job.UpdatedAt = turn.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("Job.Validate with pending JobQuestionTurnIntent: %v", err)
	}
	for _, invalidContent := range []string{" \t\n", strings.Repeat("x", MaxJobMessageBodyBytes+1)} {
		if _, err := NewJobQuestionTurnObservation(job, JobQuestionTurnObservationRequest{
			IntentID: turn.ID, CommandID: "cmd_74747474747474747474747474747475",
			Status: JobQuestionTurnCompleted, TokensUsed: 1,
			Content: invalidContent, ReceiptID: "receipt_invalid_output",
			EvidenceSHA256: strings.Repeat("f", 64),
		}, turnCreatedAt.Add(500*time.Millisecond)); err == nil {
			t.Fatal("JobQuestionTurnObservation accepted completed output that cannot be delivered as a JobMessage")
		}
	}
	if _, err := NewJobQuestionTurnIntent(job, JobQuestionTurnIntentRequest{
		JobQuestionAuthorizationID: authorization.ID,
		CommandID:                  "cmd_75757575757575757575757575757575",
		PromptSHA256:               sha256HexDomain("Duplicate turn"),
		ReservedTokens:             10,
	}, turnCreatedAt.Add(time.Second)); err == nil {
		t.Fatal("JobQuestionAuthorization allowed another turn while the first turn was unresolved")
	}

	unknown, err := NewJobQuestionTurnObservation(job, JobQuestionTurnObservationRequest{
		IntentID: turn.ID, CommandID: "cmd_76767676767676767676767676767676",
		Status: JobQuestionTurnUnknown,
	}, turnCreatedAt.Add(2*time.Second))
	if err != nil {
		t.Fatalf("NewJobQuestionTurnObservation UNKNOWN: %v", err)
	}
	job.JobQuestionTurnObservations = append(job.JobQuestionTurnObservations, unknown)
	job.Revision++
	job.UpdatedAt = unknown.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("Job.Validate with UNKNOWN JobQuestionTurnObservation: %v", err)
	}
	if _, err := NewJobQuestionTurnIntent(job, JobQuestionTurnIntentRequest{
		JobQuestionAuthorizationID: authorization.ID,
		CommandID:                  "cmd_77777777777777777777777777777777",
		PromptSHA256:               sha256HexDomain("Another duplicate turn"),
		ReservedTokens:             10,
	}, turnCreatedAt.Add(3*time.Second)); err == nil {
		t.Fatal("JobQuestionAuthorization allowed another turn while UNKNOWN was unreconciled")
	}

	reconciled, err := NewJobQuestionTurnObservation(job, JobQuestionTurnObservationRequest{
		IntentID: turn.ID, CommandID: "cmd_78787878787878787878787878787878",
		Status: JobQuestionTurnCompleted, TokensUsed: 20,
		Content: "Planner response", ReceiptID: "receipt_planner_response",
		EvidenceSHA256: strings.Repeat("e", 64), ReconcilesUnknown: true,
	}, turnCreatedAt.Add(4*time.Second))
	if err != nil {
		t.Fatalf("NewJobQuestionTurnObservation reconciliation: %v", err)
	}
	job.JobQuestionTurnObservations = append(job.JobQuestionTurnObservations, reconciled)
	job.Revision++
	job.UpdatedAt = reconciled.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("Job.Validate after UNKNOWN reconciliation: %v", err)
	}
	if _, err := NewJobQuestionTurnIntent(job, JobQuestionTurnIntentRequest{
		JobQuestionAuthorizationID: authorization.ID,
		CommandID:                  "cmd_79797979797979797979797979797979",
		PromptSHA256:               sha256HexDomain("A subsequent turn after reconciliation"),
		ReservedTokens:             10,
	}, turnCreatedAt.Add(5*time.Second)); err != nil {
		t.Fatalf("JobQuestionAuthorization rejected a second turn after terminal reconciliation: %v", err)
	}
}

func TestJobQuestionAuthorizationRejectsScopeAndBudgetExpansion(t *testing.T) {
	job, task, now := approvedJobWithTaskForExecutionPermission(t)
	permissionRequest := executionPermissionTestRequest(job, now)
	permissionSHA, err := ExecutionPermissionRequestSHA256(permissionRequest)
	if err != nil {
		t.Fatalf("ExecutionPermissionRequestSHA256: %v", err)
	}
	permission, err := NewExecutionPermission(
		job, permissionRequest, permissionSHA,
		executionPermissionTestAssessment(permissionRequest, permissionSHA, now), now,
	)
	if err != nil {
		t.Fatalf("NewExecutionPermission: %v", err)
	}
	job.ExecutionPermissions = append(job.ExecutionPermissions, permission)
	job.Revision++
	job.UpdatedAt = now
	start, err := NewTaskRoleExecutionStart(
		job, task.ID, permission.ID, "cmd_74747474747474747474747474747474",
		permission.RequestSHA256, now.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("NewTaskRoleExecutionStart: %v", err)
	}
	job.RoleExecutions = append(job.RoleExecutions, start.RoleExecution)
	job.ProviderStartIntents = append(job.ProviderStartIntents, start.ProviderStartIntent)
	job.Revision++
	job.UpdatedAt = start.RoleExecution.CreatedAt
	planner := job.RoleExecutions[0]
	message, err := NewJobMessage(job, "cmd_75757575757575757575757575757575", JobMessageRequest{
		Sender:    ActorRef{Kind: ActorCoder, ID: start.RoleExecution.ID},
		Recipient: ActorRef{Kind: ActorPlanner, ID: planner.ID},
		Body:      "Please clarify the approved implementation constraint.",
	}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("NewJobMessage: %v", err)
	}
	job.JobMessages = append(job.JobMessages, message)
	job.Revision++
	job.UpdatedAt = message.CreatedAt
	grant := job.PlanningAuthorizations[0]
	startObservation, _ := latestProviderStartObservation(job, planner.ProviderStartOperationID)
	contract := job.JobContractRevisions[len(job.JobContractRevisions)-1].Contract
	base := JobQuestionAuthorizationRequest{
		JobID: job.ID, CommandID: "cmd_76767676767676767676767676767676",
		QuestionMessageID: message.ID, PlannerRoleExecutionID: planner.ID,
		ProviderSessionID: startObservation.ProviderSessionID,
		RepositoryID:      contract.TargetRepositories[0], PlanningRepositoryID: grant.RepositoryID, ProviderID: grant.ProviderID,
		AccountID: grant.AccountID, ConfigurationSHA256: grant.ConfigurationSHA256,
		ReadOnlyBoundary: PlanningReadOnlyBoundary{
			Mode: PlanningReadOnlyBoundarySnapshot, Reference: "question_snapshot_2", SHA256: strings.Repeat("d", 64),
		},
		Budget: PlanningBudget{MaxTurns: 1, MaxTokens: 100}, Deadline: contract.Deadline.Add(-time.Minute),
	}
	if _, err := NewJobQuestionAuthorization(job, base, now.Add(3*time.Minute)); err != nil {
		t.Fatalf("unmodified JobQuestionAuthorization request is invalid: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*JobQuestionAuthorizationRequest)
	}{
		{name: "foreign repository", mutate: func(r *JobQuestionAuthorizationRequest) {
			r.RepositoryID = "repo_foreign"
		}},
		{name: "foreign Provider", mutate: func(r *JobQuestionAuthorizationRequest) { r.ProviderID = "provider.foreign" }},
		{name: "foreign session", mutate: func(r *JobQuestionAuthorizationRequest) { r.ProviderSessionID = "session_foreign" }},
		{name: "configuration differs from approved contract", mutate: func(r *JobQuestionAuthorizationRequest) { r.ConfigurationSHA256 = strings.Repeat("0", 64) }},
		{name: "budget exceeds contract", mutate: func(r *JobQuestionAuthorizationRequest) { r.Budget.MaxTokens = contract.Budget.MaxTokens + 1 }},
		{name: "deadline exceeds contract", mutate: func(r *JobQuestionAuthorizationRequest) { r.Deadline = contract.Deadline.Add(time.Minute) }},
		{name: "mutable context", mutate: func(r *JobQuestionAuthorizationRequest) { r.ReadOnlyBoundary.Mode = "mutable" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := base
			test.mutate(&request)
			if _, err := NewJobQuestionAuthorization(job, request, now.Add(3*time.Minute)); err == nil {
				t.Fatal("invalid JobQuestionAuthorization request was accepted")
			}
			if err := job.Validate(); err != nil {
				t.Fatalf("rejected request mutated Job: %v", err)
			}
		})
	}
}

func TestJobQuestionAuthorizationReservesBudgetAcrossQuestions(t *testing.T) {
	job, task, now := approvedJobWithTaskForExecutionPermission(t)
	permissionRequest := executionPermissionTestRequest(job, now)
	permissionSHA, err := ExecutionPermissionRequestSHA256(permissionRequest)
	if err != nil {
		t.Fatalf("ExecutionPermissionRequestSHA256: %v", err)
	}
	permission, err := NewExecutionPermission(
		job, permissionRequest, permissionSHA,
		executionPermissionTestAssessment(permissionRequest, permissionSHA, now), now,
	)
	if err != nil {
		t.Fatalf("NewExecutionPermission: %v", err)
	}
	job.ExecutionPermissions = append(job.ExecutionPermissions, permission)
	job.Revision++
	job.UpdatedAt = now
	start, err := NewTaskRoleExecutionStart(
		job, task.ID, permission.ID, "cmd_81818181818181818181818181818181",
		permission.RequestSHA256, now.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("NewTaskRoleExecutionStart: %v", err)
	}
	job.RoleExecutions = append(job.RoleExecutions, start.RoleExecution)
	job.ProviderStartIntents = append(job.ProviderStartIntents, start.ProviderStartIntent)
	job.Revision++
	job.UpdatedAt = start.RoleExecution.CreatedAt

	planner := job.RoleExecutions[0]
	grant := job.PlanningAuthorizations[0]
	startObservation, found := latestProviderStartObservation(job, planner.ProviderStartOperationID)
	if !found || startObservation.Status != ProviderStartAccepted {
		t.Fatal("Planner has no accepted Provider Session")
	}
	contract := job.JobContractRevisions[len(job.JobContractRevisions)-1].Contract
	newQuestion := func(commandID, body string, at time.Time) JobMessage {
		message, err := NewJobMessage(job, commandID, JobMessageRequest{
			Sender:    ActorRef{Kind: ActorCoder, ID: start.RoleExecution.ID},
			Recipient: ActorRef{Kind: ActorPlanner, ID: planner.ID},
			Body:      body,
		}, at)
		if err != nil {
			t.Fatalf("NewJobMessage: %v", err)
		}
		job.JobMessages = append(job.JobMessages, message)
		job.Revision++
		job.UpdatedAt = message.CreatedAt
		return message
	}
	firstQuestion := newQuestion(
		"cmd_82828282828282828282828282828282",
		"Clarify the first approved constraint.",
		now.Add(2*time.Minute),
	)
	firstRequest := JobQuestionAuthorizationRequest{
		JobID: job.ID, CommandID: "cmd_83838383838383838383838383838383",
		QuestionMessageID: firstQuestion.ID, PlannerRoleExecutionID: planner.ID,
		ProviderSessionID: startObservation.ProviderSessionID,
		RepositoryID:      contract.TargetRepositories[0], PlanningRepositoryID: grant.RepositoryID,
		ProviderID: grant.ProviderID, AccountID: grant.AccountID,
		ConfigurationSHA256: grant.ConfigurationSHA256,
		ReadOnlyBoundary: PlanningReadOnlyBoundary{
			Mode: PlanningReadOnlyBoundarySnapshot, Reference: "question_budget_snapshot_1",
			SHA256: strings.Repeat("d", 64),
		},
		Budget:   PlanningBudget{MaxTurns: 1, MaxTokens: 100},
		Deadline: contract.Deadline.Add(-time.Minute),
	}
	first, err := NewJobQuestionAuthorization(job, firstRequest, now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("first JobQuestionAuthorization: %v", err)
	}
	job.JobQuestionAuthorizations = append(job.JobQuestionAuthorizations, first)
	job.Revision++
	job.UpdatedAt = first.CreatedAt

	secondQuestion := newQuestion(
		"cmd_84848484848484848484848484848484",
		"Clarify the second approved constraint.",
		now.Add(4*time.Minute),
	)
	secondRequest := firstRequest
	secondRequest.CommandID = "cmd_85858585858585858585858585858585"
	secondRequest.QuestionMessageID = secondQuestion.ID
	secondRequest.ReadOnlyBoundary.Reference = "question_budget_snapshot_2"
	secondRequest.ReadOnlyBoundary.SHA256 = strings.Repeat("e", 64)
	// The remaining Job budget would permit this grant if the first question's
	// reservation were ignored. Existing question grants must reserve budget.
	secondRequest.Budget.MaxTokens = contract.Budget.MaxTokens - permissionRequest.ReservedTokens
	if _, err := NewJobQuestionAuthorization(job, secondRequest, now.Add(5*time.Minute)); err == nil {
		t.Fatal("second JobQuestionAuthorization reused budget already reserved by the first question")
	}

	// The exact remaining budget is accepted, while a later aggregate mutation
	// that exceeds the same reservation is rejected during Job restoration.
	secondRequest.Budget.MaxTokens -= first.Budget.MaxTokens
	second, err := NewJobQuestionAuthorization(job, secondRequest, now.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("second JobQuestionAuthorization within remaining budget: %v", err)
	}
	job.JobQuestionAuthorizations = append(job.JobQuestionAuthorizations, second)
	job.Revision++
	job.UpdatedAt = second.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("Job.Validate with fully reserved question budgets: %v", err)
	}
	job.JobQuestionAuthorizations[len(job.JobQuestionAuthorizations)-1].Budget.MaxTokens++
	if err := job.Validate(); err == nil {
		t.Fatal("Job.Validate accepted aggregate question grants beyond the approved Job budget")
	}
}
