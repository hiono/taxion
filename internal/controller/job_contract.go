package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hiono/taxion/internal/auth"
	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/store"
)

type JobContractProposalStore interface {
	GetJob(context.Context, string) (domain.Job, error)
	AddJobContractRevision(context.Context, string, uint64, string, string, string, string, domain.JobContract) (store.CommandReceipt, domain.JobContractRevision, error)
	AddJobApproval(context.Context, string, uint64, string, uint64, string, uint32, string, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.JobApproval, error)
	AmendJobContract(context.Context, string, uint64, string, uint64, string, string, domain.JobContract, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.JobContractRevision, error)
}

type JobContractController struct {
	store JobContractProposalStore
	now   func() time.Time
}

func NewJobContractController(proposalStore JobContractProposalStore) (*JobContractController, error) {
	if proposalStore == nil {
		return nil, errors.New("JobContractProposalStore is required")
	}
	return &JobContractController{
		store: proposalStore,
		now:   func() time.Time { return time.Now().UTC().Round(0) },
	}, nil
}

// Keeping the proposal bound to the persisted Result prevents callers from substituting an unrelated contract payload.
func (c *JobContractController) Propose(
	ctx context.Context,
	jobID, roleExecutionID, sourceResultObservationID, commandID string,
	principal auth.AuthenticatedPrincipal,
) (store.CommandReceipt, domain.JobContractRevision, error) {
	if err := principal.Validate(); err != nil {
		return store.CommandReceipt{}, domain.JobContractRevision{}, fmt.Errorf("validate authenticated HUMAN Principal: %w", err)
	}
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return store.CommandReceipt{}, domain.JobContractRevision{}, err
	}

	var execution domain.RoleExecution
	foundExecution := false
	for _, candidate := range job.RoleExecutions {
		if candidate.ID == roleExecutionID {
			execution, foundExecution = candidate, true
			break
		}
	}
	if !foundExecution || execution.JobID != job.ID ||
		execution.Role != domain.RolePlanner || execution.TaskID != "" {
		return store.CommandReceipt{}, domain.JobContractRevision{}, errors.New("JobContract proposal requires a Taskless PLANNER RoleExecution in the target Job")
	}

	var grant domain.PlanningAuthorization
	foundGrant := false
	for _, candidate := range job.PlanningAuthorizations {
		if candidate.ID == execution.PlanningAuthorizationID {
			grant, foundGrant = candidate, true
			break
		}
	}
	if !foundGrant {
		return store.CommandReceipt{}, domain.JobContractRevision{}, errors.New("Planner PlanningAuthorization was not found")
	}
	if err := grant.Validate(job); err != nil {
		return store.CommandReceipt{}, domain.JobContractRevision{}, fmt.Errorf("validate Planner PlanningAuthorization: %w", err)
	}
	if grant.PrincipalID != principal.PrincipalID() {
		return store.CommandReceipt{}, domain.JobContractRevision{}, errors.New("JobContract proposal HUMAN Principal differs from PlanningAuthorization")
	}
	var sourceResult *domain.ProviderResultObservation
	for i := range job.ProviderResultObservations {
		candidate := &job.ProviderResultObservations[i]
		if candidate.ID == sourceResultObservationID {
			sourceResult = candidate
			break
		}
	}
	if sourceResult == nil || sourceResult.JobID != job.ID ||
		sourceResult.RoleExecutionID != roleExecutionID || !sourceResult.Final || sourceResult.TurnID == "" {
		return store.CommandReceipt{}, domain.JobContractRevision{}, errors.New("JobContract proposal requires the exact final Result from the same Planner RoleExecution")
	}
	if err := sourceResult.Validate(job); err != nil {
		return store.CommandReceipt{}, domain.JobContractRevision{}, fmt.Errorf("validate Planner source Result: %w", err)
	}
	contract, err := decodeJobContractJSON(sourceResult.Content)
	if err != nil {
		return store.CommandReceipt{}, domain.JobContractRevision{}, fmt.Errorf("decode Planner JobContract Result: %w", err)
	}

	// A committed proposal may be replayed after its grant expires. The Store
	// verifies the original CommandID and canonical request hash before returning
	// the original receipt; no new revision is created on that path.
	for _, existing := range job.JobContractRevisions {
		if existing.CommandID == commandID && (existing.ProposedByRoleExecutionID != roleExecutionID ||
			existing.SourceResultObservationID != sourceResultObservationID ||
			existing.SourceResultContentSHA256 != sourceResult.ContentSHA256) {
			return store.CommandReceipt{}, domain.JobContractRevision{}, store.ErrIdempotencyConflict
		}
	}
	existingProposal := false
	for _, existing := range job.JobContractRevisions {
		if existing.CommandID == commandID {
			existingProposal = true
			break
		}
	}
	if !existingProposal {
		if job.State != domain.JobDraft {
			return store.CommandReceipt{}, domain.JobContractRevision{}, errors.New("JobContract proposal requires a DRAFT Job")
		}
		if err := grant.ValidateFor(job, principal.PrincipalID(), c.now()); err != nil {
			return store.CommandReceipt{}, domain.JobContractRevision{}, fmt.Errorf("Planner authority is not current: %w", err)
		}
		acceptedStart := false
		for _, observation := range job.ProviderStartObservations {
			if observation.RoleExecutionID == execution.ID &&
				observation.Status == domain.ProviderStartAccepted &&
				!observation.CreatedAt.Before(execution.CreatedAt) {
				acceptedStart = true
				break
			}
		}
		if !acceptedStart {
			return store.CommandReceipt{}, domain.JobContractRevision{}, errors.New("JobContract proposal requires an accepted Provider Start observation")
		}
	}

	return c.store.AddJobContractRevision(
		ctx, jobID, job.Revision, commandID, roleExecutionID,
		sourceResultObservationID, sourceResult.ContentSHA256, contract,
	)
}

