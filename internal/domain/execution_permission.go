package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

type ExecutionPermissionRequest struct {
	JobID               string    `json:"job_id"`
	CommandID           string    `json:"command_id"`
	Role                string    `json:"role"`
	Effect              string    `json:"effect"`
	Resource            string    `json:"resource"`
	ProviderID          string    `json:"provider_id"`
	AccountID           string    `json:"account_id"`
	ConfigurationSHA256 string    `json:"configuration_sha256"`
	ReservedTurns       uint32    `json:"reserved_turns"`
	ReservedTokens      uint64    `json:"reserved_tokens"`
	ExpiresAt           time.Time `json:"expires_at"`
}

type ExecutionEffectAssessment struct {
	Role                string    `json:"role"`
	Effect              string    `json:"effect"`
	Resource            string    `json:"resource"`
	ProviderID          string    `json:"provider_id"`
	AccountID           string    `json:"account_id"`
	ConfigurationSHA256 string    `json:"configuration_sha256"`
	RequestSHA256       string    `json:"request_sha256"`
	Reversible          bool      `json:"reversible"`
	Contained           bool      `json:"contained"`
	ApprovalRequired    bool      `json:"approval_required"`
	Mutating            bool      `json:"mutating"`
	ValidUntil          time.Time `json:"valid_until"`
	EvidenceSHA256      string    `json:"evidence_sha256"`
}

type executionEffectAssessmentDigest struct {
	Role                string    `json:"role"`
	Effect              string    `json:"effect"`
	Resource            string    `json:"resource"`
	ProviderID          string    `json:"provider_id"`
	AccountID           string    `json:"account_id"`
	ConfigurationSHA256 string    `json:"configuration_sha256"`
	RequestSHA256       string    `json:"request_sha256"`
	Reversible          bool      `json:"reversible"`
	Contained           bool      `json:"contained"`
	ApprovalRequired    bool      `json:"approval_required"`
	Mutating            bool      `json:"mutating"`
	ValidUntil          time.Time `json:"valid_until"`
	EvidenceSHA256      string    `json:"evidence_sha256"`
}

type ExecutionPermission struct {
	ID                      string    `json:"id"`
	CommandID               string    `json:"command_id"`
	JobID                   string    `json:"job_id"`
	OperationID             string    `json:"operation_id"`
	Role                    string    `json:"role"`
	Effect                  string    `json:"effect"`
	Resource                string    `json:"resource"`
	JobApprovalID           string    `json:"job_approval_id"`
	ContractRevision        uint64    `json:"contract_revision"`
	CanonicalizationVersion uint32    `json:"canonicalization_version"`
	CanonicalHash           string    `json:"canonical_hash"`
	ProviderID              string    `json:"provider_id"`
	AccountID               string    `json:"account_id"`
	ConfigurationSHA256     string    `json:"configuration_sha256"`
	RequestSHA256           string    `json:"request_sha256"`
	AssessmentSHA256        string    `json:"assessment_sha256"`
	Assessment              ExecutionEffectAssessment `json:"assessment"`
	ReservedTurns           uint32    `json:"reserved_turns"`
	ReservedTokens          uint64    `json:"reserved_tokens"`
	ExpiresAt               time.Time `json:"expires_at"`
	CreatedAt               time.Time `json:"created_at"`
	CreatedRevision         uint64    `json:"created_revision"`
}

func ExecutionPermissionID(commandID string) string {
	return planningOperationID("execution_permission", commandID)
}

func ExecutionPermissionOperationID(commandID string) string {
	return planningOperationID("execution_operation", commandID)
}

