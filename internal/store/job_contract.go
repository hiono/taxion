package store

import (
	"context"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/auth"
	"github.com/hiono/taxion/internal/domain"
)

func (s *Store) AddJobContractRevision(ctx context.Context, jobID string, expectedRevision uint64, commandID, roleExecutionID string, contract domain.JobContract) (CommandReceipt, domain.JobContractRevision, error) {
	canonical, _, err := domain.CanonicalizeJobContract(contract, domain.JobContractCanonicalizationVersion)
	if err != nil {
		return CommandReceipt{}, domain.JobContractRevision{}, err
	}
	request := struct {
		RoleExecutionID string             `json:"role_execution_id"`
		Contract        domain.JobContract `json:"contract"`
	}{RoleExecutionID: roleExecutionID, Contract: canonical}
	return persistPlanningFact(
		ctx, s, jobID, expectedRevision, commandID, "JOB_CONTRACT_REVISION", request,
		func(job domain.Job, now time.Time) (domain.JobContractRevision, error) {
			return domain.NewJobContractRevision(job, commandID, roleExecutionID, canonical, now)
		},
		func(job *domain.Job, revision domain.JobContractRevision) {
			job.JobContractRevisions = append(job.JobContractRevisions, revision)
			job.Goal = revision.Contract.Purpose
			job.State = domain.JobDraft
		},
		func(job domain.Job) (domain.JobContractRevision, bool) {
			for _, revision := range job.JobContractRevisions {
				if revision.CommandID == commandID {
					return revision, true
				}
			}
			return domain.JobContractRevision{}, false
		},
	)
}

func (s *Store) AddJobApproval(ctx context.Context, jobID string, expectedRevision uint64, commandID string, contractRevision uint64, canonicalHash string, canonicalizationVersion uint32, reason string, principal auth.AuthenticatedPrincipal) (CommandReceipt, domain.JobApproval, error) {
	reason = strings.TrimSpace(reason)
	request := struct {
		ContractRevision        uint64 `json:"contract_revision"`
		CanonicalHash           string `json:"canonical_hash"`
		CanonicalizationVersion uint32 `json:"canonicalization_version"`
		Reason                  string `json:"reason"`
		ApproverPrincipalID     string `json:"approver_principal_id"`
		ApproverSource          string `json:"approver_source"`
		ApproverSubject         string `json:"approver_subject"`
	}{
		ContractRevision: contractRevision, CanonicalHash: canonicalHash,
		CanonicalizationVersion: canonicalizationVersion, Reason: reason,
		ApproverPrincipalID: principal.PrincipalID(), ApproverSource: principal.Issuer(),
		ApproverSubject: principal.Subject(),
	}
	return persistPlanningFact(
		ctx, s, jobID, expectedRevision, commandID, "JOB_APPROVAL", request,
		func(job domain.Job, now time.Time) (domain.JobApproval, error) {
			return domain.NewJobApproval(job, commandID, contractRevision, canonicalHash, canonicalizationVersion, reason, principal, now)
		},
		func(job *domain.Job, approval domain.JobApproval) {
			job.JobApprovals = append(job.JobApprovals, approval)
			job.State = domain.JobActive
		},
		func(job domain.Job) (domain.JobApproval, bool) {
			for _, approval := range job.JobApprovals {
				if approval.CommandID == commandID {
					return approval, true
				}
			}
			return domain.JobApproval{}, false
		},
	)
}
