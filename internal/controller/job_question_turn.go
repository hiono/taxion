package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/provider"
	"github.com/hiono/taxion/internal/store"
)

const plannerQuestionRecoveryCallTimeout = 5 * time.Second

type JobQuestionTurnStore interface {
	CommunicationStore
	AddJobQuestionTurnIntent(
		context.Context, string, uint64, domain.JobQuestionTurnIntentRequest,
	) (store.CommandReceipt, domain.JobQuestionTurnIntent, error)
	AddJobQuestionTurnObservation(
		context.Context, string, uint64, domain.JobQuestionTurnObservationRequest,
	) (store.CommandReceipt, domain.JobQuestionTurnObservation, error)
}

type JobQuestionTurnOutcome struct {
	IntentReceipt      store.CommandReceipt
	Intent             domain.JobQuestionTurnIntent
	ObservationReceipt store.CommandReceipt
	Observation        domain.JobQuestionTurnObservation
	Reply              CommunicationOutcome
}

type JobQuestionTurnController struct {
	store           JobQuestionTurnStore
	runtime         PlanningProviderRuntime
	communication   *CommunicationController
	minimumEvidence provider.EvidenceLevel
	now             func() time.Time
}

func NewJobQuestionTurnController(
	turnStore JobQuestionTurnStore,
	runtime PlanningProviderRuntime,
) (*JobQuestionTurnController, error) {
	return newJobQuestionTurnController(turnStore, runtime, provider.EvidenceLive)
}

// newJobQuestionTurnController permits package tests to exercise deterministic E2
// orchestration with synthetic capabilities. Production callers use the exported
// constructor, which always requires live Provider evidence.
func newJobQuestionTurnController(
	turnStore JobQuestionTurnStore,
	runtime PlanningProviderRuntime,
	minimumEvidence provider.EvidenceLevel,
) (*JobQuestionTurnController, error) {
	if turnStore == nil || runtime == nil {
		return nil, errors.New("JobQuestionTurnStore and Provider runtime are required")
	}
	if err := runtime.Identity().Validate(); err != nil {
		return nil, fmt.Errorf("validate JobQuestionTurn Provider identity: %w", err)
	}
	communication, err := NewCommunicationController(turnStore)
	if err != nil {
		return nil, err
	}
	return &JobQuestionTurnController{
		store: turnStore, runtime: runtime, communication: communication,
		minimumEvidence: minimumEvidence, now: time.Now,
	}, nil
}

