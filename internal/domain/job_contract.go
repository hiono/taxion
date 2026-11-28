package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hiono/taxion/internal/auth"
	"github.com/hiono/taxion/internal/identity"
)

const JobContractCanonicalizationVersion uint32 = 1

const (
	JobContractRevisionOriginPlanner        = "PLANNER"
	JobContractRevisionOriginHumanAmendment = "HUMAN_AMENDMENT"
)

type JobContract struct {
	Purpose                 string    `json:"purpose"`
	Constraints             []string  `json:"constraints,omitempty"`
	TargetRepositories      []string  `json:"target_repositories"`
	TargetResources         []string  `json:"target_resources"`
	Workspaces              []string  `json:"workspaces"`
	AcceptanceCriteria      []string  `json:"acceptance_criteria"`
	RequiredEvidence        []string  `json:"required_evidence"`
	ConcurrencyCeiling      uint32    `json:"concurrency_ceiling"`
	Deadline                time.Time `json:"deadline"`
	ProhibitedEffects       []string  `json:"prohibited_effects,omitempty"`
	ApprovalRequiredEffects []string  `json:"approval_required_effects,omitempty"`
	ConfigurationSHA256     string    `json:"configuration_sha256"`
}

type JobContractRevision struct {
	ID                        string      `json:"id"`
	CommandID                 string      `json:"command_id"`
	JobID                     string      `json:"job_id"`
	Revision                  uint64      `json:"revision"`
	CanonicalizationVersion   uint32      `json:"canonicalization_version"`
	CanonicalHash             string      `json:"canonical_hash"`
	Contract                  JobContract `json:"contract"`
	Origin                    string      `json:"origin"`
	ProposedByRoleExecutionID string      `json:"proposed_by_role_execution_id"`
	SourceResultObservationID string      `json:"source_result_observation_id"`
	SourceResultContentSHA256 string      `json:"source_result_content_sha256"`
	AmendedByPrincipalID      string      `json:"amended_by_principal_id"`
	AmendedByIssuer           string      `json:"amended_by_issuer"`
	AmendedBySubject          string      `json:"amended_by_subject"`
	AmendedAuthenticatedAt    time.Time   `json:"amended_authenticated_at"`
	AmendmentReason           string      `json:"amendment_reason"`
	SupersedesRevision        uint64      `json:"supersedes_revision"`
	SupersedesCanonicalHash   string      `json:"supersedes_canonical_hash"`
	CreatedAt                 time.Time   `json:"created_at"`
	CreatedRevision           uint64      `json:"created_revision"`
}

type JobApproval struct {
	ID                      string    `json:"id"`
	CommandID               string    `json:"command_id"`
	JobID                   string    `json:"job_id"`
	ContractRevision        uint64    `json:"contract_revision"`
	CanonicalizationVersion uint32    `json:"canonicalization_version"`
	CanonicalHash           string    `json:"canonical_hash"`
	ApproverPrincipalID     string    `json:"approver_principal_id"`
	ApproverSource          string    `json:"approver_source"`
	ApproverSubject         string    `json:"approver_subject"`
	Reason                  string    `json:"reason"`
	ApprovedAt              time.Time `json:"approved_at"`
	CreatedRevision         uint64    `json:"created_revision"`
}

type canonicalJobContractEnvelope struct {
	CanonicalizationVersion uint32      `json:"canonicalization_version"`
	Contract                JobContract `json:"contract"`
}

func JobContractRevisionID(commandID string) string {
	return planningOperationID("job_contract_revision", commandID)
}

func JobApprovalID(commandID string) string {
	return planningOperationID("job_approval", commandID)
}