var plannerJobContractJSONFields = map[string]struct{}{
	"purpose": {}, "constraints": {}, "target_repositories": {}, "target_resources": {},
	"workspaces": {}, "acceptance_criteria": {}, "required_evidence": {}, "budget": {},
	"concurrency_ceiling": {}, "deadline": {}, "prohibited_effects": {},
	"approval_required_effects": {}, "configuration_sha256": {},
}

var plannerJobContractBudgetJSONFields = map[string]struct{}{
	"max_turns": {}, "max_tokens": {},
}

func decodeJobContractJSON(content string) (domain.JobContract, error) {
	if len(content) > 1<<20 || !utf8.ValidString(content) {
		return domain.JobContract{}, errors.New("JobContract JSON must be valid UTF-8 and not exceed 1 MiB")
	}
	decoder := json.NewDecoder(strings.NewReader(content))
	root, err := decoder.Token()
	if err != nil {
		return domain.JobContract{}, fmt.Errorf("read JSON root: %w", err)
	}
	if delimiter, ok := root.(json.Delim); !ok || delimiter != '{' {
		return domain.JobContract{}, errors.New("JobContract JSON must be one object")
	}
	if err := consumePlannerJobContractObject(decoder, plannerJobContractJSONFields); err != nil {
		return domain.JobContract{}, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return domain.JobContract{}, errors.New("JobContract JSON contains trailing JSON")
		}
		return domain.JobContract{}, fmt.Errorf("read trailing JSON: %w", err)
	}

	decoder = json.NewDecoder(strings.NewReader(content))
	decoder.DisallowUnknownFields()
	var contract domain.JobContract
	if err := decoder.Decode(&contract); err != nil {
		return domain.JobContract{}, fmt.Errorf("decode JobContract object: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return domain.JobContract{}, errors.New("JobContract JSON contains more than one JSON value")
		}
		return domain.JobContract{}, fmt.Errorf("read trailing JSON: %w", err)
	}
	return contract, nil
}

func consumePlannerJobContractObject(decoder *json.Decoder, allowed map[string]struct{}) error {
	seen := make(map[string]struct{})
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("read JSON object key: %w", err)
		}
		key, ok := token.(string)
		if !ok {
			return errors.New("JobContract JSON object key is not a string")
		}
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("JobContract JSON contains unknown or non-canonical JSON key %q", key)
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("JobContract JSON contains duplicate JSON key %q", key)
		}
		seen[key] = struct{}{}
		var nested map[string]struct{}
		if key == "budget" {
			nested = plannerJobContractBudgetJSONFields
		}
		if err := consumePlannerJobContractValue(decoder, nested); err != nil {
			return err
		}
	}
	closing, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("close JSON object: %w", err)
	}
	if delimiter, ok := closing.(json.Delim); !ok || delimiter != '}' {
		return errors.New("JobContract JSON object is not closed")
	}
	return nil
}