// Send executes the additional bounded read-only Planner turn for one persisted question.
// The original question body remains natural language; its reply is persisted as a JobMessage.
func (c *JobQuestionTurnController) Send(
	ctx context.Context,
	jobID, authorizationID, commandID string,
	reservedTokens uint64,
) (JobQuestionTurnOutcome, error) {
	var outcome JobQuestionTurnOutcome
	if ctx == nil || jobID == "" || authorizationID == "" || commandID == "" || reservedTokens == 0 {
		return outcome, errors.New("JobQuestionTurn requires Job, authorization, Command, and token reservation")
	}
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return outcome, err
	}
	if err := job.Validate(); err != nil {
		return outcome, fmt.Errorf("validate Job before question turn: %w", err)
	}
	grant, found := findJobQuestionAuthorization(job, authorizationID)
	if !found {
		return outcome, errors.New("JobQuestionAuthorization was not found")
	}
	question, found := findJobMessage(job, grant.QuestionMessageID)
	if !found {
		return outcome, errors.New("JobQuestionAuthorization source Question was not found")
	}
	reply, hasReply := existingPlannerReply(job, question)
	prompt := jobQuestionTurnPrompt(question.Body)
	promptSHA256 := sha256String(prompt)
	var capabilities provider.Capabilities

	intent, existing := findJobQuestionTurnIntentByCommand(job, commandID)
	if existing {
		if intent.JobQuestionAuthorizationID != grant.ID ||
			intent.PromptSHA256 != promptSHA256 || intent.ReservedTokens != reservedTokens {
			return outcome, store.ErrIdempotencyConflict
		}
		outcome.Intent = intent
		if hasReply {
			outcome.Reply = CommunicationOutcome{Message: reply}
			outcome.Observation, _ = latestJobQuestionTurnObservation(job, intent.ID)
			if !c.now().UTC().Before(grant.Deadline) {
				return outcome, nil
			}
		}
	} else {
		if hasReply {
			outcome.Reply = CommunicationOutcome{Message: reply}
			return outcome, nil
		}
		now := c.now().UTC().Round(0)
		if err := grant.ValidateFor(job, question.ID, grant.PlannerRoleExecutionID, grant.ProviderSessionID, now); err != nil {
			return outcome, err
		}
		capabilities, err = c.verifiedCapabilities(ctx, grant)
		if err != nil {
			return outcome, err
		}
		if !capabilities.HasEvidenceAtLeast(provider.CapabilityReadOnlyContextEnforcement, c.minimumEvidence) {
			return outcome, provider.ErrUnsupportedCapability
		}
		receipt, created, err := c.store.AddJobQuestionTurnIntent(ctx, job.ID, job.Revision, domain.JobQuestionTurnIntentRequest{
			JobQuestionAuthorizationID: grant.ID,
			CommandID:                  commandID,
			PromptSHA256:               promptSHA256,
			ReservedTokens:             reservedTokens,
		})
		if err != nil {
			return outcome, err
		}
		// Only the call that atomically created the intent may claim the first dispatch.
		existing = !receipt.Created
		outcome.IntentReceipt, outcome.Intent = receipt, created
		intent = created
		job, err = c.store.GetJob(ctx, jobID)
		if err != nil {
			return outcome, err
		}
	}

	if err := intent.Validate(job); err != nil {
		return outcome, fmt.Errorf("validate persisted JobQuestionTurnIntent: %w", err)
	}
	if latest, found := latestJobQuestionTurnObservation(job, intent.ID); found {
		outcome.Observation = latest
		switch latest.Status {
		case domain.JobQuestionTurnCompleted:
			reply, err := c.persistQuestionReply(ctx, jobID, grant, question, intent, latest)
			outcome.Reply = reply
			return outcome, err
		case domain.JobQuestionTurnFailed:
			return outcome, errors.New("Planner question turn completed with a Provider failure")
		case domain.JobQuestionTurnNotDispatched:
			if hasReply {
				return outcome, nil
			}
			now := c.now().UTC().Round(0)
			if err := grant.ValidateFor(job, question.ID, grant.PlannerRoleExecutionID, grant.ProviderSessionID, now); err != nil {
				return outcome, err
			}
			capabilities, err := c.verifiedCapabilities(ctx, grant)
			if err != nil {
				return outcome, err
			}
			if !capabilities.HasEvidenceAtLeast(provider.CapabilityReadOnlyContextEnforcement, c.minimumEvidence) {
				return outcome, provider.ErrUnsupportedCapability
			}
			return c.dispatch(ctx, outcome, grant, question, intent, capabilities, false)
		default:
			return c.reconcileUnknown(ctx, outcome, job, grant, question, intent)
		}
	}
	if existing {
		return c.reconcileUnknown(ctx, outcome, job, grant, question, intent)
	}

	now := c.now().UTC().Round(0)
	if err := grant.ValidateFor(job, question.ID, grant.PlannerRoleExecutionID, grant.ProviderSessionID, now); err != nil {
		return outcome, err
	}
	return c.dispatch(ctx, outcome, grant, question, intent, capabilities, false)
}

