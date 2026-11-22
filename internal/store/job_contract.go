package store

import (
	"context"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/auth"
	"github.com/hiono/taxion/internal/domain"
)

func (s *Store) AddJobContractRevision(ctx context.Context, jobID string, expectedRevision uint64, commandID, roleExecutionID, sourceResultObservationID, sourceResultContentSHA256 string, contract domain.JobContract) (CommandReceipt, domain.JobContractRevision, error) {
	canonical, _, err := domain.CanonicalizeJobContract(contract, domain.JobContractCanonicalizationVersion)
	if err != nil {
		return CommandReceipt{}, domain.JobContractRevision{}, err
	}
	request := struct {
		RoleExecutionID           string             `json:"role_execution_id"`
		SourceResultObservationID string             `json:"source_result_observation_id"`
		SourceResultContentSHA256 string             `json:"source_result_content_sha256"`
		Contract                  domain.JobContract `json:"contract"`
	}{RoleExecutionID: roleExecutionID, SourceResultObservationID: sourceResultObservationID, SourceResultContentSHA256: sourceResultContentSHA256, Contract: canonical}
	return persistPlanningFact(
		ctx, s, jobID, expectedRevision, commandID, "JOB_CONTRACT_REVISION", request,
		func(job domain.Job, now time.Time) (domain.JobContractRevision, error) {
			return domain.NewJobContractRevision(job, commandID, roleExecutionID, sourceResultObservationID, sourceResultContentSHA256, canonical, now)
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

func (s *Store) AmendJobContract(
	ctx context.Context,
	jobID string,
	expectedRevision uint64,
	commandID string,
	basedOnRevision uint64,
	basedOnCanonicalHash, reason string,
	contract domain.JobContract,
	principal auth.AuthenticatedPrincipal,
) (CommandReceipt, domain.JobContractRevision, error) {
	reason = strings.TrimSpace(reason)
	canonical, _, err := domain.CanonicalizeJobContract(contract, domain.JobContractCanonicalizationVersion)
	if err != nil {
		return CommandReceipt{}, domain.JobContractRevision{}, err
	}
	request := struct {
		BasedOnRevision      uint64             `json:"based_on_revision"`
		BasedOnCanonicalHash string             `json:"based_on_canonical_hash"`
		Reason               string             `json:"reason"`
		Contract             domain.JobContract `json:"contract"`
		PrincipalID          string             `json:"principal_id"`
		PrincipalIssuer      string             `json:"principal_issuer"`
		PrincipalSubject     string             `json:"principal_subject"`
	}{
		BasedOnRevision: basedOnRevision, BasedOnCanonicalHash: basedOnCanonicalHash,
		Reason: reason, Contract: canonical,
		PrincipalID: principal.PrincipalID(), PrincipalIssuer: principal.Issuer(),
		PrincipalSubject: principal.Subject(),
	}
	return persistPlanningFact(
		ctx, s, jobID, expectedRevision, commandID, "JOB_CONTRACT_AMENDMENT", request,
		func(job domain.Job, now time.Time) (domain.JobContractRevision, error) {
			return domain.NewJobContractAmendmentRevision(job, commandID, basedOnRevision, basedOnCanonicalHash, reason, principal, canonical, now)
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

func (s *Store) AddExecutionPermission(
	ctx context.Context,
	expectedRevision uint64,
	request domain.ExecutionPermissionRequest,
	requestSHA256 string,
	assessment domain.ExecutionEffectAssessment,
) (CommandReceipt, domain.ExecutionPermission, error) {
	command := struct {
		Request       domain.ExecutionPermissionRequest `json:"request"`
		RequestSHA256 string                            `json:"request_sha256"`
		Assessment    domain.ExecutionEffectAssessment  `json:"assessment"`
	}{Request: request, RequestSHA256: requestSHA256, Assessment: assessment}
	return persistPlanningFact(
		ctx, s, request.JobID, expectedRevision, request.CommandID, "EXECUTION_PERMISSION", command,
		func(job domain.Job, now time.Time) (domain.ExecutionPermission, error) {
			return domain.NewExecutionPermission(job, request, requestSHA256, assessment, now)
		},
		func(job *domain.Job, permission domain.ExecutionPermission) {
			job.ExecutionPermissions = append(job.ExecutionPermissions, permission)
		},
		func(job domain.Job) (domain.ExecutionPermission, bool) {
			for _, permission := range job.ExecutionPermissions {
				if permission.CommandID == request.CommandID {
					return permission, true
				}
			}
			return domain.ExecutionPermission{}, false
		},
	)
}
