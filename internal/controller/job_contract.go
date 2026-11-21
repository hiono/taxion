package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hiono/taxion/internal/auth"
	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/store"
)

type JobContractProposalStore interface {
	GetJob(context.Context, string) (domain.Job, error)
	AddJobContractRevision(context.Context, string, uint64, string, string, domain.JobContract) (store.CommandReceipt, domain.JobContractRevision, error)
	AddJobApproval(context.Context, string, uint64, string, uint64, string, uint32, string, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.JobApproval, error)
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

// Propose appends a JobContract revision from the currently authorized Taskless PLANNER.
// It never grants ExecutionPermission or starts a Provider operation.
func (c *JobContractController) Propose(
	ctx context.Context,
	jobID, roleExecutionID, commandID string,
	contract domain.JobContract,
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

	// A committed proposal may be replayed after its grant expires. The Store
	// verifies the original CommandID and canonical request hash before returning
	// the original receipt; no new revision is created on that path.
	for _, existing := range job.JobContractRevisions {
		if existing.CommandID == commandID && existing.ProposedByRoleExecutionID != roleExecutionID {
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
		ctx, jobID, job.Revision, commandID, roleExecutionID, contract,
	)
}

// Approve records an authenticated human decision for one exact JobContract revision.
// It does not create ExecutionPermission or authorize Provider execution.
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