func (c *JobQuestionTurnController) reconcileUnknown(
	ctx context.Context,
	outcome JobQuestionTurnOutcome,
	job domain.Job,
	grant domain.JobQuestionAuthorization,
	question domain.JobMessage,
	intent domain.JobQuestionTurnIntent,
) (JobQuestionTurnOutcome, error) {
	capabilities, err := c.verifiedCapabilities(ctx, grant)
	if err != nil {
		return outcome, err
	}
	lookupConfirmedNotFound := false
	if capabilities.SendLookup &&
		capabilities.HasEvidenceAtLeast(provider.CapabilitySendLookup, c.minimumEvidence) &&
		capabilities.HasEvidenceAtLeast(provider.CapabilityUsageIdentity, c.minimumEvidence) &&
		capabilities.HasEvidenceAtLeast(provider.CapabilityUsageCompleteness, c.minimumEvidence) {
		request := provider.LookupRequest{
			OperationID: intent.OperationID, SessionID: intent.ProviderSessionID, TurnID: intent.ID,
			RequireResultDigest: true,
		}
		lookupCtx, cancelLookup := context.WithTimeout(ctx, plannerQuestionRecoveryCallTimeout)
		lookup, lookupErr := c.runtime.Lookup(lookupCtx, request)
		cancelLookup()
		if lookupErr != nil {
			return outcome, fmt.Errorf("lookup UNKNOWN Planner question turn: %w", lookupErr)
		}
		if err := lookup.Validate(request); err != nil {
			return outcome, fmt.Errorf("validate UNKNOWN Planner question lookup: %w", err)
		}
		if !lookup.Found {
			lookupConfirmedNotFound = true
		}
		if lookup.Found {
			if lookup.Status != domain.JobQuestionTurnCompleted && lookup.Status != domain.JobQuestionTurnFailed {
				return outcome, errors.New("Planner question Provider operation remains unresolved")
			}
			usageCtx, cancelUsage := context.WithTimeout(ctx, plannerQuestionRecoveryCallTimeout)
			usage, err := c.runtime.Usage(usageCtx, provider.UsageRequest{
				OperationID: intent.OperationID, SessionID: intent.ProviderSessionID, TurnID: intent.ID,
			})
			cancelUsage()
			if err != nil {
				return outcome, fmt.Errorf("retrieve UNKNOWN Planner question usage: %w", err)
			}
			if usage.OperationID != intent.OperationID || usage.SessionID != intent.ProviderSessionID ||
				usage.TurnID != intent.ID || usage.UsageID == "" || usage.UsageID != lookup.UsageID ||
				!usage.Complete || usage.Tokens > intent.ReservedTokens ||
				!validQuestionTurnEvidence(usage.EvidenceSHA256) {
				return outcome, provider.ErrProviderInvalidResult
			}
			if lookup.Status == domain.JobQuestionTurnFailed {
				evidence := sha256String(lookup.EvidenceSHA256 + ":" + usage.EvidenceSHA256)
				receipt, observation, err := c.persistObservation(
					ctx, job.ID, intent.ID, domain.JobQuestionTurnFailed, usage.Tokens, "",
					lookup.ReceiptID, evidence,
				)
				if err != nil {
					return outcome, err
				}
				outcome.ObservationReceipt, outcome.Observation = receipt, observation
				return outcome, errors.New("Planner question Provider operation failed")
			}
			if !capabilities.HasEvidenceAtLeast(provider.CapabilityFinalOutput, c.minimumEvidence) {
				return outcome, provider.ErrUnsupportedCapability
			}
			if lookup.ResultSHA256 == "" {
				return outcome, provider.ErrProviderInvalidResult
			}
			resultCtx, cancelResult := context.WithTimeout(ctx, plannerQuestionRecoveryCallTimeout)
			result, err := c.runtime.Result(resultCtx, provider.ResultRequest{
				OperationID: intent.OperationID, SessionID: intent.ProviderSessionID, TurnID: intent.ID,
			})
			cancelResult()
			if err != nil {
				return outcome, fmt.Errorf("retrieve UNKNOWN Planner question result: %w", err)
			}
			if result.OperationID != intent.OperationID || result.SessionID != intent.ProviderSessionID ||
				result.TurnID != intent.ID || !result.Final || result.ResultSHA256 != sha256String(result.Content) ||
				!validQuestionTurnEvidence(result.EvidenceSHA256) || lookup.ResultSHA256 != result.ResultSHA256 {
				return outcome, provider.ErrProviderInvalidResult
			}
			evidence := sha256String(lookup.EvidenceSHA256 + ":" + result.EvidenceSHA256 + ":" + usage.EvidenceSHA256)
			if !validQuestionTurnReplyContent(result.Content) {
				receipt, observation, err := c.persistObservation(
					ctx, job.ID, intent.ID, domain.JobQuestionTurnFailed, usage.Tokens, "",
					lookup.ReceiptID, evidence,
				)
				if err != nil {
					return outcome, err
				}
				outcome.ObservationReceipt, outcome.Observation = receipt, observation
				return outcome, provider.ErrProviderInvalidResult
			}
			receipt, observation, err := c.persistObservation(
				ctx, job.ID, intent.ID, domain.JobQuestionTurnCompleted, usage.Tokens, result.Content,
				lookup.ReceiptID, evidence,
			)
			if err != nil {
				return outcome, err
			}
			outcome.ObservationReceipt, outcome.Observation = receipt, observation
			reply, err := c.persistQuestionReply(ctx, job.ID, grant, question, intent, observation)
			outcome.Reply = reply
			return outcome, err
		}
	}

	currentJob, err := c.store.GetJob(ctx, intent.JobID)
	if err != nil {
		return outcome, err
	}
	if err := currentJob.Validate(); err != nil {
		return outcome, fmt.Errorf("validate Job before retrying UNKNOWN Planner question turn: %w", err)
	}
	currentGrant, found := findJobQuestionAuthorization(currentJob, grant.ID)
	if !found {
		return outcome, errors.New("JobQuestionAuthorization disappeared before retry")
	}
	currentQuestion, found := findJobMessage(currentJob, question.ID)
	if !found {
		return outcome, errors.New("source Question disappeared before retry")
	}
	now := c.now().UTC().Round(0)
	if err := currentGrant.ValidateFor(currentJob, currentQuestion.ID, currentGrant.PlannerRoleExecutionID, currentGrant.ProviderSessionID, now); err != nil {
		return outcome, err
	}
	if latest, found := latestJobQuestionTurnObservation(currentJob, intent.ID); found {
		outcome.Observation = latest
		switch latest.Status {
		case domain.JobQuestionTurnCompleted:
			reply, replyErr := c.persistQuestionReply(ctx, intent.JobID, currentGrant, currentQuestion, intent, latest)
			outcome.Reply = reply
			return outcome, replyErr
		case domain.JobQuestionTurnFailed:
			return outcome, errors.New("Planner question turn completed with a Provider failure")
		}
	}
	currentCapabilities, err := c.verifiedCapabilities(ctx, currentGrant)
	if err != nil {
		return outcome, err
	}
	requireSendIdempotency := !lookupConfirmedNotFound ||
		!currentCapabilities.HasEvidenceAtLeast(provider.CapabilitySendLookup, c.minimumEvidence)
	if requireSendIdempotency &&
		!currentCapabilities.HasEvidenceAtLeast(provider.CapabilitySendIdempotency, c.minimumEvidence) {
		return outcome, provider.ErrUnsupportedCapability
	}
	return c.dispatch(ctx, outcome, currentGrant, currentQuestion, intent, currentCapabilities, requireSendIdempotency)
}