func CanonicalizeJobContract(contract JobContract, version uint32) (JobContract, string, error) {
	if version != JobContractCanonicalizationVersion {
		return JobContract{}, "", fmt.Errorf("unsupported JobContract canonicalization version %d", version)
	}
	if !utf8.ValidString(contract.Purpose) {
		return JobContract{}, "", errors.New("JobContract purpose must be valid UTF-8")
	}
	contract.Purpose = strings.TrimSpace(contract.Purpose)
	if contract.Purpose == "" || len(contract.Purpose) > 4096 {
		return JobContract{}, "", errors.New("JobContract purpose is required and must not exceed 4,096 bytes")
	}
	var err error
	if contract.Constraints, err = canonicalContractSet("constraints", contract.Constraints, false); err != nil {
		return JobContract{}, "", err
	}
	if contract.TargetRepositories, err = canonicalContractSet("target repositories", contract.TargetRepositories, false); err != nil {
		return JobContract{}, "", err
	}
	if contract.TargetResources, err = canonicalContractSet("target resources", contract.TargetResources, false); err != nil {
		return JobContract{}, "", err
	}
	if len(contract.TargetRepositories) == 0 && len(contract.TargetResources) == 0 {
		return JobContract{}, "", errors.New("JobContract requires a target repository or resource")
	}
	if contract.Workspaces, err = canonicalContractSet("workspaces", contract.Workspaces, true); err != nil {
		return JobContract{}, "", err
	}
	if contract.AcceptanceCriteria, err = canonicalContractSet("acceptance criteria", contract.AcceptanceCriteria, true); err != nil {
		return JobContract{}, "", err
	}
	if contract.RequiredEvidence, err = canonicalContractSet("required evidence", contract.RequiredEvidence, true); err != nil {
		return JobContract{}, "", err
	}
	if contract.ProhibitedEffects, err = canonicalContractSet("prohibited effects", contract.ProhibitedEffects, false); err != nil {
		return JobContract{}, "", err
	}
	if contract.ApprovalRequiredEffects, err = canonicalContractSet("approval-required effects", contract.ApprovalRequiredEffects, false); err != nil {
		return JobContract{}, "", err
	}
	if contract.ConcurrencyCeiling == 0 {
		return JobContract{}, "", errors.New("JobContract concurrency ceiling must be positive")
	}
	if contract.Deadline.IsZero() {
		return JobContract{}, "", errors.New("JobContract deadline is required")
	}
	contract.Deadline = contract.Deadline.UTC().Round(0)
	contract.ConfigurationSHA256 = strings.ToLower(strings.TrimSpace(contract.ConfigurationSHA256))
	if !validSHA256(contract.ConfigurationSHA256) {
		return JobContract{}, "", errors.New("JobContract configuration reference must be a SHA-256 digest")
	}
	encoded, err := json.Marshal(canonicalJobContractEnvelope{CanonicalizationVersion: version, Contract: contract})
	if err != nil {
		return JobContract{}, "", fmt.Errorf("encode canonical JobContract: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return contract, hex.EncodeToString(sum[:]), nil
}

func canonicalContractSet(name string, values []string, required bool) ([]string, error) {
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !utf8.ValidString(value) {
			return nil, fmt.Errorf("JobContract %s must be valid UTF-8", name)
		}
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 4096 || strings.ContainsRune(value, '\x00') {
			return nil, fmt.Errorf("JobContract %s contains an empty or invalid entry", name)
		}
		if _, exists := seen[value]; exists {
			return nil, fmt.Errorf("JobContract %s contains duplicate entry %q", name, value)
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	if required && len(normalized) == 0 {
		return nil, fmt.Errorf("JobContract %s is required", name)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func NewJobContractRevision(job Job, commandID, roleExecutionID, sourceResultObservationID, sourceResultContentSHA256 string, contract JobContract, now time.Time) (JobContractRevision, error) {
	if err := job.Validate(); err != nil {
		return JobContractRevision{}, fmt.Errorf("validate Job before contract proposal: %w", err)
	}
	if !validCommandID(commandID) || now.IsZero() || !validPlanningIdentity(roleExecutionID) {
		return JobContractRevision{}, errors.New("JobContract revision command, Planner RoleExecution, and timestamp are required")
	}
	if job.State != JobDraft {
		return JobContractRevision{}, errors.New("JobContract revision requires a DRAFT Job; ACTIVE amendments require a separate authorized revision command")
	}
	var execution RoleExecution
	foundExecution := false
	for _, candidate := range job.RoleExecutions {
		if candidate.ID == roleExecutionID {
			execution, foundExecution = candidate, true
			break
		}
	}
	startObservation, foundStart := acceptedStartForExecution(job, roleExecutionID)
	var grant PlanningAuthorization
	foundGrant := false
	for _, candidate := range job.PlanningAuthorizations {
		if foundExecution && candidate.ID == execution.PlanningAuthorizationID {
			grant, foundGrant = candidate, true
			break
		}
	}
	if !foundExecution || execution.Role != RolePlanner || execution.TaskID != "" || !foundStart ||
		!foundGrant || grant.Validate(job) != nil {
		return JobContractRevision{}, errors.New("JobContract proposal requires an accepted Taskless PLANNER execution with its PlanningAuthorization")
	}
	if !validPlanningIdentity(sourceResultObservationID) || !validSHA256(sourceResultContentSHA256) {
		return JobContractRevision{}, errors.New("JobContract proposal requires an exact Provider Result observation identity and digest")
	}
	var sourceResult *ProviderResultObservation
	for i := range job.ProviderResultObservations {
		candidate := &job.ProviderResultObservations[i]
		if candidate.ID == sourceResultObservationID {
			sourceResult = candidate
			break
		}
	}
	if sourceResult == nil || sourceResult.JobID != job.ID ||
		sourceResult.RoleExecutionID != roleExecutionID || !sourceResult.Final ||
		sourceResult.ContentSHA256 != sourceResultContentSHA256 {
		return JobContractRevision{}, errors.New("JobContract source Result must be final and belong to the same Job and Planner RoleExecution")
	}
	if err := sourceResult.Validate(job); err != nil {
		return JobContractRevision{}, fmt.Errorf("validate JobContract source Result: %w", err)
	}
	if sourceResult.TurnID == "" {
		return JobContractRevision{}, errors.New("JobContract source Result must be bound to a Planner Turn")
	}
	canonical, hash, err := CanonicalizeJobContract(contract, JobContractCanonicalizationVersion)
	if err != nil {
		return JobContractRevision{}, err
	}
	now = now.UTC().Round(0)
	if now.Before(startObservation.CreatedAt) || now.Before(grant.IssuedAt) ||
		now.Before(sourceResult.CreatedAt) || !now.Before(grant.Deadline) || !now.Before(canonical.Deadline) {
		return JobContractRevision{}, errors.New("JobContract proposal time is outside the accepted Planner Session, PlanningAuthorization, or contract deadline")
	}
	if job.Revision >= uint64(^uint64(0)>>1) {
		return JobContractRevision{}, errors.New("Job revision exhausted")
	}
	return JobContractRevision{
		ID: JobContractRevisionID(commandID), CommandID: commandID, JobID: job.ID,
		Revision:                uint64(len(job.JobContractRevisions) + 1),
		CanonicalizationVersion: JobContractCanonicalizationVersion, CanonicalHash: hash,
		Contract: canonical, Origin: JobContractRevisionOriginPlanner,
		ProposedByRoleExecutionID: roleExecutionID,
		SourceResultObservationID: sourceResult.ID, SourceResultContentSHA256: sourceResult.ContentSHA256,
		CreatedAt: now, CreatedRevision: job.Revision + 1,
	}, nil
}

func NewJobContractAmendmentRevision(
	job Job,
	commandID string,
	basedOnRevision uint64,
	basedOnCanonicalHash, reason string,
	principal auth.AuthenticatedPrincipal,
	contract JobContract,
	now time.Time,
) (JobContractRevision, error) {
	if err := job.Validate(); err != nil {
		return JobContractRevision{}, fmt.Errorf("validate Job before contract amendment: %w", err)
	}
	if job.State != JobActive {
		return JobContractRevision{}, errors.New("JobContract amendment requires an ACTIVE Job")
	}
	if !validCommandID(commandID) || now.IsZero() {
		return JobContractRevision{}, errors.New("JobContract amendment command identity and timestamp are required")
	}
	if err := principal.Validate(); err != nil {
		return JobContractRevision{}, fmt.Errorf("validate amending Principal: %w", err)
	}
	if !strings.HasPrefix(principal.PrincipalID(), "human_") {
		return JobContractRevision{}, errors.New("JobContract amendment requires a HUMAN Principal")
	}
	if now.Before(principal.AuthenticatedAt()) {
		return JobContractRevision{}, errors.New("JobContract amendment time precedes Principal authentication")
	}
	if len(job.JobContractRevisions) == 0 {
		return JobContractRevision{}, errors.New("JobContract amendment requires an existing approved revision")
	}
	latest := job.JobContractRevisions[len(job.JobContractRevisions)-1]
	if basedOnRevision != latest.Revision || basedOnCanonicalHash != latest.CanonicalHash ||
		!validSHA256(basedOnCanonicalHash) {
		return JobContractRevision{}, errors.New("JobContract amendment must identify the exact latest approved revision and hash")
	}
	var approval *JobApproval
	for i := range job.JobApprovals {
		candidate := &job.JobApprovals[i]
		if candidate.ContractRevision == latest.Revision {
			approval = candidate
			break
		}
	}
	if approval == nil {
		return JobContractRevision{}, errors.New("JobContract amendment requires a matching JobApproval for the latest revision")
	}
	if err := approval.Validate(job, latest); err != nil {
		return JobContractRevision{}, fmt.Errorf("validate prior JobApproval: %w", err)
	}
	if principal.AuthenticatedAt().Before(approval.ApprovedAt) {
		return JobContractRevision{}, errors.New("JobContract amendment Principal must authenticate after the current JobApproval")
	}
	reason = strings.TrimSpace(reason)
	if !utf8.ValidString(reason) || reason == "" || len(reason) > 2000 {
		return JobContractRevision{}, errors.New("JobContract amendment reason is required and must not exceed 2,000 bytes")
	}
	canonical, hash, err := CanonicalizeJobContract(contract, JobContractCanonicalizationVersion)
	if err != nil {
		return JobContractRevision{}, err
	}
	if hash == latest.CanonicalHash {
		return JobContractRevision{}, errors.New("JobContract amendment must change the canonical contract")
	}
	now = now.UTC().Round(0)
	if now.Before(latest.CreatedAt) || now.Before(approval.ApprovedAt) || !now.Before(canonical.Deadline) {
		return JobContractRevision{}, errors.New("JobContract amendment time is outside the prior approval or new contract deadline")
	}
	if job.Revision >= uint64(^uint64(0)>>1) {
		return JobContractRevision{}, errors.New("Job revision exhausted")
	}
	return JobContractRevision{
		ID: JobContractRevisionID(commandID), CommandID: commandID, JobID: job.ID,
		Revision:                uint64(len(job.JobContractRevisions) + 1),
		CanonicalizationVersion: JobContractCanonicalizationVersion, CanonicalHash: hash,
		Contract: canonical, Origin: JobContractRevisionOriginHumanAmendment,
		AmendedByPrincipalID: principal.PrincipalID(), AmendedByIssuer: principal.Issuer(),
		AmendedBySubject: principal.Subject(), AmendedAuthenticatedAt: principal.AuthenticatedAt().UTC().Round(0),
		AmendmentReason: reason, SupersedesRevision: latest.Revision, SupersedesCanonicalHash: latest.CanonicalHash,
		CreatedAt: now, CreatedRevision: job.Revision + 1,
	}, nil
}

func (r JobContractRevision) Validate(job Job) error {
	if !validCommandID(r.CommandID) || r.ID != JobContractRevisionID(r.CommandID) ||
		r.JobID != job.ID || r.Revision == 0 || r.CanonicalizationVersion != JobContractCanonicalizationVersion ||
		!validSHA256(r.CanonicalHash) || r.CreatedAt.IsZero() || r.CreatedRevision < 2 ||
		r.CreatedRevision > job.Revision || r.CreatedAt.After(job.UpdatedAt) {
		return errors.New("JobContractRevision identity or metadata is invalid")
	}
	switch r.Origin {
	case JobContractRevisionOriginPlanner:
		if r.AmendedByPrincipalID != "" || r.AmendedByIssuer != "" || r.AmendedBySubject != "" ||
			!r.AmendedAuthenticatedAt.IsZero() || r.AmendmentReason != "" ||
			r.SupersedesRevision != 0 || r.SupersedesCanonicalHash != "" {
			return errors.New("Planner JobContractRevision contains human amendment provenance")
		}
		if err := r.validatePlannerProvenance(job); err != nil {
			return err
		}
	case JobContractRevisionOriginHumanAmendment:
		if r.ProposedByRoleExecutionID != "" || r.SourceResultObservationID != "" ||
			r.SourceResultContentSHA256 != "" {
			return errors.New("HUMAN_AMENDMENT JobContractRevision contains Planner provenance")
		}
		if err := r.validateHumanAmendmentProvenance(job); err != nil {
			return err
		}
	default:
		return errors.New("JobContractRevision origin is invalid")
	}
	canonical, hash, err := CanonicalizeJobContract(r.Contract, r.CanonicalizationVersion)
	if err != nil {
		return err
	}
	canonicalJSON, err := json.Marshal(canonical)
	if err != nil {
		return err
	}
	storedJSON, err := json.Marshal(r.Contract)
	if err != nil {
		return err
	}
	if !bytes.Equal(canonicalJSON, storedJSON) || hash != r.CanonicalHash || !r.CreatedAt.Before(canonical.Deadline) {
		return errors.New("JobContractRevision canonical content or hash mismatch")
	}
	return nil
}

func (r JobContractRevision) validatePlannerProvenance(job Job) error {
	if !validPlanningIdentity(r.ProposedByRoleExecutionID) ||
		!validPlanningIdentity(r.SourceResultObservationID) || !validSHA256(r.SourceResultContentSHA256) {
		return errors.New("Planner JobContractRevision requires RoleExecution and Result provenance")
	}
	var execution RoleExecution
	foundExecution := false
	for _, candidate := range job.RoleExecutions {
		if candidate.ID == r.ProposedByRoleExecutionID {
			execution, foundExecution = candidate, true
			break
		}
	}
	startObservation, foundStart := acceptedStartForExecution(job, r.ProposedByRoleExecutionID)
	var grant PlanningAuthorization
	foundGrant := false
	for _, candidate := range job.PlanningAuthorizations {
		if foundExecution && candidate.ID == execution.PlanningAuthorizationID {
			grant, foundGrant = candidate, true
			break
		}
	}
	if !foundExecution || execution.Role != RolePlanner || execution.TaskID != "" || !foundStart ||
		!foundGrant || grant.Validate(job) != nil || r.CreatedRevision <= execution.CreatedRevision ||
		r.CreatedAt.Before(startObservation.CreatedAt) || r.CreatedAt.Before(execution.CreatedAt) ||
		r.CreatedAt.Before(grant.IssuedAt) || !r.CreatedAt.Before(grant.Deadline) {
		return errors.New("JobContractRevision proposer is not an accepted Taskless PLANNER execution within its PlanningAuthorization")
	}
	var sourceResult *ProviderResultObservation
	for i := range job.ProviderResultObservations {
		candidate := &job.ProviderResultObservations[i]
		if candidate.ID == r.SourceResultObservationID {
			sourceResult = candidate
			break
		}
	}
	if sourceResult == nil || sourceResult.JobID != job.ID ||
		sourceResult.RoleExecutionID != r.ProposedByRoleExecutionID || !sourceResult.Final ||
		sourceResult.ContentSHA256 != r.SourceResultContentSHA256 ||
		r.CreatedRevision <= sourceResult.CreatedRevision || r.CreatedAt.Before(sourceResult.CreatedAt) {
		return errors.New("JobContractRevision source Result identity, digest, or append order is invalid")
	}
	if err := sourceResult.Validate(job); err != nil {
		return fmt.Errorf("JobContractRevision source Result is invalid: %w", err)
	}
	if sourceResult.TurnID == "" {
		return errors.New("JobContractRevision source Result is not bound to a Planner Turn")
	}
	return nil
}

func (r JobContractRevision) validateHumanAmendmentProvenance(job Job) error {
	if !strings.HasPrefix(r.AmendedByPrincipalID, "human_") ||
		!utf8.ValidString(r.AmendedByIssuer) || !utf8.ValidString(r.AmendedBySubject) ||
		strings.TrimSpace(r.AmendedBySubject) == "" || r.AmendedAuthenticatedAt.IsZero() ||
		r.AmendedAuthenticatedAt.After(r.CreatedAt) || !utf8.ValidString(r.AmendmentReason) ||
		strings.TrimSpace(r.AmendmentReason) == "" || len(r.AmendmentReason) > 2000 ||
		r.AmendmentReason != strings.TrimSpace(r.AmendmentReason) ||
		r.Revision < 2 || r.SupersedesRevision != r.Revision-1 ||
		!validSHA256(r.SupersedesCanonicalHash) {
		return errors.New("HUMAN_AMENDMENT JobContractRevision provenance is invalid")
	}
	issuer, err := url.Parse(r.AmendedByIssuer)
	if err != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.User != nil ||
		issuer.RawQuery != "" || issuer.Fragment != "" {
		return errors.New("JobContract amendment source must be an authenticated HTTPS OIDC issuer")
	}
	if r.AmendedByPrincipalID != identity.PrincipalID(r.AmendedByIssuer, r.AmendedBySubject) {
		return errors.New("JobContract amendment Principal does not match its issuer and subject")
	}
	if len(job.JobContractRevisions) < 2 || r.SupersedesRevision > uint64(len(job.JobContractRevisions)-1) {
		return errors.New("JobContract amendment supersedes a missing revision")
	}
	previous := job.JobContractRevisions[r.SupersedesRevision-1]
	if previous.Revision != r.SupersedesRevision || previous.CanonicalHash != r.SupersedesCanonicalHash ||
		r.CreatedRevision <= previous.CreatedRevision || r.CreatedAt.Before(previous.CreatedAt) {
		return errors.New("JobContract amendment does not bind the exact prior revision")
	}
	var approval *JobApproval
	for i := range job.JobApprovals {
		candidate := &job.JobApprovals[i]
		if candidate.ContractRevision == previous.Revision {
			approval = candidate
			break
		}
	}
	if approval == nil {
		return errors.New("JobContract amendment has no prior approval")
	}
	if approval.JobID != job.ID || approval.ContractRevision != previous.Revision ||
		approval.CanonicalHash != previous.CanonicalHash ||
		approval.CanonicalizationVersion != previous.CanonicalizationVersion ||
		approval.ApprovedAt.IsZero() || approval.ApprovedAt.Before(previous.CreatedAt) ||
		!approval.ApprovedAt.Before(previous.Contract.Deadline) ||
		approval.CreatedRevision <= previous.CreatedRevision || approval.CreatedRevision > job.Revision ||
		approval.ApprovedAt.After(job.UpdatedAt) {
		return errors.New("JobContract amendment prior approval does not bind the superseded revision")
	}
	if r.AmendedAuthenticatedAt.Before(approval.ApprovedAt) ||
		r.CreatedRevision <= approval.CreatedRevision || r.CreatedAt.Before(approval.ApprovedAt) {
		return errors.New("JobContract amendment Principal or command predates its prior approval")
	}
	return nil
}

func NewJobApproval(job Job, commandID string, contractRevision uint64, canonicalHash string, canonicalizationVersion uint32, reason string, principal auth.AuthenticatedPrincipal, now time.Time) (JobApproval, error) {
	if err := job.Validate(); err != nil {
		return JobApproval{}, fmt.Errorf("validate Job before approval: %w", err)
	}
	if job.State != JobDraft {
		return JobApproval{}, errors.New("JobApproval requires a DRAFT Job")
	}
	if !validCommandID(commandID) || now.IsZero() {
		return JobApproval{}, errors.New("JobApproval command identity and timestamp are required")
	}
	if err := principal.Validate(); err != nil {
		return JobApproval{}, fmt.Errorf("validate approving Principal: %w", err)
	}
	if !strings.HasPrefix(principal.PrincipalID(), "human_") {
		return JobApproval{}, errors.New("JobApproval requires a HUMAN Principal")
	}
	if now.Before(principal.AuthenticatedAt()) {
		return JobApproval{}, errors.New("JobApproval time precedes Principal authentication")
	}
	if len(job.JobContractRevisions) == 0 {
		return JobApproval{}, errors.New("JobApproval requires a JobContract revision")
	}
	latest := job.JobContractRevisions[len(job.JobContractRevisions)-1]
	if contractRevision != latest.Revision || canonicalHash != latest.CanonicalHash ||
		canonicalizationVersion != latest.CanonicalizationVersion {
		return JobApproval{}, errors.New("JobApproval must bind the exact latest JobContract revision")
	}
	for _, existing := range job.JobApprovals {
		if existing.ContractRevision == contractRevision {
			return JobApproval{}, errors.New("JobContract revision already has a JobApproval")
		}
	}
	reason = strings.TrimSpace(reason)
	if !utf8.ValidString(reason) || reason == "" || len(reason) > 2000 {
		return JobApproval{}, errors.New("JobApproval reason is required and must not exceed 2,000 bytes")
	}
	now = now.UTC().Round(0)
	if now.Before(latest.CreatedAt) || !now.Before(latest.Contract.Deadline) {
		return JobApproval{}, errors.New("JobApproval time is outside the approved contract deadline")
	}
	if job.Revision >= uint64(^uint64(0)>>1) {
		return JobApproval{}, errors.New("Job revision exhausted")
	}
	return JobApproval{
		ID: JobApprovalID(commandID), CommandID: commandID, JobID: job.ID,
		ContractRevision: latest.Revision, CanonicalizationVersion: latest.CanonicalizationVersion,
		CanonicalHash: latest.CanonicalHash, ApproverPrincipalID: principal.PrincipalID(),
		ApproverSource: principal.Issuer(), ApproverSubject: principal.Subject(),
		Reason: reason, ApprovedAt: now, CreatedRevision: job.Revision + 1,
	}, nil
}

func (a JobApproval) Validate(job Job, revision JobContractRevision) error {
	if !validCommandID(a.CommandID) || a.ID != JobApprovalID(a.CommandID) ||
		a.JobID != job.ID || a.ContractRevision != revision.Revision ||
		a.CanonicalizationVersion != revision.CanonicalizationVersion || a.CanonicalHash != revision.CanonicalHash ||
		!strings.HasPrefix(a.ApproverPrincipalID, "human_") ||
		!utf8.ValidString(a.ApproverSource) || !utf8.ValidString(a.ApproverSubject) ||
		strings.TrimSpace(a.ApproverSubject) == "" || !utf8.ValidString(a.Reason) ||
		strings.TrimSpace(a.Reason) == "" || len(a.Reason) > 2000 ||
		a.ApprovedAt.IsZero() || a.ApprovedAt.Before(revision.CreatedAt) ||
		!a.ApprovedAt.Before(revision.Contract.Deadline) ||
		a.CreatedRevision <= revision.CreatedRevision || a.CreatedRevision > job.Revision ||
		a.ApprovedAt.After(job.UpdatedAt) {
		return errors.New("JobApproval identity, binding, or metadata is invalid")
	}
	issuer, err := url.Parse(a.ApproverSource)
	if err != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.User != nil ||
		issuer.RawQuery != "" || issuer.Fragment != "" {
		return errors.New("JobApproval source must be an authenticated HTTPS OIDC issuer")
	}
	if a.ApproverPrincipalID != identity.PrincipalID(a.ApproverSource, a.ApproverSubject) {
		return errors.New("JobApproval Principal does not match its issuer and subject")
	}
	if strings.TrimSpace(a.Reason) != a.Reason {
		return errors.New("JobApproval reason must be canonical")
	}
	return nil
}

func validateJobContractFacts(job Job) error {
	revisionsByNumber := make(map[uint64]JobContractRevision, len(job.JobContractRevisions))
	seenRevisionIDs := make(map[string]struct{}, len(job.JobContractRevisions))
	seenRevisionCommands := make(map[string]struct{}, len(job.JobContractRevisions))
	var previousRevisionCreated uint64
	var previousRevisionAt time.Time
	for i, revision := range job.JobContractRevisions {
		if revision.Revision != uint64(i+1) || revision.CreatedRevision <= previousRevisionCreated ||
			(!previousRevisionAt.IsZero() && revision.CreatedAt.Before(previousRevisionAt)) {
			return fmt.Errorf("%w: JobContract revision sequence is not append ordered", ErrInvalidJob)
		}
		if err := revision.Validate(job); err != nil {
			return fmt.Errorf("%w: invalid JobContractRevision: %v", ErrInvalidJob, err)
		}
		if _, exists := seenRevisionIDs[revision.ID]; exists {
			return fmt.Errorf("%w: duplicate JobContractRevision ID", ErrInvalidJob)
		}
		if _, exists := seenRevisionCommands[revision.CommandID]; exists {
			return fmt.Errorf("%w: duplicate JobContractRevision command", ErrInvalidJob)
		}
		seenRevisionIDs[revision.ID] = struct{}{}
		seenRevisionCommands[revision.CommandID] = struct{}{}
		revisionsByNumber[revision.Revision] = revision
		previousRevisionCreated = revision.CreatedRevision
		previousRevisionAt = revision.CreatedAt
	}
	approvalsByRevision := make(map[uint64]JobApproval, len(job.JobApprovals))
	seenApprovalIDs := make(map[string]struct{}, len(job.JobApprovals))
	seenApprovalCommands := make(map[string]struct{}, len(job.JobApprovals))
	var previousApprovalCreated uint64
	var previousApprovalAt time.Time
	for _, approval := range job.JobApprovals {
		revision, exists := revisionsByNumber[approval.ContractRevision]
		if !exists {
			return fmt.Errorf("%w: JobApproval has no JobContract revision", ErrInvalidJob)
		}
		if approval.CreatedRevision <= previousApprovalCreated ||
			(!previousApprovalAt.IsZero() && approval.ApprovedAt.Before(previousApprovalAt)) {
			return fmt.Errorf("%w: JobApproval sequence is not append ordered", ErrInvalidJob)
		}
		if err := approval.Validate(job, revision); err != nil {
			return fmt.Errorf("%w: invalid JobApproval: %v", ErrInvalidJob, err)
		}
		if _, exists := approvalsByRevision[approval.ContractRevision]; exists {
			return fmt.Errorf("%w: duplicate JobApproval for contract revision", ErrInvalidJob)
		}
		if _, exists := seenApprovalIDs[approval.ID]; exists {
			return fmt.Errorf("%w: duplicate JobApproval ID", ErrInvalidJob)
		}
		if _, exists := seenApprovalCommands[approval.CommandID]; exists {
			return fmt.Errorf("%w: duplicate JobApproval command", ErrInvalidJob)
		}
		approvalsByRevision[approval.ContractRevision] = approval
		seenApprovalIDs[approval.ID] = struct{}{}
		seenApprovalCommands[approval.CommandID] = struct{}{}
		previousApprovalCreated = approval.CreatedRevision
		previousApprovalAt = approval.ApprovedAt
	}
	if len(job.JobContractRevisions) == 0 {
		if len(job.JobApprovals) != 0 || job.State == JobActive {
			return fmt.Errorf("%w: JobApproval or ACTIVE state requires a JobContract revision", ErrInvalidJob)
		}
		return nil
	}
	latest := job.JobContractRevisions[len(job.JobContractRevisions)-1]
	_, latestApproved := approvalsByRevision[latest.Revision]
	if job.Goal != latest.Contract.Purpose {
		return fmt.Errorf("%w: Job goal differs from latest JobContract purpose", ErrInvalidJob)
	}
	if job.State == JobActive && !latestApproved {
		return fmt.Errorf("%w: ACTIVE Job requires JobApproval for latest contract revision", ErrInvalidJob)
	}
	if job.State == JobDraft && latestApproved {
		return fmt.Errorf("%w: approved latest JobContract requires ACTIVE state", ErrInvalidJob)
	}
	return nil
}

func acceptedStartForExecution(job Job, roleExecutionID string) (ProviderStartObservation, bool) {
	for _, start := range job.ProviderStartIntents {
		if start.RoleExecutionID != roleExecutionID {
			continue
		}
		if observation, found := latestProviderStartObservation(job, start.ID); found && observation.Status == ProviderStartAccepted {
			return observation, true
		}
	}
	return ProviderStartObservation{}, false
}