func ExecutionPermissionRequestSHA256(request ExecutionPermissionRequest) (string, error) {
	if request.ExpiresAt.IsZero() {
		return "", errors.New("ExecutionPermission expiry is required")
	}
	request.ExpiresAt = request.ExpiresAt.UTC().Round(0)
	encoded, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("encode ExecutionPermission request: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func (a ExecutionEffectAssessment) SHA256() (string, error) {
	if a.ValidUntil.IsZero() {
		return "", errors.New("ExecutionEffectAssessment validity is required")
	}
	encoded, err := json.Marshal(executionEffectAssessmentDigest{
		Role: a.Role, Effect: a.Effect, Resource: a.Resource, ProviderID: a.ProviderID,
		AccountID: a.AccountID, ConfigurationSHA256: a.ConfigurationSHA256,
		RequestSHA256: a.RequestSHA256, Reversible: a.Reversible, Contained: a.Contained,
		ApprovalRequired: a.ApprovalRequired, Mutating: a.Mutating,
		ValidUntil: a.ValidUntil.UTC().Round(0), EvidenceSHA256: a.EvidenceSHA256,
	})
	if err != nil {
		return "", fmt.Errorf("encode ExecutionEffectAssessment: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func ValidateExecutionPermissionRequest(job Job, request ExecutionPermissionRequest, now time.Time) error {
	if err := job.Validate(); err != nil {
		return fmt.Errorf("validate Job before ExecutionPermission: %w", err)
	}
	if !validCommandID(request.CommandID) || request.JobID != job.ID ||
		(request.Role != "CODER" && request.Role != "REVIEWER") ||
		!validExecutionText(request.Effect, 512) || !validExecutionText(request.Resource, 4096) ||
		!validPlanningIdentity(request.ProviderID) || !validPlanningIdentity(request.AccountID) ||
		!validSHA256(request.ConfigurationSHA256) || request.ReservedTurns == 0 || request.ReservedTokens == 0 ||
		request.ExpiresAt.IsZero() || now.IsZero() {
		return errors.New("ExecutionPermission request identity or bounds are invalid")
	}
	if job.State != JobActive || len(job.JobContractRevisions) == 0 {
		return errors.New("ExecutionPermission requires an ACTIVE Job with an approved JobContract")
	}
	revision := job.JobContractRevisions[len(job.JobContractRevisions)-1]
	approval, found := jobApprovalForRevision(job, revision.Revision)
	if !found {
		return errors.New("ExecutionPermission requires JobApproval for the latest contract revision")
	}
	if err := approval.Validate(job, revision); err != nil {
		return fmt.Errorf("validate current JobApproval: %w", err)
	}
	now = now.UTC().Round(0)
	expiresAt := request.ExpiresAt.UTC().Round(0)
	if now.Before(approval.ApprovedAt) || !now.Before(revision.Contract.Deadline) ||
		!expiresAt.After(now) || expiresAt.After(revision.Contract.Deadline) {
		return errors.New("ExecutionPermission request is outside the current approval or contract deadline")
	}
	if request.ConfigurationSHA256 != revision.Contract.ConfigurationSHA256 {
		return errors.New("ExecutionPermission Provider configuration differs from the approved contract")
	}
	if !contractContainsResource(revision.Contract, request.Resource) {
		return errors.New("ExecutionPermission resource is outside the approved JobContract scope")
	}
	if containsExact(revision.Contract.ProhibitedEffects, request.Effect) {
		return errors.New("ExecutionPermission effect is prohibited by the approved JobContract")
	}
	if containsExact(revision.Contract.ApprovalRequiredEffects, request.Effect) {
		return errors.New("ExecutionPermission effect requires a separate human approval")
	}
	turns, tokens, err := executionPermissionBudgetUsed(job, revision.Revision)
	if err != nil {
		return err
	}
	if turns > uint64(revision.Contract.Budget.MaxTurns) || uint64(request.ReservedTurns) > uint64(revision.Contract.Budget.MaxTurns)-turns {
		return errors.New("ExecutionPermission turn reservation exceeds the approved budget")
	}
	if ^uint64(0)-tokens < request.ReservedTokens ||
		tokens+request.ReservedTokens > revision.Contract.Budget.MaxTokens {
		return errors.New("ExecutionPermission token reservation exceeds the approved budget")
	}
	if job.Revision >= uint64(^uint64(0)>>1) {
		return errors.New("Job revision exhausted")
	}
	return nil
}

func NewExecutionPermission(
	job Job,
	request ExecutionPermissionRequest,
	requestSHA256 string,
	assessment ExecutionEffectAssessment,
	now time.Time,
) (ExecutionPermission, error) {
	if err := ValidateExecutionPermissionRequest(job, request, now); err != nil {
		return ExecutionPermission{}, err
	}
	expectedRequestSHA, err := ExecutionPermissionRequestSHA256(request)
	if err != nil {
		return ExecutionPermission{}, err
	}
	if requestSHA256 != expectedRequestSHA || !validSHA256(requestSHA256) {
		return ExecutionPermission{}, errors.New("ExecutionPermission request digest mismatch")
	}
	if assessment.Role != request.Role || assessment.Effect != request.Effect ||
		assessment.Resource != request.Resource || assessment.ProviderID != request.ProviderID ||
		assessment.AccountID != request.AccountID ||
		assessment.ConfigurationSHA256 != request.ConfigurationSHA256 ||
		assessment.RequestSHA256 != requestSHA256 ||
		!validSHA256(assessment.EvidenceSHA256) || assessment.ValidUntil.IsZero() ||
		assessment.ValidUntil.Before(now) || request.ExpiresAt.After(assessment.ValidUntil) ||
		assessment.ApprovalRequired || (!assessment.Reversible && !assessment.Contained) ||
		(request.Role == "REVIEWER" && assessment.Mutating) {
		return ExecutionPermission{}, errors.New("ExecutionEffectAssessment does not safely authorize the exact request")
	}
	assessmentSHA, err := assessment.SHA256()
	if err != nil {
		return ExecutionPermission{}, err
	}
	revision := job.JobContractRevisions[len(job.JobContractRevisions)-1]
	approval, _ := jobApprovalForRevision(job, revision.Revision)
	now = now.UTC().Round(0)
	permission := ExecutionPermission{
		ID: ExecutionPermissionID(request.CommandID), CommandID: request.CommandID,
		JobID: job.ID, OperationID: ExecutionPermissionOperationID(request.CommandID),
		Role: request.Role, Effect: request.Effect, Resource: request.Resource,
		JobApprovalID: approval.ID, ContractRevision: revision.Revision,
		CanonicalizationVersion: revision.CanonicalizationVersion, CanonicalHash: revision.CanonicalHash,
		ProviderID: request.ProviderID, AccountID: request.AccountID,
		ConfigurationSHA256: request.ConfigurationSHA256, RequestSHA256: requestSHA256,
		AssessmentSHA256: assessmentSHA, Assessment: assessment,
		ReservedTurns: request.ReservedTurns, ReservedTokens: request.ReservedTokens,
		ExpiresAt: request.ExpiresAt.UTC().Round(0), CreatedAt: now, CreatedRevision: job.Revision + 1,
	}
	if err := permission.Validate(job); err != nil {
		return ExecutionPermission{}, err
	}
	return permission, nil
}

func (p ExecutionPermission) Validate(job Job) error {
	if !validCommandID(p.CommandID) || p.ID != ExecutionPermissionID(p.CommandID) ||
		p.JobID != job.ID || p.OperationID != ExecutionPermissionOperationID(p.CommandID) ||
		(p.Role != "CODER" && p.Role != "REVIEWER") ||
		!validExecutionText(p.Effect, 512) || !validExecutionText(p.Resource, 4096) ||
		!validPlanningIdentity(p.JobApprovalID) || p.ContractRevision == 0 ||
		p.CanonicalizationVersion != JobContractCanonicalizationVersion ||
		!validSHA256(p.CanonicalHash) || !validPlanningIdentity(p.ProviderID) ||
		!validPlanningIdentity(p.AccountID) || !validSHA256(p.ConfigurationSHA256) ||
		!validSHA256(p.RequestSHA256) || !validSHA256(p.AssessmentSHA256) ||
		p.ReservedTurns == 0 || p.ReservedTokens == 0 || p.ExpiresAt.IsZero() || p.CreatedAt.IsZero() ||
		p.CreatedRevision == 0 {
		return errors.New("ExecutionPermission identity or metadata is invalid")
	}
	var revision *JobContractRevision
	for i := range job.JobContractRevisions {
		if job.JobContractRevisions[i].Revision == p.ContractRevision {
			revision = &job.JobContractRevisions[i]
			break
		}
	}
	if revision == nil || revision.CanonicalHash != p.CanonicalHash ||
		revision.CanonicalizationVersion != p.CanonicalizationVersion {
		return errors.New("ExecutionPermission does not bind an existing exact JobContract revision")
	}
	approval, found := jobApprovalForRevision(job, p.ContractRevision)
	if !found || approval.ID != p.JobApprovalID {
		return errors.New("ExecutionPermission does not bind the JobApproval for its contract revision")
	}
	if err := approval.Validate(job, *revision); err != nil {
		return err
	}
	if p.CreatedRevision <= approval.CreatedRevision || p.CreatedAt.Before(approval.ApprovedAt) ||
		!p.CreatedAt.Before(revision.Contract.Deadline) || !p.ExpiresAt.After(p.CreatedAt) ||
		p.ExpiresAt.After(revision.Contract.Deadline) || p.ConfigurationSHA256 != revision.Contract.ConfigurationSHA256 ||
		!contractContainsResource(revision.Contract, p.Resource) ||
		containsExact(revision.Contract.ProhibitedEffects, p.Effect) ||
		containsExact(revision.Contract.ApprovalRequiredEffects, p.Effect) {
		return errors.New("ExecutionPermission exceeds its approved JobContract boundary")
	}
	assessmentSHA, assessmentErr := p.Assessment.SHA256()
	if assessmentErr != nil || assessmentSHA != p.AssessmentSHA256 ||
		p.Assessment.Role != p.Role || p.Assessment.Effect != p.Effect ||
		p.Assessment.Resource != p.Resource || p.Assessment.ProviderID != p.ProviderID ||
		p.Assessment.AccountID != p.AccountID ||
		p.Assessment.ConfigurationSHA256 != p.ConfigurationSHA256 ||
		p.Assessment.RequestSHA256 != p.RequestSHA256 ||
		!validSHA256(p.Assessment.EvidenceSHA256) ||
		p.Assessment.ValidUntil.Before(p.ExpiresAt) || p.Assessment.ApprovalRequired ||
		(!p.Assessment.Reversible && !p.Assessment.Contained) ||
		(p.Role == "REVIEWER" && p.Assessment.Mutating) {
		return errors.New("ExecutionPermission assessment evidence or safety classification is invalid")
	}
	expectedRequest := ExecutionPermissionRequest{
		JobID: p.JobID, CommandID: p.CommandID, Role: p.Role, Effect: p.Effect,
		Resource: p.Resource, ProviderID: p.ProviderID, AccountID: p.AccountID,
		ConfigurationSHA256: p.ConfigurationSHA256, ReservedTurns: p.ReservedTurns, ReservedTokens: p.ReservedTokens,
		ExpiresAt: p.ExpiresAt,
	}
	expectedDigest, err := ExecutionPermissionRequestSHA256(expectedRequest)
	if err != nil || expectedDigest != p.RequestSHA256 {
		return errors.New("ExecutionPermission request digest does not match its immutable fields")
	}
	return nil
}

func (p ExecutionPermission) ValidateFor(job Job, operationID, requestSHA256 string, now time.Time) error {
	if err := p.Validate(job); err != nil {
		return err
	}
	if job.State != JobActive || len(job.JobContractRevisions) == 0 {
		return errors.New("ExecutionPermission is not valid for an inactive Job")
	}
	latest := job.JobContractRevisions[len(job.JobContractRevisions)-1]
	approval, found := jobApprovalForRevision(job, latest.Revision)
	if !found || latest.Revision != p.ContractRevision || latest.CanonicalHash != p.CanonicalHash ||
		approval.ID != p.JobApprovalID || operationID != p.OperationID ||
		requestSHA256 != p.RequestSHA256 || now.IsZero() ||
		!now.UTC().Before(p.ExpiresAt) || !now.UTC().Before(latest.Contract.Deadline) {
		return errors.New("ExecutionPermission is stale, expired, or bound to another operation")
	}
	return nil
}

func validateExecutionPermissionFacts(job Job) error {
	seenIDs := make(map[string]struct{}, len(job.ExecutionPermissions))
	seenCommands := make(map[string]struct{}, len(job.ExecutionPermissions))
	seenOperations := make(map[string]struct{}, len(job.ExecutionPermissions))
	previousRevision := uint64(0)
	previousAt := time.Time{}
	for _, permission := range job.ExecutionPermissions {
		if err := permission.Validate(job); err != nil {
			return fmt.Errorf("%w: invalid ExecutionPermission: %v", ErrInvalidJob, err)
		}
		if permission.CreatedAt.After(job.UpdatedAt) || permission.CreatedRevision > job.Revision ||
			permission.CreatedRevision <= previousRevision ||
			(!previousAt.IsZero() && permission.CreatedAt.Before(previousAt)) {
			return fmt.Errorf("%w: ExecutionPermission append order is invalid", ErrInvalidJob)
		}
		if _, exists := seenIDs[permission.ID]; exists {
			return fmt.Errorf("%w: duplicate ExecutionPermission ID", ErrInvalidJob)
		}
		if _, exists := seenCommands[permission.CommandID]; exists {
			return fmt.Errorf("%w: duplicate ExecutionPermission command", ErrInvalidJob)
		}
		if _, exists := seenOperations[permission.OperationID]; exists {
			return fmt.Errorf("%w: duplicate ExecutionPermission OperationID", ErrInvalidJob)
		}
		seenIDs[permission.ID] = struct{}{}
		seenCommands[permission.CommandID] = struct{}{}
		seenOperations[permission.OperationID] = struct{}{}
		previousRevision = permission.CreatedRevision
		previousAt = permission.CreatedAt
	}
	for _, revision := range job.JobContractRevisions {
		turns, tokens, err := executionPermissionBudgetUsed(job, revision.Revision)
		if err != nil {
			return err
		}
		if turns > uint64(revision.Contract.Budget.MaxTurns) || tokens > revision.Contract.Budget.MaxTokens {
			return fmt.Errorf("%w: ExecutionPermission reservations exceed contract revision %d budget", ErrInvalidJob, revision.Revision)
		}
	}
	return nil
}

func executionPermissionBudgetUsed(job Job, revision uint64) (uint64, uint64, error) {
	var turns, tokens uint64
	for _, permission := range job.ExecutionPermissions {
		if permission.ContractRevision != revision {
			continue
		}
		if ^uint64(0)-turns < uint64(permission.ReservedTurns) || ^uint64(0)-tokens < permission.ReservedTokens {
			return 0, 0, fmt.Errorf("%w: ExecutionPermission budget overflow", ErrInvalidJob)
		}
		turns += uint64(permission.ReservedTurns)
		tokens += permission.ReservedTokens
	}
	return turns, tokens, nil
}

func jobApprovalForRevision(job Job, revision uint64) (JobApproval, bool) {
	for _, approval := range job.JobApprovals {
		if approval.ContractRevision == revision {
			return approval, true
		}
	}
	return JobApproval{}, false
}

func contractContainsResource(contract JobContract, resource string) bool {
	return containsExact(contract.TargetRepositories, resource) ||
		containsExact(contract.TargetResources, resource) ||
		containsExact(contract.Workspaces, resource)
}

func containsExact(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func validExecutionText(value string, maxBytes int) bool {
	return utf8.ValidString(value) && strings.TrimSpace(value) == value &&
		len(value) > 0 && len(value) <= maxBytes && !strings.ContainsRune(value, '\x00')
}