func (c *JobQuestionTurnController) dispatch(
	ctx context.Context,
	outcome JobQuestionTurnOutcome,
	grant domain.JobQuestionAuthorization,
	question domain.JobMessage,
	intent domain.JobQuestionTurnIntent,
	capabilities provider.Capabilities,
	requireSendIdempotency bool,
) (JobQuestionTurnOutcome, error) {
	if !capabilities.HasEvidenceAtLeast(provider.CapabilityReadOnlyContextEnforcement, c.minimumEvidence) {
		return outcome, provider.ErrUnsupportedCapability
	}
	prompt := jobQuestionTurnPrompt(question.Body)
	request := provider.TurnRequest{
		OperationID:  intent.OperationID,
		TurnID:       intent.ID,
		SessionID:    intent.ProviderSessionID,
		PromptSHA256: intent.PromptSHA256,
		Prompt:       prompt,
		ReadOnlyBoundary: provider.ReadOnlyContextBoundary{
			Mode:      string(grant.ReadOnlyBoundary.Mode),
			Reference: grant.ReadOnlyBoundary.Reference,
			SHA256:    grant.ReadOnlyBoundary.SHA256,
		},
		RequireSendIdempotency: requireSendIdempotency,
		MaxTokens:              intent.ReservedTokens,
		Deadline:               intent.Deadline,
	}
	if err := request.Validate(); err != nil {
		return outcome, fmt.Errorf("validate bounded Planner question Provider Turn: %w", err)
	}
	job, err := c.store.GetJob(ctx, intent.JobID)
	if err != nil {
		return outcome, err
	}
	if err := job.Validate(); err != nil {
		return outcome, fmt.Errorf("validate Job immediately before Provider Send: %w", err)
	}
	currentGrant, found := findJobQuestionAuthorization(job, grant.ID)
	if !found {
		return outcome, errors.New("JobQuestionAuthorization disappeared before Provider Send")
	}
	if err := currentGrant.ValidateFor(
		job, question.ID, currentGrant.PlannerRoleExecutionID, currentGrant.ProviderSessionID, c.now().UTC().Round(0),
	); err != nil {
		return outcome, err
	}
	for _, message := range job.JobMessages {
		if message.ReplyToMessageID == question.ID &&
			message.Sender == (domain.ActorRef{Kind: domain.ActorPlanner, ID: question.Recipient.ID}) &&
			message.Recipient == question.Sender {
			outcome.Reply = CommunicationOutcome{Message: message}
			return outcome, nil
		}
	}
	result, err := c.runtime.Send(ctx, request)
	if err != nil {
		if providerOperationNotDispatched(err) && !requireSendIdempotency {
			var operationError *provider.OperationError
			code := provider.OperationCodeUnavailable
			if errors.As(err, &operationError) && operationError != nil && operationError.Code != "" {
				code = operationError.Code
			}
			evidence := sha256String("taxion:job-question-turn:not-dispatched:" + intent.OperationID + ":" + code)
			receipt, observation, persistErr := c.persistObservation(
				ctx, intent.JobID, intent.ID, domain.JobQuestionTurnNotDispatched, 0, "", "", evidence,
			)
			if persistErr != nil {
				return outcome, errors.Join(err, fmt.Errorf("persist NOT_DISPATCHED Planner question turn: %w", persistErr))
			}
			outcome.ObservationReceipt, outcome.Observation = receipt, observation
			return outcome, err
		}
		if providerOperationMayHaveStarted(err) {
			receipt, observation, persistErr := c.persistObservation(
				ctx, intent.JobID, intent.ID, domain.JobQuestionTurnUnknown, 0, "", "", "",
			)
			if persistErr != nil {
				persistErr = fmt.Errorf("persist UNKNOWN Planner question turn after ambiguous Send: %w", persistErr)
				latestJob, readErr := c.store.GetJob(ctx, intent.JobID)
				if readErr != nil {
					return outcome, errors.Join(persistErr, fmt.Errorf("reload Job after concurrent Provider Turn completion: %w", readErr))
				}
				if validationErr := latestJob.Validate(); validationErr != nil {
					return outcome, errors.Join(persistErr, fmt.Errorf("validate Job after concurrent Provider Turn completion: %w", validationErr))
				}
				if latest, found := latestJobQuestionTurnObservation(latestJob, intent.ID); found {
					outcome.Observation = latest
					switch latest.Status {
					case domain.JobQuestionTurnCompleted:
						reply, replyErr := c.persistQuestionReply(ctx, intent.JobID, grant, question, intent, latest)
						if replyErr != nil {
							return outcome, replyErr
						}
						outcome.Reply = reply
						return outcome, nil
					case domain.JobQuestionTurnFailed:
						return outcome, errors.New("Planner question Provider operation failed")
					}
				}
				return outcome, persistErr
			}
			outcome.ObservationReceipt, outcome.Observation = receipt, observation
			if observation.Status == domain.JobQuestionTurnCompleted {
				reply, replyErr := c.persistQuestionReply(ctx, intent.JobID, grant, question, intent, observation)
				outcome.Reply = reply
				return outcome, replyErr
			}
			if observation.Status == domain.JobQuestionTurnFailed {
				return outcome, errors.New("Planner question Provider operation failed")
			}
		}
		return outcome, err
	}
	lookup := provider.LookupResult{
		Found: true, OperationID: result.OperationID, Status: result.Status,
		ReceiptID: result.ReceiptID, SessionID: result.SessionID, TurnID: result.TurnID,
		ResultSHA256: sha256String(result.Output), EvidenceSHA256: result.EvidenceSHA256,
	}
	if err := lookup.Validate(provider.LookupRequest{
		OperationID: intent.OperationID, SessionID: intent.ProviderSessionID, TurnID: intent.ID,
	}); err != nil || result.TokensUsed > intent.ReservedTokens {
		receipt, observation, persistErr := c.persistObservation(
			ctx, intent.JobID, intent.ID, domain.JobQuestionTurnUnknown, 0, "", "", "",
		)
		if persistErr != nil {
			return outcome, fmt.Errorf("persist UNKNOWN after invalid Provider Turn: %w", persistErr)
		}
		outcome.ObservationReceipt, outcome.Observation = receipt, observation
		if err != nil {
			return outcome, fmt.Errorf("Provider returned an invalid Planner question Turn: %w", err)
		}
		return outcome, provider.ErrProviderInvalidResult
	}
	if result.Status != domain.JobQuestionTurnCompleted && result.Status != domain.JobQuestionTurnFailed {
		receipt, observation, persistErr := c.persistObservation(
			ctx, intent.JobID, intent.ID, domain.JobQuestionTurnUnknown, 0, "", "", "",
		)
		if persistErr != nil {
			return outcome, persistErr
		}
		outcome.ObservationReceipt, outcome.Observation = receipt, observation
		return outcome, errors.New("Planner question Provider Turn is not terminal")
	}
	observationStatus := result.Status
	observationContent := result.Output
	if observationStatus == domain.JobQuestionTurnFailed {
		observationContent = ""
	} else if !validQuestionTurnReplyContent(observationContent) {
		observationStatus = domain.JobQuestionTurnFailed
		observationContent = ""
	}
	receipt, observation, err := c.persistObservation(
		ctx, intent.JobID, intent.ID, observationStatus, result.TokensUsed, observationContent,
		result.ReceiptID, result.EvidenceSHA256,
	)
	if err != nil {
		return outcome, err
	}
	outcome.ObservationReceipt, outcome.Observation = receipt, observation
	if observation.Status == domain.JobQuestionTurnFailed {
		if result.Status == domain.JobQuestionTurnCompleted {
			return outcome, provider.ErrProviderInvalidResult
		}
		return outcome, errors.New("Planner question Provider Turn failed")
	}
	reply, err := c.persistQuestionReply(ctx, intent.JobID, grant, question, intent, observation)
	outcome.Reply = reply
	return outcome, err
}

