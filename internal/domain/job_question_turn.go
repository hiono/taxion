package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	JobQuestionTurnUnknown   = "UNKNOWN"
	JobQuestionTurnCompleted = "COMPLETED"
	JobQuestionTurnFailed    = "FAILED"
)

type JobQuestionTurnIntentRequest struct {
	JobQuestionAuthorizationID string `json:"job_question_authorization_id"`
	CommandID                  string `json:"command_id"`
	PromptSHA256               string `json:"prompt_sha256"`
	ReservedTokens             uint64 `json:"reserved_tokens"`
}

type JobQuestionTurnIntent struct {
	ID                         string    `json:"id"`
	OperationID                string    `json:"operation_id"`
	CommandID                  string    `json:"command_id"`
	JobID                      string    `json:"job_id"`
	JobQuestionAuthorizationID string    `json:"job_question_authorization_id"`
	QuestionMessageID          string    `json:"question_message_id"`
	RoleExecutionID            string    `json:"role_execution_id"`
	ProviderSessionID          string    `json:"provider_session_id"`
	ProviderID                 string    `json:"provider_id"`
	AccountID                  string    `json:"account_id"`
	ConfigurationSHA256        string    `json:"configuration_sha256"`
	RequestSHA256              string    `json:"request_sha256"`
	PromptSHA256               string    `json:"prompt_sha256"`
	TurnNumber                 uint32    `json:"turn_number"`
	ReservedTokens             uint64    `json:"reserved_tokens"`
	Deadline                   time.Time `json:"deadline"`
	CreatedAt                  time.Time `json:"created_at"`
	CreatedRevision            uint64    `json:"created_revision"`
}

type JobQuestionTurnObservationRequest struct {
	IntentID          string `json:"intent_id"`
	CommandID         string `json:"command_id"`
	Status            string `json:"status"`
	TokensUsed        uint64 `json:"tokens_used"`
	Content           string `json:"content,omitempty"`
	ReceiptID         string `json:"receipt_id,omitempty"`
	EvidenceSHA256    string `json:"evidence_sha256,omitempty"`
	ReconcilesUnknown bool   `json:"reconciles_unknown,omitempty"`
}