func consumePlannerJobContractValue(decoder *json.Decoder, objectFields map[string]struct{}) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("read JSON value: %w", err)
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		return consumePlannerJobContractObject(decoder, objectFields)
	case '[':
		for decoder.More() {
			if err := consumePlannerJobContractValue(decoder, nil); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("close JSON array: %w", err)
		}
		if closing != json.Delim(']') {
			return errors.New("JobContract JSON array is not closed")
		}
		return nil
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
}

// Approve records an authenticated human decision for one exact JobContract revision.
// It does not create ExecutionPermission or authorize Provider execution.
// Amend requires a fresh HUMAN command because the DRAFT Planner proposal authority cannot change ACTIVE scope.
func (c *JobContractController) AmendJSON(
	ctx context.Context,
	jobID string,
	expectedJobRevision uint64,
	commandID string,
	basedOnRevision uint64,
	basedOnCanonicalHash, reason, contractJSON string,
	principal auth.AuthenticatedPrincipal,
) (store.CommandReceipt, domain.JobContractRevision, error) {
	contract, err := decodeJobContractJSON(contractJSON)
	if err != nil {
		return store.CommandReceipt{}, domain.JobContractRevision{}, fmt.Errorf("decode human JobContract amendment: %w", err)
	}
	return c.Amend(ctx, jobID, expectedJobRevision, commandID, basedOnRevision, basedOnCanonicalHash, reason, contract, principal)
}

func (c *JobContractController) Amend(
	ctx context.Context,
	jobID string,
	expectedJobRevision uint64,
	commandID string,
	basedOnRevision uint64,
	basedOnCanonicalHash, reason string,
	contract domain.JobContract,
	principal auth.AuthenticatedPrincipal,
) (store.CommandReceipt, domain.JobContractRevision, error) {
	if err := principal.Validate(); err != nil {
		return store.CommandReceipt{}, domain.JobContractRevision{}, fmt.Errorf("validate amending Principal: %w", err)
	}
	if !strings.HasPrefix(principal.PrincipalID(), "human_") {
		return store.CommandReceipt{}, domain.JobContractRevision{}, errors.New("JobContract amendment requires a HUMAN Principal")
	}
	return c.store.AmendJobContract(ctx, jobID, expectedJobRevision, commandID, basedOnRevision, basedOnCanonicalHash, reason, contract, principal)
}

func (c *JobContractController) Approve(
	ctx context.Context,
	jobID, commandID string,
	contractRevision uint64,
	canonicalHash string,
	canonicalizationVersion uint32,
	reason string,
	principal auth.AuthenticatedPrincipal,
) (store.CommandReceipt, domain.JobApproval, error) {
	if err := principal.Validate(); err != nil {
		return store.CommandReceipt{}, domain.JobApproval{}, fmt.Errorf("validate authenticated HUMAN Principal: %w", err)
	}
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return store.CommandReceipt{}, domain.JobApproval{}, err
	}

	// Exact retries remain readable after ACTIVE or a later revision. The Store
	// checks the full command payload and returns the original receipt or conflict.
	for _, existing := range job.JobApprovals {
		if existing.CommandID == commandID {
			if existing.ApproverPrincipalID != principal.PrincipalID() {
				return store.CommandReceipt{}, domain.JobApproval{}, store.ErrIdempotencyConflict
			}
			return c.store.AddJobApproval(
				ctx, jobID, job.Revision, commandID, contractRevision,
				canonicalHash, canonicalizationVersion, reason, principal,
			)
		}
	}

	if len(job.JobContractRevisions) == 0 {
		return store.CommandReceipt{}, domain.JobApproval{}, errors.New("JobApproval requires a JobContract revision")
	}
	latest := job.JobContractRevisions[len(job.JobContractRevisions)-1]
	if contractRevision != latest.Revision || canonicalHash != latest.CanonicalHash ||
		canonicalizationVersion != latest.CanonicalizationVersion {
		return store.CommandReceipt{}, domain.JobApproval{}, errors.New("approval must identify the exact latest JobContract revision and canonical hash")
	}
	if job.State != domain.JobDraft {
		return store.CommandReceipt{}, domain.JobApproval{}, errors.New("new JobApproval requires a DRAFT Job")
	}
	for _, existing := range job.JobApprovals {
		if existing.ContractRevision == contractRevision {
			return store.CommandReceipt{}, domain.JobApproval{}, errors.New("JobContract revision already has a JobApproval")
		}
	}

	return c.store.AddJobApproval(
		ctx, jobID, job.Revision, commandID, contractRevision,
		canonicalHash, canonicalizationVersion, reason, principal,
	)
}