func (c *JobQuestionTurnController) persistObservation(
	ctx context.Context,
	jobID, intentID, status string,
	tokens uint64,
	content, receiptID, evidenceSHA256 string,
) (store.CommandReceipt, domain.JobQuestionTurnObservation, error) {
	var empty domain.JobQuestionTurnObservation
	for attempt := 0; attempt < 2; attempt++ {
		job, err := c.store.GetJob(ctx, jobID)
		if err != nil {
			return store.CommandReceipt{}, empty, err
		}
		previous, found := latestJobQuestionTurnObservation(job, intentID)
		if found {
			if previous.Status == domain.JobQuestionTurnUnknown && status == domain.JobQuestionTurnUnknown {
				return store.CommandReceipt{}, previous, nil
			}
			if previous.Status == domain.JobQuestionTurnNotDispatched {
				if status == domain.JobQuestionTurnNotDispatched && previous.TokensUsed == tokens &&
					previous.Content == content && previous.ReceiptID == receiptID &&
					previous.EvidenceSHA256 == evidenceSHA256 {
					return store.CommandReceipt{}, previous, nil
				}
			} else if previous.Status != domain.JobQuestionTurnUnknown {
				if previous.Status == status && previous.TokensUsed == tokens &&
					previous.Content == content && previous.ReceiptID == receiptID {
					return store.CommandReceipt{}, previous, nil
				}
				if status == domain.JobQuestionTurnUnknown {
					return store.CommandReceipt{}, previous, nil
				}
				return store.CommandReceipt{}, empty, errors.New("terminal Planner question observation cannot be replaced")
			}
		}
		reconcilesUnknown := found && previous.Status == domain.JobQuestionTurnUnknown
		commandID := jobQuestionTurnObservationCommandID(
			intentID, status, tokens, content, receiptID, evidenceSHA256, reconcilesUnknown,
		)
		request := domain.JobQuestionTurnObservationRequest{
			IntentID: intentID, CommandID: commandID, Status: status, TokensUsed: tokens,
			Content: content, ReceiptID: receiptID, EvidenceSHA256: evidenceSHA256,
			ReconcilesUnknown: reconcilesUnknown,
		}
		receipt, observation, err := c.store.AddJobQuestionTurnObservation(
			ctx, jobID, job.Revision, request,
		)
		if err == nil {
			return receipt, observation, nil
		}
		if !errors.Is(err, store.ErrIdempotencyConflict) || attempt != 0 {
			return store.CommandReceipt{}, empty, err
		}
	}
	return store.CommandReceipt{}, empty, store.ErrIdempotencyConflict
}