type JobQuestionTurnObservation struct {
	ID                string    `json:"id"`
	CommandID         string    `json:"command_id"`
	JobID             string    `json:"job_id"`
	IntentID          string    `json:"intent_id"`
	OperationID       string    `json:"operation_id"`
	QuestionMessageID string    `json:"question_message_id"`
	RoleExecutionID   string    `json:"role_execution_id"`
	ProviderSessionID string    `json:"provider_session_id"`
	Status            string    `json:"status"`
	TokensUsed        uint64    `json:"tokens_used"`
	Content           string    `json:"content,omitempty"`
	ContentSHA256     string    `json:"content_sha256,omitempty"`
	ReceiptID         string    `json:"receipt_id,omitempty"`
	EvidenceSHA256    string    `json:"evidence_sha256,omitempty"`
	ReconcilesUnknown bool      `json:"reconciles_unknown,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	CreatedRevision   uint64    `json:"created_revision"`
}

func JobQuestionTurnIntentID(commandID string) string {
	return planningOperationID("job_question_turn", commandID)
}

func JobQuestionTurnOperationID(commandID string) string {
	return planningOperationID("job_question_turn_operation", commandID)
}

func JobQuestionTurnObservationID(commandID string) string {
	return planningOperationID("job_question_turn_observation", commandID)
}

func NewJobQuestionTurnIntent(job Job, request JobQuestionTurnIntentRequest, now time.Time) (JobQuestionTurnIntent, error) {
	now = now.UTC().Round(0)
	if err := job.Validate(); err != nil {
		return JobQuestionTurnIntent{}, fmt.Errorf("validate Job before question turn: %w", err)
	}
	if !validCommandID(request.CommandID) ||
		!validPlanningIdentity(request.JobQuestionAuthorizationID) ||
		!validSHA256(request.PromptSHA256) || request.ReservedTokens == 0 || now.IsZero() {
		return JobQuestionTurnIntent{}, errors.New("JobQuestionTurnIntent command, authorization, prompt digest, reservation, and timestamp are required")
	}
	grant, found := jobQuestionAuthorizationByID(job, request.JobQuestionAuthorizationID)
	if !found {
		return JobQuestionTurnIntent{}, errors.New("JobQuestionAuthorization was not found")
	}
	if err := grant.ValidateFor(
		job, grant.QuestionMessageID, grant.PlannerRoleExecutionID, grant.ProviderSessionID, now,
	); err != nil {
		return JobQuestionTurnIntent{}, err
	}
	turnNumber, tokensUsed, err := jobQuestionTurnBudgetUsed(job, grant.ID)
	if err != nil {
		return JobQuestionTurnIntent{}, err
	}
	if turnNumber >= grant.Budget.MaxTurns || tokensUsed > grant.Budget.MaxTokens ||
		request.ReservedTokens > grant.Budget.MaxTokens-tokensUsed {
		return JobQuestionTurnIntent{}, errors.New("JobQuestionAuthorization turn or token budget is exhausted")
	}
	start, found := providerStartIntentForRoleExecution(job, grant.PlannerRoleExecutionID)
	if !found {
		return JobQuestionTurnIntent{}, errors.New("JobQuestionAuthorization Provider Start intent was not found")
	}
	if err := validateProviderStartSession(job, start, grant.ProviderSessionID, job.Revision+1, now); err != nil {
		return JobQuestionTurnIntent{}, err
	}
	if start.ProviderID != grant.ProviderID || start.AccountID != grant.AccountID ||
		start.ConfigurationSHA256 != grant.ConfigurationSHA256 {
		return JobQuestionTurnIntent{}, errors.New("Provider Start identity differs from JobQuestionAuthorization")
	}
	if job.Revision >= uint64(^uint64(0)>>1) {
		return JobQuestionTurnIntent{}, errors.New("Job revision exhausted")
	}
	intent := JobQuestionTurnIntent{
		ID:                         JobQuestionTurnIntentID(request.CommandID),
		OperationID:                JobQuestionTurnOperationID(request.CommandID),
		CommandID:                  request.CommandID,
		JobID:                      job.ID,
		JobQuestionAuthorizationID: grant.ID,
		QuestionMessageID:          grant.QuestionMessageID,
		RoleExecutionID:            grant.PlannerRoleExecutionID,
		ProviderSessionID:          grant.ProviderSessionID,
		ProviderID:                 grant.ProviderID,
		AccountID:                  grant.AccountID,
		ConfigurationSHA256:        grant.ConfigurationSHA256,
		PromptSHA256:               request.PromptSHA256,
		TurnNumber:                 turnNumber + 1,
		ReservedTokens:             request.ReservedTokens,
		Deadline:                   grant.Deadline,
		CreatedAt:                  now,
		CreatedRevision:            job.Revision + 1,
	}
	intent.RequestSHA256 = jobQuestionTurnRequestSHA256(intent)
	if err := intent.Validate(job); err != nil {
		return JobQuestionTurnIntent{}, err
	}
	return intent, nil
}

func (t JobQuestionTurnIntent) Validate(job Job) error {
	if !validPlanningIdentity(t.ID) || t.ID != JobQuestionTurnIntentID(t.CommandID) ||
		!validPlanningIdentity(t.OperationID) || t.OperationID != JobQuestionTurnOperationID(t.CommandID) ||
		!validCommandID(t.CommandID) || t.JobID != job.ID ||
		!validPlanningIdentity(t.JobQuestionAuthorizationID) ||
		!validPlanningIdentity(t.QuestionMessageID) || !validPlanningIdentity(t.RoleExecutionID) ||
		!validPlanningIdentity(t.ProviderSessionID) || !validPlanningIdentity(t.ProviderID) ||
		!validPlanningIdentity(t.AccountID) || !validSHA256(t.ConfigurationSHA256) ||
		!validSHA256(t.RequestSHA256) || !validSHA256(t.PromptSHA256) ||
		t.TurnNumber == 0 || t.ReservedTokens == 0 || t.Deadline.IsZero() ||
		t.CreatedAt.IsZero() || t.CreatedRevision == 0 || t.CreatedRevision > job.Revision+1 {
		return errors.New("JobQuestionTurnIntent identity or metadata is invalid")
	}
	grant, found := jobQuestionAuthorizationByID(job, t.JobQuestionAuthorizationID)
	if !found || grant.Validate(job) != nil ||
		t.QuestionMessageID != grant.QuestionMessageID ||
		t.RoleExecutionID != grant.PlannerRoleExecutionID ||
		t.ProviderSessionID != grant.ProviderSessionID ||
		t.ProviderID != grant.ProviderID || t.AccountID != grant.AccountID ||
		t.ConfigurationSHA256 != grant.ConfigurationSHA256 ||
		!t.Deadline.Equal(grant.Deadline) ||
		t.CreatedRevision <= grant.CreatedRevision ||
		t.CreatedAt.Before(grant.IssuedAt) || !t.CreatedAt.Before(grant.Deadline) ||
		t.RequestSHA256 != jobQuestionTurnRequestSHA256(t) {
		return errors.New("JobQuestionTurnIntent does not bind its JobQuestionAuthorization")
	}
	for _, revision := range job.JobContractRevisions {
		if revision.Revision > grant.ContractRevision && revision.CreatedRevision < t.CreatedRevision {
			return errors.New("JobQuestionTurnIntent was created after its JobQuestionAuthorization was superseded")
		}
	}
	planner, found := roleExecutionByID(job, t.RoleExecutionID)
	if !found || planner.Role != RolePlanner || planner.TaskID != "" {
		return errors.New("JobQuestionTurnIntent requires the authorized Taskless PLANNER RoleExecution")
	}
	start, found := providerStartIntentForRoleExecution(job, planner.ID)
	if !found || start.ProviderID != t.ProviderID || start.AccountID != t.AccountID ||
		start.ConfigurationSHA256 != t.ConfigurationSHA256 {
		return errors.New("JobQuestionTurnIntent Provider Start identity mismatch")
	}
	if err := validateProviderStartSession(job, start, t.ProviderSessionID, t.CreatedRevision, t.CreatedAt); err != nil {
		return err
	}
	if providerStopIntentExists(job, t.RoleExecutionID, t.ProviderSessionID, t.CreatedRevision) {
		return errors.New("JobQuestionTurnIntent follows a Provider Stop intent")
	}
	turnNumber, tokensUsed, err := jobQuestionTurnBudgetUsedBefore(job, grant.ID, t.CreatedRevision)
	if err != nil {
		return err
	}
	if t.TurnNumber != turnNumber+1 || t.TurnNumber > grant.Budget.MaxTurns ||
		tokensUsed > grant.Budget.MaxTokens || t.ReservedTokens > grant.Budget.MaxTokens-tokensUsed {
		return errors.New("JobQuestionTurnIntent exceeds its question turn or token budget")
	}
	return nil
}

func NewJobQuestionTurnObservation(job Job, request JobQuestionTurnObservationRequest, now time.Time) (JobQuestionTurnObservation, error) {
	now = now.UTC().Round(0)
	if err := job.Validate(); err != nil {
		return JobQuestionTurnObservation{}, fmt.Errorf("validate Job before question turn observation: %w", err)
	}
	if !validCommandID(request.CommandID) || !validPlanningIdentity(request.IntentID) || now.IsZero() {
		return JobQuestionTurnObservation{}, errors.New("JobQuestionTurnObservation command, intent, and timestamp are required")
	}
	intent, found := jobQuestionTurnIntentByID(job, request.IntentID)
	if !found {
		return JobQuestionTurnObservation{}, errors.New("JobQuestionTurnIntent was not found")
	}
	if err := intent.Validate(job); err != nil {
		return JobQuestionTurnObservation{}, err
	}
	if request.Status != JobQuestionTurnUnknown && request.Status != JobQuestionTurnCompleted &&
		request.Status != JobQuestionTurnFailed {
		return JobQuestionTurnObservation{}, errors.New("unsupported JobQuestionTurnObservation status")
	}
	contentSHA256 := ""
	if request.Status == JobQuestionTurnUnknown {
		if request.TokensUsed != 0 || request.Content != "" || request.ReceiptID != "" ||
			request.EvidenceSHA256 != "" || request.ReconcilesUnknown {
			return JobQuestionTurnObservation{}, errors.New("UNKNOWN question turn observation cannot claim output, usage, receipt, or evidence")
		}
	} else {
		if !utf8.ValidString(request.Content) || len([]byte(request.Content)) > MaxJobMessageBodyBytes ||
			(request.Status == JobQuestionTurnCompleted && strings.TrimSpace(request.Content) == "") ||
			request.TokensUsed > intent.ReservedTokens ||
			!validPlanningIdentity(request.ReceiptID) || !validSHA256(request.EvidenceSHA256) {
			return JobQuestionTurnObservation{}, errors.New("terminal question turn observation output, usage, receipt, or evidence is invalid")
		}
		contentSHA256 = sha256HexDomain(request.Content)
	}
	previous, foundPrevious := latestJobQuestionTurnObservation(job, intent.ID)
	if !foundPrevious {
		if request.ReconcilesUnknown {
			return JobQuestionTurnObservation{}, errors.New("first question turn observation cannot reconcile UNKNOWN")
		}
	} else if previous.Status != JobQuestionTurnUnknown ||
		request.Status == JobQuestionTurnUnknown || !request.ReconcilesUnknown {
		return JobQuestionTurnObservation{}, errors.New("only UNKNOWN question turn observations may be reconciled by terminal evidence")
	}
	if job.Revision >= uint64(^uint64(0)>>1) {
		return JobQuestionTurnObservation{}, errors.New("Job revision exhausted")
	}
	observation := JobQuestionTurnObservation{
		ID:                JobQuestionTurnObservationID(request.CommandID),
		CommandID:         request.CommandID,
		JobID:             job.ID,
		IntentID:          intent.ID,
		OperationID:       intent.OperationID,
		QuestionMessageID: intent.QuestionMessageID,
		RoleExecutionID:   intent.RoleExecutionID,
		ProviderSessionID: intent.ProviderSessionID,
		Status:            request.Status,
		TokensUsed:        request.TokensUsed,
		Content:           request.Content,
		ContentSHA256:     contentSHA256,
		ReceiptID:         request.ReceiptID,
		EvidenceSHA256:    request.EvidenceSHA256,
		ReconcilesUnknown: request.ReconcilesUnknown,
		CreatedAt:         now,
		CreatedRevision:   job.Revision + 1,
	}
	if err := observation.Validate(job, intent); err != nil {
		return JobQuestionTurnObservation{}, err
	}
	return observation, nil
}

func (o JobQuestionTurnObservation) Validate(job Job, intent JobQuestionTurnIntent) error {
	if !validPlanningIdentity(o.ID) || !validCommandID(o.CommandID) ||
		o.ID != JobQuestionTurnObservationID(o.CommandID) || o.JobID != job.ID ||
		o.IntentID != intent.ID || o.OperationID != intent.OperationID ||
		o.QuestionMessageID != intent.QuestionMessageID ||
		o.RoleExecutionID != intent.RoleExecutionID ||
		o.ProviderSessionID != intent.ProviderSessionID ||
		o.CreatedAt.IsZero() || o.CreatedAt.Before(intent.CreatedAt) ||
		o.CreatedRevision <= intent.CreatedRevision || o.CreatedRevision > job.Revision+1 {
		return errors.New("JobQuestionTurnObservation identity or metadata mismatch")
	}
	if o.Status == JobQuestionTurnUnknown {
		if o.TokensUsed != 0 || o.Content != "" || o.ContentSHA256 != "" ||
			o.ReceiptID != "" || o.EvidenceSHA256 != "" || o.ReconcilesUnknown {
			return errors.New("UNKNOWN JobQuestionTurnObservation contains terminal claims")
		}
		return nil
	}
	if o.Status != JobQuestionTurnCompleted && o.Status != JobQuestionTurnFailed {
		return errors.New("JobQuestionTurnObservation status is invalid")
	}
	if !utf8.ValidString(o.Content) || len([]byte(o.Content)) > MaxJobMessageBodyBytes ||
		(o.Status == JobQuestionTurnCompleted && strings.TrimSpace(o.Content) == "") ||
		o.TokensUsed > intent.ReservedTokens || !validPlanningIdentity(o.ReceiptID) ||
		!validSHA256(o.ContentSHA256) || sha256HexDomain(o.Content) != o.ContentSHA256 ||
		!validSHA256(o.EvidenceSHA256) {
		return errors.New("terminal JobQuestionTurnObservation output, usage, receipt, or evidence is invalid")
	}
	return nil
}

func validateJobQuestionTurnFacts(job Job) error {
	intentsByID := make(map[string]JobQuestionTurnIntent, len(job.JobQuestionTurnIntents))
	seenIntentIDs := make(map[string]struct{}, len(job.JobQuestionTurnIntents))
	seenIntentCommands := make(map[string]struct{}, len(job.JobQuestionTurnIntents))
	seenOperations := make(map[string]struct{}, len(job.JobQuestionTurnIntents))
	var previousIntentRevision uint64
	var previousIntentAt time.Time
	for _, intent := range job.JobQuestionTurnIntents {
		if err := intent.Validate(job); err != nil {
			return fmt.Errorf("invalid JobQuestionTurnIntent: %w", err)
		}
		if intent.CreatedAt.After(job.UpdatedAt) || intent.CreatedRevision > job.Revision ||
			intent.CreatedRevision <= previousIntentRevision ||
			(!previousIntentAt.IsZero() && intent.CreatedAt.Before(previousIntentAt)) {
			return errors.New("JobQuestionTurnIntent append order is invalid")
		}
		if _, exists := seenIntentIDs[intent.ID]; exists {
			return errors.New("duplicate JobQuestionTurnIntent ID")
		}
		if _, exists := seenIntentCommands[intent.CommandID]; exists {
			return errors.New("duplicate JobQuestionTurnIntent CommandID")
		}
		if _, exists := seenOperations[intent.OperationID]; exists {
			return errors.New("duplicate JobQuestionTurnIntent OperationID")
		}
		seenIntentIDs[intent.ID] = struct{}{}
		seenIntentCommands[intent.CommandID] = struct{}{}
		seenOperations[intent.OperationID] = struct{}{}
		intentsByID[intent.ID] = intent
		previousIntentRevision, previousIntentAt = intent.CreatedRevision, intent.CreatedAt
	}
	observationsByIntent := make(map[string][]JobQuestionTurnObservation)
	seenObservationIDs := make(map[string]struct{}, len(job.JobQuestionTurnObservations))
	seenObservationCommands := make(map[string]struct{}, len(job.JobQuestionTurnObservations))
	var previousObservationRevision uint64
	var previousObservationAt time.Time
	for _, observation := range job.JobQuestionTurnObservations {
		intent, found := intentsByID[observation.IntentID]
		if !found {
			return errors.New("JobQuestionTurnObservation has no intent")
		}
		if err := observation.Validate(job, intent); err != nil {
			return fmt.Errorf("invalid JobQuestionTurnObservation: %w", err)
		}
		if observation.CreatedAt.After(job.UpdatedAt) || observation.CreatedRevision > job.Revision ||
			observation.CreatedRevision <= previousObservationRevision ||
			(!previousObservationAt.IsZero() && observation.CreatedAt.Before(previousObservationAt)) {
			return errors.New("JobQuestionTurnObservation append order is invalid")
		}
		if _, exists := seenObservationIDs[observation.ID]; exists {
			return errors.New("duplicate JobQuestionTurnObservation ID")
		}
		if _, exists := seenObservationCommands[observation.CommandID]; exists {
			return errors.New("duplicate JobQuestionTurnObservation CommandID")
		}
		seenObservationIDs[observation.ID] = struct{}{}
		seenObservationCommands[observation.CommandID] = struct{}{}
		observationsByIntent[observation.IntentID] = append(observationsByIntent[observation.IntentID], observation)
		previousObservationRevision, previousObservationAt = observation.CreatedRevision, observation.CreatedAt
	}
	for intentID, observations := range observationsByIntent {
		if len(observations) > 2 {
			return fmt.Errorf("JobQuestionTurnIntent %s has too many observations", intentID)
		}
		if len(observations) == 1 && observations[0].ReconcilesUnknown {
			return fmt.Errorf("JobQuestionTurnIntent %s first observation reconciles UNKNOWN", intentID)
		}
		if len(observations) == 2 {
			first, second := observations[0], observations[1]
			if first.Status != JobQuestionTurnUnknown ||
				(second.Status != JobQuestionTurnCompleted && second.Status != JobQuestionTurnFailed) ||
				!second.ReconcilesUnknown || second.CreatedRevision <= first.CreatedRevision ||
				second.CreatedAt.Before(first.CreatedAt) {
				return fmt.Errorf("JobQuestionTurnIntent %s has an invalid UNKNOWN reconciliation sequence", intentID)
			}
		}
	}
	for _, grant := range job.JobQuestionAuthorizations {
		var turnNumber uint32
		var tokensUsed uint64
		unresolvedTurn := false
		for _, intent := range job.JobQuestionTurnIntents {
			if intent.JobQuestionAuthorizationID != grant.ID {
				continue
			}
			if unresolvedTurn {
				return fmt.Errorf("JobQuestionAuthorization %s has a turn after an unresolved turn", grant.ID)
			}
			turnNumber++
			observation, found := latestJobQuestionTurnObservation(job, intent.ID)
			if !found || observation.Status == JobQuestionTurnUnknown {
				unresolvedTurn = true
				continue
			}
			if ^uint64(0)-tokensUsed < observation.TokensUsed {
				return errors.New("JobQuestionAuthorization token usage overflow")
			}
			tokensUsed += observation.TokensUsed
		}
		if turnNumber > grant.Budget.MaxTurns || tokensUsed > grant.Budget.MaxTokens {
			return fmt.Errorf("JobQuestionAuthorization %s turn or token budget is exceeded", grant.ID)
		}
	}
	return nil
}

func jobQuestionAuthorizationByID(job Job, authorizationID string) (JobQuestionAuthorization, bool) {
	for _, grant := range job.JobQuestionAuthorizations {
		if grant.ID == authorizationID {
			return grant, true
		}
	}
	return JobQuestionAuthorization{}, false
}

func jobQuestionTurnIntentByID(job Job, intentID string) (JobQuestionTurnIntent, bool) {
	for _, intent := range job.JobQuestionTurnIntents {
		if intent.ID == intentID {
			return intent, true
		}
	}
	return JobQuestionTurnIntent{}, false
}

func latestJobQuestionTurnObservation(job Job, intentID string) (JobQuestionTurnObservation, bool) {
	var latest JobQuestionTurnObservation
	found := false
	for _, observation := range job.JobQuestionTurnObservations {
		if observation.IntentID == intentID && (!found || observation.CreatedRevision > latest.CreatedRevision) {
			latest, found = observation, true
		}
	}
	return latest, found
}

func providerStartIntentForRoleExecution(job Job, roleExecutionID string) (ProviderStartIntent, bool) {
	for _, start := range job.ProviderStartIntents {
		if start.RoleExecutionID == roleExecutionID {
			return start, true
		}
	}
	return ProviderStartIntent{}, false
}

func jobQuestionTurnBudgetUsed(job Job, authorizationID string) (uint32, uint64, error) {
	return jobQuestionTurnBudgetUsedBefore(job, authorizationID, job.Revision+1)
}

func jobQuestionTurnBudgetUsedBefore(job Job, authorizationID string, beforeRevision uint64) (uint32, uint64, error) {
	observationsByIntent := make(map[string]JobQuestionTurnObservation, len(job.JobQuestionTurnObservations))
	for _, observation := range job.JobQuestionTurnObservations {
		if observation.CreatedRevision >= beforeRevision {
			continue
		}
		previous, found := observationsByIntent[observation.IntentID]
		if !found || observation.CreatedRevision > previous.CreatedRevision {
			observationsByIntent[observation.IntentID] = observation
		}
	}

	var turns uint32
	var tokens uint64
	for _, intent := range job.JobQuestionTurnIntents {
		if intent.JobQuestionAuthorizationID != authorizationID || intent.CreatedRevision >= beforeRevision {
			continue
		}
		turns++
		observation, found := observationsByIntent[intent.ID]
		if !found || observation.Status == JobQuestionTurnUnknown {
			return turns, tokens, errors.New("previous JobQuestion Provider Turn is unresolved; reconcile the same OperationID before another turn")
		}
		if ^uint64(0)-tokens < observation.TokensUsed {
			return turns, tokens, errors.New("JobQuestionAuthorization token usage overflow")
		}
		tokens += observation.TokensUsed
	}
	return turns, tokens, nil
}

func jobQuestionTurnRequestSHA256(intent JobQuestionTurnIntent) string {
	material := fmt.Sprintf(
		"taxion:job-question-turn:v1:%s:%s:%s:%s:%s:%s:%s:%s:%d:%d:%s",
		intent.JobQuestionAuthorizationID, intent.QuestionMessageID, intent.RoleExecutionID,
		intent.ProviderSessionID, intent.ProviderID, intent.AccountID, intent.ConfigurationSHA256,
		intent.PromptSHA256, intent.ReservedTokens, intent.TurnNumber,
		intent.Deadline.UTC().Format(time.RFC3339Nano),
	)
	return sha256HexDomain(material)
}