func (c *JobQuestionTurnController) persistQuestionReply(
	ctx context.Context,
	jobID string,
	grant domain.JobQuestionAuthorization,
	question domain.JobMessage,
	intent domain.JobQuestionTurnIntent,
	observation domain.JobQuestionTurnObservation,
) (CommunicationOutcome, error) {
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return CommunicationOutcome{}, err
	}
	if reply, found := existingPlannerReply(job, question); found {
		return CommunicationOutcome{Message: reply}, nil
	}
	return c.communication.SendFromRole(ctx, RoleMessageRequest{
		JobID:             jobID,
		RoleExecutionID:   grant.PlannerRoleExecutionID,
		ProviderSessionID: grant.ProviderSessionID,
		CommandID:         jobQuestionReplyCommandID(intent.ID),
		Recipient:         question.Sender,
		ReplyToMessageID:  question.ID,
		Body:              observation.Content,
	})
}

func existingPlannerReply(job domain.Job, question domain.JobMessage) (domain.JobMessage, bool) {
	for _, message := range job.JobMessages {
		if message.ReplyToMessageID == question.ID &&
			message.Sender == (domain.ActorRef{Kind: domain.ActorPlanner, ID: question.Recipient.ID}) &&
			message.Recipient == question.Sender {
			return message, true
		}
	}
	return domain.JobMessage{}, false
}

func (c *JobQuestionTurnController) verifiedCapabilities(
	ctx context.Context,
	grant domain.JobQuestionAuthorization,
) (provider.Capabilities, error) {
	identity := c.runtime.Identity()
	if identity.ProviderID != grant.ProviderID || identity.AccountID != grant.AccountID ||
		identity.ConfigurationSHA256 != grant.ConfigurationSHA256 {
		return provider.Capabilities{}, errors.New("Provider identity differs from JobQuestionAuthorization")
	}
	capabilityCtx, cancel := context.WithTimeout(ctx, plannerQuestionRecoveryCallTimeout)
	defer cancel()
	capabilities, err := c.runtime.Capabilities(capabilityCtx)
	if err != nil {
		return provider.Capabilities{}, err
	}
	if err := capabilities.Validate(identity); err != nil {
		return provider.Capabilities{}, fmt.Errorf("validate question Provider capabilities: %w", err)
	}
	return capabilities, nil
}

func findJobQuestionAuthorization(job domain.Job, id string) (domain.JobQuestionAuthorization, bool) {
	for _, grant := range job.JobQuestionAuthorizations {
		if grant.ID == id {
			return grant, true
		}
	}
	return domain.JobQuestionAuthorization{}, false
}

func findJobMessage(job domain.Job, id string) (domain.JobMessage, bool) {
	for _, message := range job.JobMessages {
		if message.ID == id {
			return message, true
		}
	}
	return domain.JobMessage{}, false
}

func findJobQuestionTurnIntentByCommand(job domain.Job, commandID string) (domain.JobQuestionTurnIntent, bool) {
	for _, intent := range job.JobQuestionTurnIntents {
		if intent.CommandID == commandID {
			return intent, true
		}
	}
	return domain.JobQuestionTurnIntent{}, false
}

func latestJobQuestionTurnObservation(job domain.Job, intentID string) (domain.JobQuestionTurnObservation, bool) {
	var latest domain.JobQuestionTurnObservation
	found := false
	for _, observation := range job.JobQuestionTurnObservations {
		if observation.IntentID == intentID && (!found || observation.CreatedRevision > latest.CreatedRevision) {
			latest, found = observation, true
		}
	}
	return latest, found
}

func jobQuestionTurnPrompt(question string) string {
	return "Answer the following question using only the authorized read-only context. Do not modify resources or perform external side effects.\n\n" + question
}

func jobQuestionTurnObservationCommandID(
	intentID, status string, tokens uint64, content, receiptID, evidenceSHA256 string, reconcilesUnknown bool,
) string {
	material := fmt.Sprintf(
		"taxion:job-question-turn-observation:%s:%s:%d:%s:%s:%s:%t",
		intentID, status, tokens, sha256String(content), receiptID, evidenceSHA256, reconcilesUnknown,
	)
	return "cmd_" + sha256String(material)[:32]
}

func jobQuestionReplyCommandID(intentID string) string {
	return "cmd_" + sha256String("taxion:job-question-reply:v1:" + intentID)[:32]
}

func validQuestionTurnEvidence(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return false
		}
	}
	return true
}

func validQuestionTurnReplyContent(content string) bool {
	return utf8.ValidString(content) &&
		strings.TrimSpace(content) != "" &&
		len([]byte(content)) <= domain.MaxJobMessageBodyBytes
}
