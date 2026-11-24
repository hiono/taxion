package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hiono/taxion/internal/identity"
)

var ErrInvalidJob = errors.New("invalid job")

const (
	IntakeProvenanceSchemaVersion uint32 = 1
	IngressSourceCLI                     = "cli"
)

type JobState string

const (
	JobDraft  JobState = "DRAFT"
	JobActive JobState = "ACTIVE"
)

type Job struct {
	ID                                    string                      `json:"id"`
	Request                               string                      `json:"request"`
	Goal                                  string                      `json:"goal,omitempty"`
	State                                 JobState                    `json:"state"`
	Revision                              uint64                      `json:"revision"`
	CreatedAt                             time.Time                   `json:"created_at"`
	UpdatedAt                             time.Time                   `json:"updated_at"`
	IntakeProvenance                      *IntakeProvenance           `json:"intake_provenance,omitempty"`
	PlanningAuthorizations                []PlanningAuthorization     `json:"planning_authorizations,omitempty"`
	JobContractRevisions                  []JobContractRevision       `json:"job_contract_revisions,omitempty"`
	JobApprovals                          []JobApproval               `json:"job_approvals,omitempty"`
	Tasks                                 []Task                      `json:"tasks,omitempty"`
	WorktreeAssignments                   []WorktreeAssignment        `json:"worktree_assignments,omitempty"`
	JobOutputs                            []JobOutput                 `json:"job_outputs,omitempty"`
	WorktreeLifecycleEnforcedFromRevision uint64                      `json:"worktree_lifecycle_enforced_from_revision,omitempty"`
	ExecutionPermissions                  []ExecutionPermission       `json:"execution_permissions,omitempty"`
	RoleExecutions                        []RoleExecution             `json:"role_executions,omitempty"`
	ProviderStartIntents                  []ProviderStartIntent       `json:"provider_start_intents,omitempty"`
	ProviderStartObservations             []ProviderStartObservation  `json:"provider_start_observations,omitempty"`
	ProviderEventObservations             []ProviderEventObservation  `json:"provider_event_observations,omitempty"`
	ProviderStopIntents                   []ProviderStopIntent        `json:"provider_stop_intents,omitempty"`
	ProviderStopObservations              []ProviderStopObservation   `json:"provider_stop_observations,omitempty"`
	ProviderStopDispatchClaims            []ProviderStopDispatchClaim `json:"provider_stop_dispatch_claims,omitempty"`
	ProviderStopDispatchReleases          []ProviderStopDispatchRelease `json:"provider_stop_dispatch_releases,omitempty"`
	ProviderResultObservations            []ProviderResultObservation `json:"provider_result_observations,omitempty"`
	ProviderUsageObservations             []ProviderUsageObservation  `json:"provider_usage_observations,omitempty"`
	ProviderLifecycleEnforcedFromRevision uint64                      `json:"provider_lifecycle_enforced_from_revision,omitempty"`
	PlanningTurnIntents                   []PlanningTurnIntent        `json:"planning_turn_intents,omitempty"`
	PlanningTurnObservations              []PlanningTurnObservation   `json:"planning_turn_observations,omitempty"`
	TaskTurnIntents                       []TaskTurnIntent            `json:"task_turn_intents,omitempty"`
	TaskTurnObservations                  []TaskTurnObservation       `json:"task_turn_observations,omitempty"`
	TaskTurnDispatchClaims                []TaskTurnDispatchClaim     `json:"task_turn_dispatch_claims,omitempty"`
	TaskTurnDispatchReleases              []TaskTurnDispatchRelease   `json:"task_turn_dispatch_releases,omitempty"`
	PlanningEffectDecisions               []PlanningEffectDecision    `json:"planning_effect_decisions,omitempty"`
	PlanningResumeIntents                 []PlanningResumeIntent      `json:"planning_resume_intents,omitempty"`
	PlanningResumeObservations            []PlanningResumeObservation `json:"planning_resume_observations,omitempty"`
	PlanningBoundaryDecisions             []PlanningBoundaryDecision  `json:"planning_boundary_decisions,omitempty"`
}

type IntakeProvenance struct {
	PrincipalID           string    `json:"principal_id"`
	Issuer                string    `json:"issuer"`
	Subject               string    `json:"subject"`
	IngressSource         string    `json:"ingress_source"`
	AuthenticatedAt       time.Time `json:"authenticated_at"`
	ReceivedAt            time.Time `json:"received_at"`
	RequestIdentity       string    `json:"request_identity"`
	OriginalRequestSHA256 string    `json:"original_request_sha256"`
	SchemaVersion         uint32    `json:"schema_version"`
}

func NewID(prefix string) (string, error) {
	if strings.TrimSpace(prefix) == "" {
		return "", fmt.Errorf("%w: empty identifier prefix", ErrInvalidJob)
	}
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate identifier: %w", err)
	}
	return prefix + "_" + hex.EncodeToString(value[:]), nil
}

func NewDraftJob(id, request string, now time.Time) (Job, error) {
	job := Job{
		ID:        id,
		Request:   request,
		State:     JobDraft,
		Revision:  1,
		CreatedAt: now.UTC().Round(0),
		UpdatedAt: now.UTC().Round(0),
	}
	if err := job.Validate(); err != nil {
		return Job{}, err
	}
	return job, nil
}

func NewDraftJobWithProvenance(id, request string, provenance IntakeProvenance, now time.Time) (Job, error) {
	provenance.AuthenticatedAt = provenance.AuthenticatedAt.UTC().Round(0)
	provenance.ReceivedAt = provenance.ReceivedAt.UTC().Round(0)
	provenance.OriginalRequestSHA256 = originalRequestDigest(request)

	job, err := NewDraftJob(id, request, now)
	if err != nil {
		return Job{}, err
	}
	job.IntakeProvenance = &provenance
	if err := job.Validate(); err != nil {
		return Job{}, err
	}
	return job, nil
}

func (p IntakeProvenance) Validate(originalRequest string, jobCreatedAt time.Time) error {
	if !utf8.ValidString(p.PrincipalID) || !utf8.ValidString(p.Issuer) || !utf8.ValidString(p.Subject) ||
		!utf8.ValidString(p.IngressSource) || !utf8.ValidString(p.RequestIdentity) {
		return errors.New("IntakeProvenance fields must be valid UTF-8")
	}
	if !strings.HasPrefix(strings.TrimSpace(p.PrincipalID), "human_") {
		return errors.New("authenticated HUMAN Principal ID is required")
	}
	if p.PrincipalID != identity.PrincipalID(p.Issuer, p.Subject) {
		return errors.New("HUMAN Principal ID does not match OIDC issuer and subject")
	}
	issuer, err := url.Parse(p.Issuer)
	if err != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.User != nil || issuer.RawQuery != "" || issuer.Fragment != "" {
		return errors.New("OIDC issuer must be an absolute HTTPS URL")
	}
	if strings.TrimSpace(p.Subject) == "" {
		return errors.New("OIDC subject is required")
	}
	if strings.TrimSpace(p.IngressSource) == "" {
		return errors.New("server-derived ingress source is required")
	}
	if p.AuthenticatedAt.IsZero() || p.ReceivedAt.IsZero() {
		return errors.New("authentication and intake timestamps are required")
	}
	if strings.TrimSpace(p.RequestIdentity) == "" {
		return errors.New("request identity is required")
	}
	if p.SchemaVersion != IntakeProvenanceSchemaVersion {
		return fmt.Errorf("unsupported IntakeProvenance schema version %d", p.SchemaVersion)
	}
	decoded, err := hex.DecodeString(p.OriginalRequestSHA256)
	if err != nil || len(decoded) != sha256.Size || p.OriginalRequestSHA256 != originalRequestDigest(originalRequest) {
		return errors.New("original request SHA-256 digest mismatch")
	}
	if jobCreatedAt.IsZero() || jobCreatedAt.Before(p.AuthenticatedAt) || jobCreatedAt.Before(p.ReceivedAt) {
		return errors.New("Job creation time precedes authenticated intake")
	}
	return nil
}

func originalRequestDigest(request string) string {
	sum := sha256.Sum256([]byte(request))
	return hex.EncodeToString(sum[:])
}

const (
	PlanningReadOnlyBoundarySnapshot  = "snapshot"
	PlanningReadOnlyBoundaryDocuments = "documents"
	PlanningEffectRepositoryRead      = "repository.read"
)

type PlanningBudget struct {
	MaxTurns  uint32 `json:"max_turns"`
	MaxTokens uint64 `json:"max_tokens"`
}

type PlanningReadOnlyBoundary struct {
	Mode      string `json:"mode"`
	Reference string `json:"reference"`
	SHA256    string `json:"sha256"`
}

type PlanningAuthorizationRequest struct {
	ProviderID          string                   `json:"provider_id"`
	AccountID           string                   `json:"account_id"`
	ConfigurationSHA256 string                   `json:"configuration_sha256"`
	RepositoryID        string                   `json:"repository_id"`
	ReadOnlyBoundary    PlanningReadOnlyBoundary `json:"read_only_boundary"`
	Budget              PlanningBudget           `json:"budget"`
	Deadline            time.Time                `json:"deadline"`
}

type PlanningAuthorization struct {
	ID                  string                   `json:"id"`
	CommandID           string                   `json:"command_id"`
	JobID               string                   `json:"job_id"`
	RequestSHA256       string                   `json:"request_sha256"`
	PrincipalID         string                   `json:"principal_id"`
	ProviderID          string                   `json:"provider_id"`
	AccountID           string                   `json:"account_id"`
	ConfigurationSHA256 string                   `json:"configuration_sha256"`
	RepositoryID        string                   `json:"repository_id"`
	ReadOnlyBoundary    PlanningReadOnlyBoundary `json:"read_only_boundary"`
	PermittedEffects    []string                 `json:"permitted_effects"`
	Budget              PlanningBudget           `json:"budget"`
	IssuedAt            time.Time                `json:"issued_at"`
	Deadline            time.Time                `json:"deadline"`
}

const (
	RolePlanner  = "PLANNER"
	RoleCoder    = "CODER"
	RoleReviewer = "REVIEWER"
)

type RoleExecution struct {
	ID                       string    `json:"id"`
	CommandID                string    `json:"command_id"`
	JobID                    string    `json:"job_id"`
	Role                     string    `json:"role"`
	TaskID                   string    `json:"task_id,omitempty"`
	WorktreeID               string    `json:"worktree_id,omitempty"`
	PlanningAuthorizationID  string    `json:"planning_authorization_id"`
	ExecutionPermissionID    string    `json:"execution_permission_id,omitempty"`
	ProviderStartOperationID string    `json:"provider_start_operation_id"`
	CreatedAt                time.Time `json:"created_at"`
	CreatedRevision          uint64    `json:"created_revision"`
}

type ProviderStartIntent struct {
	ID                      string                   `json:"id"`
	CommandID               string                   `json:"command_id"`
	JobID                   string                   `json:"job_id"`
	RoleExecutionID         string                   `json:"role_execution_id"`
	PlanningAuthorizationID string                   `json:"planning_authorization_id"`
	TaskID                  string                   `json:"task_id,omitempty"`
	WorktreeID              string                   `json:"worktree_id,omitempty"`
	ExecutionPermissionID   string                   `json:"execution_permission_id,omitempty"`
	ProviderID              string                   `json:"provider_id"`
	AccountID               string                   `json:"account_id"`
	ConfigurationSHA256     string                   `json:"configuration_sha256"`
	ReadOnlyBoundary        PlanningReadOnlyBoundary `json:"read_only_boundary"`
	Budget                  PlanningBudget           `json:"budget"`
	Deadline                time.Time                `json:"deadline"`
	RequestSHA256           string                   `json:"request_sha256"`
	CreatedAt               time.Time                `json:"created_at"`
	CreatedRevision         uint64                   `json:"created_revision"`
}

func RoleExecutionID(commandID string) string {
	sum := sha256.Sum256([]byte("taxion:role-execution:" + commandID))
	return "role_exec_" + hex.EncodeToString(sum[:])
}

func ProviderStartOperationID(commandID string) string {
	sum := sha256.Sum256([]byte("taxion:provider-start:" + commandID))
	return "provider_op_" + hex.EncodeToString(sum[:])
}

func validCommandID(commandID string) bool {
	if !strings.HasPrefix(commandID, "cmd_") || len(commandID) != len("cmd_")+32 {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(commandID, "cmd_"))
	return err == nil && len(decoded) == 16
}

func NewTasklessPlannerStart(job Job, authorizationID, principalID, commandID, requestSHA256 string, now time.Time) (RoleExecution, ProviderStartIntent, error) {
	if err := job.Validate(); err != nil {
		return RoleExecution{}, ProviderStartIntent{}, fmt.Errorf("validate Job before planning start: %w", err)
	}
	if !validCommandID(commandID) || !validSHA256(requestSHA256) || now.IsZero() {
		return RoleExecution{}, ProviderStartIntent{}, errors.New("planning start command identity, request digest, and timestamp are required")
	}
	var grant PlanningAuthorization
	found := false
	for _, candidate := range job.PlanningAuthorizations {
		if candidate.ID == authorizationID {
			grant = candidate
			found = true
			break
		}
	}
	if !found {
		return RoleExecution{}, ProviderStartIntent{}, errors.New("PlanningAuthorization was not found in Job")
	}
	if err := grant.ValidateFor(job, principalID, now); err != nil {
		return RoleExecution{}, ProviderStartIntent{}, fmt.Errorf("PlanningAuthorization is not valid for start: %w", err)
	}
	for _, existing := range job.RoleExecutions {
		if existing.PlanningAuthorizationID == grant.ID {
			return RoleExecution{}, ProviderStartIntent{}, errors.New("PlanningAuthorization has already been used to start a RoleExecution")
		}
	}
	createdAt := now.UTC().Round(0)
	if job.Revision >= uint64(^uint64(0)>>1) {
		return RoleExecution{}, ProviderStartIntent{}, errors.New("Job revision exhausted")
	}
	createdRevision := job.Revision + 1
	execution := RoleExecution{
		ID:                       RoleExecutionID(commandID),
		CommandID:                commandID,
		JobID:                    job.ID,
		Role:                     RolePlanner,
		PlanningAuthorizationID:  grant.ID,
		ProviderStartOperationID: ProviderStartOperationID(commandID),
		CreatedAt:                createdAt,
		CreatedRevision:          createdRevision,
	}
	intent := ProviderStartIntent{
		ID:                      ProviderStartOperationID(commandID),
		CommandID:               commandID,
		JobID:                   job.ID,
		RoleExecutionID:         execution.ID,
		PlanningAuthorizationID: grant.ID,
		ProviderID:              grant.ProviderID,
		AccountID:               grant.AccountID,
		ConfigurationSHA256:     grant.ConfigurationSHA256,
		ReadOnlyBoundary:        grant.ReadOnlyBoundary,
		Budget:                  grant.Budget,
		Deadline:                grant.Deadline,
		RequestSHA256:           requestSHA256,
		CreatedAt:               createdAt,
		CreatedRevision:         createdRevision,
	}
	if err := execution.Validate(job); err != nil {
		return RoleExecution{}, ProviderStartIntent{}, err
	}
	if err := intent.Validate(job, execution); err != nil {
		return RoleExecution{}, ProviderStartIntent{}, err
	}
	return execution, intent, nil
}

func (e RoleExecution) Validate(job Job) error {
	if !validPlanningIdentity(e.ID) || !validCommandID(e.CommandID) ||
		!validPlanningIdentity(e.JobID) || !validPlanningIdentity(e.ProviderStartOperationID) {
		return errors.New("RoleExecution identity is invalid")
	}
	if e.ID != RoleExecutionID(e.CommandID) || e.JobID != job.ID ||
		e.CreatedAt.IsZero() || e.CreatedRevision < 2 {
		return errors.New("RoleExecution command identity or creation metadata mismatch")
	}
	switch e.Role {
	case RolePlanner:
		if !validPlanningIdentity(e.PlanningAuthorizationID) || e.TaskID != "" || e.WorktreeID != "" || e.ExecutionPermissionID != "" ||
			e.ProviderStartOperationID != ProviderStartOperationID(e.CommandID) {
			return errors.New("DRAFT planning RoleExecution must be a Taskless PLANNER")
		}
		for _, grant := range job.PlanningAuthorizations {
			if grant.ID != e.PlanningAuthorizationID {
				continue
			}
			if err := grant.Validate(job); err != nil {
				return err
			}
			if e.CreatedAt.Before(grant.IssuedAt) || !e.CreatedAt.Before(grant.Deadline) {
				return errors.New("RoleExecution creation time is outside PlanningAuthorization")
			}
			return nil
		}
		return errors.New("RoleExecution PlanningAuthorization was not found")
	case RoleCoder, RoleReviewer:
		if e.PlanningAuthorizationID != "" || !validPlanningIdentity(e.TaskID) ||
			(e.WorktreeID != "" && !validPlanningIdentity(e.WorktreeID)) ||
			(job.WorktreeLifecycleEnforcedFromRevision > 0 && e.CreatedRevision >= job.WorktreeLifecycleEnforcedFromRevision && e.WorktreeID == "") ||
			!validPlanningIdentity(e.ExecutionPermissionID) {
			return errors.New("Task RoleExecution requires TaskID, WorktreeID, and ExecutionPermissionID")
		}
		task, taskFound := taskByID(job, e.TaskID)
		permission, permissionFound := executionPermissionByID(job, e.ExecutionPermissionID)
		if !taskFound || !permissionFound || task.ContractRevision != permission.ContractRevision ||
			task.JobApprovalID != permission.JobApprovalID || permission.TaskID != task.ID ||
			permission.Role != e.Role || permission.OperationID != e.ProviderStartOperationID {
			return errors.New("RoleExecution does not match its Task and ExecutionPermission")
		}
		if err := permission.Validate(job); err != nil {
			return err
		}
		if e.CreatedRevision <= permission.CreatedRevision ||
			e.CreatedAt.Before(permission.CreatedAt) || !e.CreatedAt.Before(permission.ExpiresAt) {
			return errors.New("RoleExecution creation time is outside ExecutionPermission")
		}
		return nil
	default:
		return errors.New("RoleExecution role is unsupported")
	}
}

func (p ProviderStartIntent) Validate(job Job, execution RoleExecution) error {
	if !validPlanningIdentity(p.ID) || !validCommandID(p.CommandID) ||
		!validPlanningIdentity(p.JobID) || !validPlanningIdentity(p.RoleExecutionID) ||
		!validPlanningIdentity(p.ProviderID) || !validPlanningIdentity(p.AccountID) ||
		!validSHA256(p.ConfigurationSHA256) || !validSHA256(p.RequestSHA256) {
		return errors.New("Provider Start intent identity or digest is invalid")
	}
	if p.CommandID != execution.CommandID || p.JobID != job.ID ||
		p.RoleExecutionID != execution.ID || p.TaskID != execution.TaskID || p.WorktreeID != execution.WorktreeID ||
		p.ExecutionPermissionID != execution.ExecutionPermissionID ||
		p.PlanningAuthorizationID != execution.PlanningAuthorizationID ||
		p.CreatedAt.IsZero() || !p.CreatedAt.Equal(execution.CreatedAt) ||
		p.CreatedRevision != execution.CreatedRevision {
		return errors.New("Provider Start intent does not match RoleExecution")
	}
	switch execution.Role {
	case RolePlanner:
		if p.ID != ProviderStartOperationID(p.CommandID) ||
			p.ID != execution.ProviderStartOperationID ||
			p.TaskID != "" || p.ExecutionPermissionID != "" {
			return errors.New("Planner Provider Start intent identity mismatch")
		}
		for _, grant := range job.PlanningAuthorizations {
			if grant.ID != p.PlanningAuthorizationID {
				continue
			}
			if p.ProviderID != grant.ProviderID || p.AccountID != grant.AccountID ||
				p.ConfigurationSHA256 != grant.ConfigurationSHA256 ||
				p.ReadOnlyBoundary != grant.ReadOnlyBoundary || p.Budget != grant.Budget ||
				!p.Deadline.Equal(grant.Deadline) {
				return errors.New("Provider Start intent exceeds PlanningAuthorization scope")
			}
			return nil
		}
		return errors.New("Provider Start intent PlanningAuthorization was not found")
	case RoleCoder, RoleReviewer:
		if p.PlanningAuthorizationID != "" || !validPlanningIdentity(p.TaskID) ||
			!validPlanningIdentity(p.ExecutionPermissionID) {
			return errors.New("Task Provider Start intent requires TaskID and ExecutionPermissionID")
		}
		permission, found := executionPermissionByID(job, p.ExecutionPermissionID)
		task, taskFound := taskByID(job, p.TaskID)
		if !found || !taskFound || p.ID != permission.OperationID ||
			p.ID != execution.ProviderStartOperationID ||
			permission.TaskID != task.ID || permission.Role != execution.Role ||
			permission.RequestSHA256 != p.RequestSHA256 ||
			permission.ProviderID != p.ProviderID || permission.AccountID != p.AccountID ||
			permission.ConfigurationSHA256 != p.ConfigurationSHA256 ||
			p.Budget != (PlanningBudget{MaxTurns: permission.ReservedTurns, MaxTokens: permission.ReservedTokens}) ||
			p.Deadline.After(task.Deadline) || p.Deadline.After(permission.ExpiresAt) ||
			!p.Deadline.Equal(permission.ExpiresAt) ||
			p.ReadOnlyBoundary != (PlanningReadOnlyBoundary{}) {
			return errors.New("Provider Start intent exceeds Task ExecutionPermission scope")
		}
		if err := permission.Validate(job); err != nil {
			return err
		}
		return nil
	default:
		return errors.New("Provider Start intent role is unsupported")
	}
}

func (j Job) IntakeProvenanceDigest() string {
	if j.IntakeProvenance == nil {
		return ""
	}
	return j.IntakeProvenance.OriginalRequestSHA256
}

func PlanningAuthorizationID(commandID string) string {
	sum := sha256.Sum256([]byte(commandID))
	return "plan_auth_" + hex.EncodeToString(sum[:])
}

func NewPlanningAuthorization(job Job, principalID, commandID string, request PlanningAuthorizationRequest, issuedAt time.Time) (PlanningAuthorization, error) {
	issuedAt = issuedAt.UTC().Round(0)
	deadline := request.Deadline.UTC().Round(0)
	if !deadline.After(issuedAt) {
		if !deadline.After(job.CreatedAt) {
			return PlanningAuthorization{}, errors.New("PlanningAuthorization deadline predates the Job")
		}
		issuedAt = deadline.Add(-time.Nanosecond)
	}
	grant := PlanningAuthorization{
		ID:                  PlanningAuthorizationID(commandID),
		CommandID:           commandID,
		JobID:               job.ID,
		RequestSHA256:       job.IntakeProvenanceDigest(),
		PrincipalID:         principalID,
		ProviderID:          request.ProviderID,
		AccountID:           request.AccountID,
		ConfigurationSHA256: request.ConfigurationSHA256,
		RepositoryID:        request.RepositoryID,
		ReadOnlyBoundary:    request.ReadOnlyBoundary,
		PermittedEffects:    []string{PlanningEffectRepositoryRead},
		Budget:              request.Budget,
		IssuedAt:            issuedAt,
		Deadline:            deadline,
	}
	if err := job.Validate(); err != nil {
		return PlanningAuthorization{}, fmt.Errorf("validate PlanningAuthorization Job: %w", err)
	}
	if job.State != JobDraft || principalID != grant.PrincipalID {
		return PlanningAuthorization{}, errors.New("PlanningAuthorization requires the authenticated HUMAN Principal and a DRAFT Job")
	}
	if err := grant.Validate(job); err != nil {
		return PlanningAuthorization{}, err
	}
	return grant, nil
}

func validPlanningIdentity(value string) bool {
	if !utf8.ValidString(value) || len(value) == 0 || len(value) > 128 || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || strings.ContainsRune("._:/-", r)) {
			return false
		}
	}
	return true
}

func validSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}

func validRepositoryIdentity(value string) bool {
	at := strings.LastIndex(value, "@")
	if at <= 0 {
		return false
	}
	repositoryURL, err := url.Parse(value[:at])
	if err != nil || repositoryURL.Scheme != "https" || repositoryURL.Host == "" ||
		repositoryURL.User != nil || repositoryURL.RawQuery != "" || repositoryURL.Fragment != "" ||
		repositoryURL.Path == "" {
		return false
	}
	commit := value[at+1:]
	if len(commit) != 40 && len(commit) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(commit)
	return err == nil && commit == strings.ToLower(commit) && (len(decoded) == 20 || len(decoded) == 32)
}

func (a PlanningAuthorization) Validate(job Job) error {
	for name, value := range map[string]string{
		"id": a.ID, "command_id": a.CommandID, "job_id": a.JobID, "request_sha256": a.RequestSHA256,
		"principal_id": a.PrincipalID, "provider_id": a.ProviderID, "account_id": a.AccountID,
		"configuration_sha256": a.ConfigurationSHA256,
	} {
		if !validPlanningIdentity(value) {
			return fmt.Errorf("PlanningAuthorization %s is invalid", name)
		}
	}
	if !strings.HasPrefix(a.CommandID, "cmd_") || len(a.CommandID) != len("cmd_")+32 {
		return errors.New("PlanningAuthorization command identity is invalid")
	}
	if commandBytes, err := hex.DecodeString(strings.TrimPrefix(a.CommandID, "cmd_")); err != nil || len(commandBytes) != 16 {
		return errors.New("PlanningAuthorization command identity is invalid")
	}
	if a.ID != PlanningAuthorizationID(a.CommandID) {
		return errors.New("PlanningAuthorization ID does not match command identity")
	}
	if a.JobID != job.ID {
		return errors.New("PlanningAuthorization Job identity mismatch")
	}
	if job.IntakeProvenance == nil {
		return errors.New("PlanningAuthorization requires trusted IntakeProvenance")
	}
	if a.PrincipalID != job.IntakeProvenance.PrincipalID {
		return errors.New("PlanningAuthorization HUMAN Principal mismatch")
	}
	if a.RequestSHA256 != job.IntakeProvenance.OriginalRequestSHA256 {
		return errors.New("PlanningAuthorization original request digest mismatch")
	}
	if !validSHA256(a.ConfigurationSHA256) {
		return errors.New("PlanningAuthorization configuration SHA-256 is invalid")
	}
	if !validRepositoryIdentity(a.RepositoryID) {
		return errors.New("PlanningAuthorization repository identity is invalid")
	}
	if !validPlanningIdentity(a.ReadOnlyBoundary.Reference) || !validSHA256(a.ReadOnlyBoundary.SHA256) {
		return errors.New("PlanningAuthorization read-only boundary identity is invalid")
	}
	if a.ReadOnlyBoundary.Mode != PlanningReadOnlyBoundarySnapshot &&
		a.ReadOnlyBoundary.Mode != PlanningReadOnlyBoundaryDocuments {
		return errors.New("PlanningAuthorization read-only boundary mode is invalid")
	}
	if len(a.PermittedEffects) != 1 || a.PermittedEffects[0] != PlanningEffectRepositoryRead {
		return errors.New("PlanningAuthorization permits an unsupported effect")
	}
	if a.Budget.MaxTurns == 0 || a.Budget.MaxTokens == 0 {
		return errors.New("PlanningAuthorization budget must set positive turn and token limits")
	}
	if a.IssuedAt.IsZero() || a.Deadline.IsZero() || !a.Deadline.After(a.IssuedAt) {
		return errors.New("PlanningAuthorization deadline must follow issue time")
	}
	if a.IssuedAt.Before(job.CreatedAt) {
		return errors.New("PlanningAuthorization predates its Job")
	}
	return nil
}

func (a PlanningAuthorization) ValidateFor(job Job, principalID string, now time.Time) error {
	if err := a.Validate(job); err != nil {
		return err
	}
	if job.State != JobDraft {
		return errors.New("PlanningAuthorization requires a DRAFT Job")
	}
	if principalID != a.PrincipalID {
		return errors.New("PlanningAuthorization principal mismatch")
	}
	if now.IsZero() || now.Before(a.IssuedAt) {
		return errors.New("PlanningAuthorization is not active yet")
	}
	if !now.Before(a.Deadline) {
		return errors.New("PlanningAuthorization has expired")
	}
	return nil
}

func (j Job) Validate() error {
	if !utf8.ValidString(j.Request) {
		return fmt.Errorf("%w: original request must be valid UTF-8", ErrInvalidJob)
	}
	if strings.TrimSpace(j.ID) == "" {
		return fmt.Errorf("%w: id is required", ErrInvalidJob)
	}
	if strings.TrimSpace(j.Request) == "" {
		return fmt.Errorf("%w: original request is required", ErrInvalidJob)
	}
	if j.State != JobDraft && j.State != JobActive {
		return fmt.Errorf("%w: unsupported Job state %q", ErrInvalidJob, j.State)
	}
	if j.Revision == 0 {
		return fmt.Errorf("%w: revision must be positive", ErrInvalidJob)
	}
	if j.CreatedAt.IsZero() || j.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: timestamps are required", ErrInvalidJob)
	}
	if j.UpdatedAt.Before(j.CreatedAt) {
		return fmt.Errorf("%w: updated_at precedes created_at", ErrInvalidJob)
	}
	if j.IntakeProvenance != nil {
		if err := j.IntakeProvenance.Validate(j.Request, j.CreatedAt); err != nil {
			return fmt.Errorf("%w: invalid IntakeProvenance: %v", ErrInvalidJob, err)
		}
	}
	appendOnlyFacts := uint64(len(j.JobContractRevisions) + len(j.JobApprovals) + len(j.Tasks) + len(j.ExecutionPermissions) + len(j.PlanningAuthorizations) + len(j.RoleExecutions) + len(j.ProviderStartObservations) + len(j.ProviderEventObservations) + len(j.ProviderStopIntents) + len(j.ProviderStopObservations) + len(j.ProviderStopDispatchClaims) + len(j.ProviderStopDispatchReleases) + len(j.ProviderResultObservations) + len(j.ProviderUsageObservations) + len(j.JobOutputs) + len(j.PlanningTurnIntents) + len(j.PlanningTurnObservations) + len(j.TaskTurnIntents) + len(j.TaskTurnObservations) + len(j.TaskTurnDispatchClaims) + len(j.TaskTurnDispatchReleases) + len(j.PlanningEffectDecisions) + len(j.PlanningResumeIntents) + len(j.PlanningResumeObservations) + len(j.PlanningBoundaryDecisions))
	if appendOnlyFacts > j.Revision-1 || len(j.RoleExecutions) != len(j.ProviderStartIntents) {
		return fmt.Errorf("%w: append-only fact counts exceed aggregate revisions", ErrInvalidJob)
	}
	seenGrants := make(map[string]struct{}, len(j.PlanningAuthorizations))
	var previousIssue time.Time
	for _, grant := range j.PlanningAuthorizations {
		if err := grant.Validate(j); err != nil {
			return fmt.Errorf("%w: invalid PlanningAuthorization: %v", ErrInvalidJob, err)
		}
		if grant.IssuedAt.After(j.UpdatedAt) || (!previousIssue.IsZero() && grant.IssuedAt.Before(previousIssue)) {
			return fmt.Errorf("%w: PlanningAuthorization append order is invalid", ErrInvalidJob)
		}
		if _, exists := seenGrants[grant.ID]; exists {
			return fmt.Errorf("%w: duplicate PlanningAuthorization ID", ErrInvalidJob)
		}
		seenGrants[grant.ID] = struct{}{}
		previousIssue = grant.IssuedAt
	}
	seenExecutions := make(map[string]struct{}, len(j.RoleExecutions))
	usedGrants := make(map[string]struct{}, len(j.RoleExecutions))
	usedPermissions := make(map[string]struct{}, len(j.RoleExecutions))
	previousExecution := time.Time{}
	for _, execution := range j.RoleExecutions {
		if err := execution.Validate(j); err != nil {
			return fmt.Errorf("%w: invalid RoleExecution: %v", ErrInvalidJob, err)
		}
		if execution.CreatedRevision > j.Revision || execution.CreatedAt.After(j.UpdatedAt) || (!previousExecution.IsZero() && execution.CreatedAt.Before(previousExecution)) {
			return fmt.Errorf("%w: RoleExecution append order is invalid", ErrInvalidJob)
		}
		if _, exists := seenExecutions[execution.ID]; exists {
			return fmt.Errorf("%w: duplicate RoleExecution ID", ErrInvalidJob)
		}
		if execution.Role == RolePlanner {
			if _, exists := usedGrants[execution.PlanningAuthorizationID]; exists {
				return fmt.Errorf("%w: PlanningAuthorization has multiple RoleExecutions", ErrInvalidJob)
			}
			usedGrants[execution.PlanningAuthorizationID] = struct{}{}
		} else {
			if _, exists := usedPermissions[execution.ExecutionPermissionID]; exists {
				return fmt.Errorf("%w: ExecutionPermission has multiple RoleExecutions", ErrInvalidJob)
			}
			usedPermissions[execution.ExecutionPermissionID] = struct{}{}
		}
		seenExecutions[execution.ID] = struct{}{}
		previousExecution = execution.CreatedAt
	}
	seenOperations := make(map[string]struct{}, len(j.ProviderStartIntents))
	for _, intent := range j.ProviderStartIntents {
		var execution RoleExecution
		found := false
		for _, candidate := range j.RoleExecutions {
			if candidate.ID == intent.RoleExecutionID {
				execution = candidate
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%w: Provider Start intent has no RoleExecution", ErrInvalidJob)
		}
		if err := intent.Validate(j, execution); err != nil {
			return fmt.Errorf("%w: invalid Provider Start intent: %v", ErrInvalidJob, err)
		}
		if intent.CreatedAt.After(j.UpdatedAt) {
			return fmt.Errorf("%w: Provider Start intent follows Job update", ErrInvalidJob)
		}
		if _, exists := seenOperations[intent.ID]; exists {
			return fmt.Errorf("%w: duplicate Provider Start operation ID", ErrInvalidJob)
		}
		seenOperations[intent.ID] = struct{}{}
	}
	startIntentsByID := make(map[string]ProviderStartIntent, len(j.ProviderStartIntents))
	for _, intent := range j.ProviderStartIntents {
		startIntentsByID[intent.ID] = intent
	}
	startObservationsByOperation := make(map[string][]ProviderStartObservation)
	seenStartObservationIDs := make(map[string]struct{}, len(j.ProviderStartObservations))
	for _, observation := range j.ProviderStartObservations {
		start, exists := startIntentsByID[observation.OperationID]
		if !exists {
			return fmt.Errorf("%w: Provider Start observation has no Start intent", ErrInvalidJob)
		}
		if err := observation.Validate(j, start); err != nil {
			return fmt.Errorf("%w: invalid Provider Start observation: %v", ErrInvalidJob, err)
		}
		if observation.CreatedAt.After(j.UpdatedAt) || observation.CreatedRevision > j.Revision {
			return fmt.Errorf("%w: Provider Start observation append order is invalid", ErrInvalidJob)
		}
		if _, exists := seenStartObservationIDs[observation.ID]; exists {
			return fmt.Errorf("%w: duplicate Provider Start observation ID", ErrInvalidJob)
		}
		seenStartObservationIDs[observation.ID] = struct{}{}
		startObservationsByOperation[observation.OperationID] = append(startObservationsByOperation[observation.OperationID], observation)
	}
	if j.ProviderLifecycleEnforcedFromRevision > j.Revision {
		return fmt.Errorf("%w: Provider Start observation activation revision exceeds Job revision", ErrInvalidJob)
	}
	if j.ProviderLifecycleEnforcedFromRevision > 0 {
		foundActivationFact := false
		for _, intent := range j.ProviderStartIntents {
			if intent.CreatedRevision == j.ProviderLifecycleEnforcedFromRevision {
				foundActivationFact = true
				break
			}
		}
		if !foundActivationFact {
			for _, observation := range j.ProviderStartObservations {
				if observation.CreatedRevision == j.ProviderLifecycleEnforcedFromRevision {
					foundActivationFact = true
					break
				}
			}
		}
		if !foundActivationFact {
			return fmt.Errorf("%w: Provider lifecycle activation revision has no matching Start fact", ErrInvalidJob)
		}
	}
	for operationID, observations := range startObservationsByOperation {
		for i, observation := range observations {
			if i == 0 {
				if observation.ReconcilesUnknown {
					return fmt.Errorf("%w: first Provider Start observation cannot reconcile UNKNOWN", ErrInvalidJob)
				}
				continue
			}
			previous := observations[i-1]
			if previous.Status != ProviderStartUnknown ||
				observation.CreatedRevision <= previous.CreatedRevision ||
				observation.CreatedAt.Before(previous.CreatedAt) {
				return fmt.Errorf("%w: invalid Provider Start observation sequence for %s", ErrInvalidJob, operationID)
			}
			if observation.Status == ProviderStartUnknown && observation.ReconcilesUnknown {
				return fmt.Errorf("%w: UNKNOWN Provider Start observation cannot reconcile UNKNOWN", ErrInvalidJob)
			}
			if observation.Status != ProviderStartUnknown && !observation.ReconcilesUnknown {
				return fmt.Errorf("%w: terminal Provider Start observation must reconcile UNKNOWN", ErrInvalidJob)
			}
		}
	}
	seenStopIntentIDs := make(map[string]struct{}, len(j.ProviderStopIntents))
	seenStopIntentCommands := make(map[string]struct{}, len(j.ProviderStopIntents))
	seenStopSessions := make(map[string]struct{}, len(j.ProviderStopIntents))
	stopIntentsByOperation := make(map[string]ProviderStopIntent, len(j.ProviderStopIntents))
	for _, intent := range j.ProviderStopIntents {
		if err := intent.Validate(j); err != nil {
			return fmt.Errorf("%w: invalid ProviderStopIntent: %v", ErrInvalidJob, err)
		}
		if intent.CreatedRevision > j.Revision {
			return fmt.Errorf("%w: ProviderStopIntent append revision exceeds Job revision", ErrInvalidJob)
		}
		if _, exists := seenStopIntentIDs[intent.ID]; exists {
			return fmt.Errorf("%w: duplicate ProviderStopIntent identity", ErrInvalidJob)
		}
		if _, exists := seenStopIntentCommands[intent.CommandID]; exists {
			return fmt.Errorf("%w: duplicate ProviderStopIntent command identity", ErrInvalidJob)
		}
		sessionIdentity := intent.RoleExecutionID + "\x00" + intent.SessionID
		if _, exists := seenStopSessions[sessionIdentity]; exists {
			return fmt.Errorf("%w: duplicate ProviderStopIntent for Session", ErrInvalidJob)
		}
		seenStopIntentIDs[intent.ID] = struct{}{}
		seenStopIntentCommands[intent.CommandID] = struct{}{}
		seenStopSessions[sessionIdentity] = struct{}{}
		stopIntentsByOperation[intent.OperationID] = intent
	}
	seenStopObservationIDs := make(map[string]struct{}, len(j.ProviderStopObservations))
	seenStopObservationCommands := make(map[string]struct{}, len(j.ProviderStopObservations))
	stopObservationsByOperation := make(map[string][]ProviderStopObservation)
	for _, observation := range j.ProviderStopObservations {
		if err := observation.Validate(j); err != nil {
			return fmt.Errorf("%w: invalid ProviderStopObservation: %v", ErrInvalidJob, err)
		}
		if observation.CreatedRevision > j.Revision {
			return fmt.Errorf("%w: ProviderStopObservation append revision exceeds Job revision", ErrInvalidJob)
		}
		if _, exists := seenStopObservationIDs[observation.ID]; exists {
			return fmt.Errorf("%w: duplicate ProviderStopObservation identity", ErrInvalidJob)
		}
		if _, exists := seenStopObservationCommands[observation.CommandID]; exists {
			return fmt.Errorf("%w: duplicate ProviderStopObservation command identity", ErrInvalidJob)
		}
		if _, exists := stopIntentsByOperation[observation.OperationID]; !exists {
			return fmt.Errorf("%w: ProviderStopObservation has no Stop intent", ErrInvalidJob)
		}
		seenStopObservationIDs[observation.ID] = struct{}{}
		seenStopObservationCommands[observation.CommandID] = struct{}{}
		stopObservationsByOperation[observation.OperationID] = append(stopObservationsByOperation[observation.OperationID], observation)
	}
	for operationID, observations := range stopObservationsByOperation {
		for i, observation := range observations {
			if i == 0 {
				if observation.ReconcilesUnknown {
					return fmt.Errorf("%w: first ProviderStopObservation cannot reconcile UNKNOWN", ErrInvalidJob)
				}
				continue
			}
			previous := observations[i-1]
			previousUnknown := !previous.Stopped && !previous.Quiescent && previous.EvidenceSHA256 == ""
			currentUnknown := !observation.Stopped && !observation.Quiescent && observation.EvidenceSHA256 == ""
			if previous.Quiescent || (previous.Stopped && !observation.Stopped) ||
				observation.CreatedRevision <= previous.CreatedRevision ||
				observation.CreatedAt.Before(previous.CreatedAt) {
				return fmt.Errorf("%w: ProviderStopObservation order is invalid for Operation %s", ErrInvalidJob, operationID)
			}
			if previousUnknown {
				if currentUnknown || !observation.ReconcilesUnknown {
					return fmt.Errorf("%w: ProviderStopObservation must resolve prior UNKNOWN", ErrInvalidJob)
				}
			} else if observation.ReconcilesUnknown {
				return fmt.Errorf("%w: ProviderStopObservation unexpectedly reconciles UNKNOWN", ErrInvalidJob)
			}
		}
	}
	seenTurns := make(map[string]struct{}, len(j.PlanningTurnIntents))
	turnByID := make(map[string]PlanningTurnIntent, len(j.PlanningTurnIntents))
	turnCounts := make(map[string]uint32)
	previousTurnRevision := make(map[string]uint64)
	previousTurnCreatedAt := make(map[string]time.Time)
	sessionByExecution := make(map[string]string)
	for _, turn := range j.PlanningTurnIntents {
		if err := turn.Validate(j); err != nil {
			return fmt.Errorf("%w: invalid PlanningTurnIntent: %v", ErrInvalidJob, err)
		}
		if turn.CreatedAt.After(j.UpdatedAt) || turn.CreatedRevision > j.Revision {
			return fmt.Errorf("%w: PlanningTurnIntent append order is invalid", ErrInvalidJob)
		}
		if _, exists := seenTurns[turn.ID]; exists {
			return fmt.Errorf("%w: duplicate PlanningTurnIntent ID", ErrInvalidJob)
		}
		seenTurns[turn.ID] = struct{}{}
		turnByID[turn.ID] = turn
		if session, exists := sessionByExecution[turn.RoleExecutionID]; exists && session != turn.ProviderSessionID {
			return fmt.Errorf("%w: Provider Session identity changed within a RoleExecution", ErrInvalidJob)
		}
		sessionByExecution[turn.RoleExecutionID] = turn.ProviderSessionID
		turnCounts[turn.RoleExecutionID]++
		if turn.TurnNumber != turnCounts[turn.RoleExecutionID] {
			return fmt.Errorf("%w: planning turn sequence is not contiguous", ErrInvalidJob)
		}
		if previousRevision := previousTurnRevision[turn.RoleExecutionID]; previousRevision != 0 &&
			(turn.CreatedRevision <= previousRevision || turn.CreatedAt.Before(previousTurnCreatedAt[turn.RoleExecutionID])) {
			return fmt.Errorf("%w: PlanningTurnIntent sequence is not append ordered", ErrInvalidJob)
		}
		previousTurnRevision[turn.RoleExecutionID] = turn.CreatedRevision
		previousTurnCreatedAt[turn.RoleExecutionID] = turn.CreatedAt
	}
	seenProviderEventIDs := make(map[string]struct{}, len(j.ProviderEventObservations))
	seenProviderEventCursors := make(map[string]struct{}, len(j.ProviderEventObservations))
	seenProviderEventCommands := make(map[string]struct{}, len(j.ProviderEventObservations))
	providerEventsBySession := make(map[string][]ProviderEventObservation)
	for _, observation := range j.ProviderEventObservations {
		if err := observation.Validate(j); err != nil {
			return fmt.Errorf("%w: invalid ProviderEventObservation: %v", ErrInvalidJob, err)
		}
		if observation.CreatedRevision > j.Revision {
			return fmt.Errorf("%w: ProviderEventObservation append revision exceeds Job revision", ErrInvalidJob)
		}
		eventIdentity := observation.SessionID + "\x00" + observation.EventID
		cursorIdentity := observation.SessionID + "\x00" + observation.Cursor
		if _, exists := seenProviderEventIDs[eventIdentity]; exists {
			return fmt.Errorf("%w: duplicate Provider event identity", ErrInvalidJob)
		}
		if _, exists := seenProviderEventCursors[cursorIdentity]; exists {
			return fmt.Errorf("%w: duplicate Provider event cursor", ErrInvalidJob)
		}
		if _, exists := seenProviderEventCommands[observation.CommandID]; exists {
			return fmt.Errorf("%w: duplicate Provider event command identity", ErrInvalidJob)
		}
		seenProviderEventIDs[eventIdentity] = struct{}{}
		seenProviderEventCursors[cursorIdentity] = struct{}{}
		seenProviderEventCommands[observation.CommandID] = struct{}{}
		providerEventsBySession[observation.SessionID] = append(providerEventsBySession[observation.SessionID], observation)
	}
	for sessionID, observations := range providerEventsBySession {
		previousCursor := ""
		var previousRevision uint64
		var previousObservedAt time.Time
		for _, observation := range observations {
			if observation.CreatedRevision <= previousRevision ||
				observation.PreviousCursor != previousCursor ||
				observation.ObservedAt.Before(previousObservedAt) {
				return fmt.Errorf("%w: Provider event cursor or observation order is discontinuous for Session %s", ErrInvalidJob, sessionID)
			}
			previousCursor = observation.Cursor
			previousRevision = observation.CreatedRevision
			previousObservedAt = observation.ObservedAt
		}
	}
	observationsByTurn := make(map[string][]PlanningTurnObservation)
	seenObservationIDs := make(map[string]struct{}, len(j.PlanningTurnObservations))
	for _, observation := range j.PlanningTurnObservations {
		turn, exists := turnByID[observation.TurnID]
		if !exists {
			return fmt.Errorf("%w: PlanningTurnObservation has no turn intent", ErrInvalidJob)
		}
		if err := observation.Validate(j, turn); err != nil {
			return fmt.Errorf("%w: invalid PlanningTurnObservation: %v", ErrInvalidJob, err)
		}
		if observation.CreatedAt.After(j.UpdatedAt) || observation.CreatedRevision > j.Revision {
			return fmt.Errorf("%w: PlanningTurnObservation append order is invalid", ErrInvalidJob)
		}
		if _, exists := seenObservationIDs[observation.ID]; exists {
			return fmt.Errorf("%w: duplicate PlanningTurnObservation ID", ErrInvalidJob)
		}
		seenObservationIDs[observation.ID] = struct{}{}
		observationsByTurn[observation.TurnID] = append(observationsByTurn[observation.TurnID], observation)
	}
	previousTurnByExecution := make(map[string]PlanningTurnIntent)
	for _, turn := range j.PlanningTurnIntents {
		if previous, exists := previousTurnByExecution[turn.RoleExecutionID]; exists {
			observations := observationsByTurn[previous.ID]
			if turn.CreatedRevision <= previous.CreatedRevision || turn.CreatedAt.Before(previous.CreatedAt) ||
				turn.ProviderSessionID != previous.ProviderSessionID ||
				len(observations) == 0 || observations[len(observations)-1].Status == PlanningTurnUnknown ||
				observations[len(observations)-1].CreatedRevision >= turn.CreatedRevision ||
				observations[len(observations)-1].CreatedAt.After(turn.CreatedAt) {
				return fmt.Errorf("%w: a new turn does not follow a terminal Provider operation in append order", ErrInvalidJob)
			}
		}
		previousTurnByExecution[turn.RoleExecutionID] = turn
	}
	for turnID, observations := range observationsByTurn {
		for i, observation := range observations {
			if i == 0 {
				if observation.ReconcilesUnknown {
					return fmt.Errorf("%w: first turn observation cannot reconcile UNKNOWN", ErrInvalidJob)
				}
				continue
			}
			previous := observations[i-1]
			if previous.Status != PlanningTurnUnknown || observation.Status == PlanningTurnUnknown ||
				!observation.ReconcilesUnknown || observation.CreatedRevision <= previous.CreatedRevision ||
				observation.CreatedAt.Before(previous.CreatedAt) {
				return fmt.Errorf("%w: invalid PlanningTurnObservation reconciliation sequence", ErrInvalidJob)
			}
		}
		if len(observations) > 2 || (len(observations) == 2 && observations[0].Status != PlanningTurnUnknown) {
			return fmt.Errorf("%w: invalid PlanningTurnObservation history for %s", ErrInvalidJob, turnID)
		}
	}
	seenEffects := make(map[string]struct{}, len(j.PlanningEffectDecisions))
	for _, decision := range j.PlanningEffectDecisions {
		if err := decision.Validate(j); err != nil {
			return fmt.Errorf("%w: invalid PlanningEffectDecision: %v", ErrInvalidJob, err)
		}
		if decision.CreatedAt.After(j.UpdatedAt) || decision.CreatedRevision > j.Revision {
			return fmt.Errorf("%w: PlanningEffectDecision append order is invalid", ErrInvalidJob)
		}
		if _, exists := seenEffects[decision.ID]; exists {
			return fmt.Errorf("%w: duplicate PlanningEffectDecision ID", ErrInvalidJob)
		}
		seenEffects[decision.ID] = struct{}{}
	}
	seenResumes := make(map[string]struct{}, len(j.PlanningResumeIntents))
	previousResumeRevision := make(map[string]uint64)
	previousResumeCreatedAt := make(map[string]time.Time)
	for _, resume := range j.PlanningResumeIntents {
		if err := resume.Validate(j); err != nil {
			return fmt.Errorf("%w: invalid PlanningResumeIntent: %v", ErrInvalidJob, err)
		}
		if resume.CreatedAt.After(j.UpdatedAt) || resume.CreatedRevision > j.Revision {
			return fmt.Errorf("%w: PlanningResumeIntent append order is invalid", ErrInvalidJob)
		}
		if previousRevision := previousResumeRevision[resume.RoleExecutionID]; previousRevision != 0 &&
			(resume.CreatedRevision <= previousRevision || resume.CreatedAt.Before(previousResumeCreatedAt[resume.RoleExecutionID])) {
			return fmt.Errorf("%w: PlanningResumeIntent sequence is not append ordered", ErrInvalidJob)
		}
		if _, exists := seenResumes[resume.ID]; exists {
			return fmt.Errorf("%w: duplicate PlanningResumeIntent ID", ErrInvalidJob)
		}
		seenResumes[resume.ID] = struct{}{}
		previousResumeRevision[resume.RoleExecutionID] = resume.CreatedRevision
		previousResumeCreatedAt[resume.RoleExecutionID] = resume.CreatedAt
	}
	resumeByID := make(map[string]PlanningResumeIntent, len(j.PlanningResumeIntents))
	for _, resume := range j.PlanningResumeIntents {
		resumeByID[resume.ID] = resume
	}
	resumeObservationsByID := make(map[string][]PlanningResumeObservation)
	seenResumeObservationIDs := make(map[string]struct{}, len(j.PlanningResumeObservations))
	for _, observation := range j.PlanningResumeObservations {
		resume, exists := resumeByID[observation.ResumeID]
		if !exists {
			return fmt.Errorf("%w: PlanningResumeObservation has no resume intent", ErrInvalidJob)
		}
		if err := observation.Validate(j, resume); err != nil {
			return fmt.Errorf("%w: invalid PlanningResumeObservation: %v", ErrInvalidJob, err)
		}
		if observation.CreatedAt.After(j.UpdatedAt) || observation.CreatedRevision > j.Revision {
			return fmt.Errorf("%w: PlanningResumeObservation append order is invalid", ErrInvalidJob)
		}
		if _, exists := seenResumeObservationIDs[observation.ID]; exists {
			return fmt.Errorf("%w: duplicate PlanningResumeObservation ID", ErrInvalidJob)
		}
		seenResumeObservationIDs[observation.ID] = struct{}{}
		resumeObservationsByID[observation.ResumeID] = append(resumeObservationsByID[observation.ResumeID], observation)
	}
	previousResumeByExecution := make(map[string]PlanningResumeIntent)
	for _, resume := range j.PlanningResumeIntents {
		if previous, exists := previousResumeByExecution[resume.RoleExecutionID]; exists {
			observations := resumeObservationsByID[previous.ID]
			if len(observations) == 0 || observations[len(observations)-1].Status == PlanningResumeUnknown ||
				observations[len(observations)-1].CreatedRevision >= resume.CreatedRevision ||
				observations[len(observations)-1].CreatedAt.After(resume.CreatedAt) {
				return fmt.Errorf("%w: a new resume does not follow a terminal Provider resume operation in append order", ErrInvalidJob)
			}
		}
		previousResumeByExecution[resume.RoleExecutionID] = resume
	}
	for resumeID, observations := range resumeObservationsByID {
		for i, observation := range observations {
			if i == 0 {
				if observation.ReconcilesUnknown {
					return fmt.Errorf("%w: first resume observation cannot reconcile UNKNOWN", ErrInvalidJob)
				}
				continue
			}
			previous := observations[i-1]
			if previous.Status != PlanningResumeUnknown || observation.Status == PlanningResumeUnknown ||
				!observation.ReconcilesUnknown || observation.CreatedRevision <= previous.CreatedRevision ||
				observation.CreatedAt.Before(previous.CreatedAt) {
				return fmt.Errorf("%w: invalid PlanningResumeObservation reconciliation sequence for %s", ErrInvalidJob, resumeID)
			}
		}
		if len(observations) > 2 || (len(observations) == 2 && observations[0].Status != PlanningResumeUnknown) {
			return fmt.Errorf("%w: invalid PlanningResumeObservation history for %s", ErrInvalidJob, resumeID)
		}
	}
	seenBoundaryDecisions := make(map[string]struct{}, len(j.PlanningBoundaryDecisions))
	for _, decision := range j.PlanningBoundaryDecisions {
		if err := decision.Validate(j); err != nil {
			return fmt.Errorf("%w: invalid PlanningBoundaryDecision: %v", ErrInvalidJob, err)
		}
		if decision.CreatedAt.After(j.UpdatedAt) || decision.CreatedRevision > j.Revision {
			return fmt.Errorf("%w: PlanningBoundaryDecision append order is invalid", ErrInvalidJob)
		}
		if _, exists := seenBoundaryDecisions[decision.ID]; exists {
			return fmt.Errorf("%w: duplicate PlanningBoundaryDecision ID", ErrInvalidJob)
		}
		seenBoundaryDecisions[decision.ID] = struct{}{}
	}
	budgetUsed := make(map[string]uint64)
	for _, turn := range j.PlanningTurnIntents {
		amount := turn.ReservedTokens
		observations := observationsByTurn[turn.ID]
		if len(observations) > 0 && observations[len(observations)-1].Status != PlanningTurnUnknown {
			amount = observations[len(observations)-1].TokensUsed
		}
		if ^uint64(0)-budgetUsed[turn.PlanningAuthorizationID] < amount {
			return fmt.Errorf("%w: planning token usage overflow", ErrInvalidJob)
		}
		budgetUsed[turn.PlanningAuthorizationID] += amount
	}
	for grantID, used := range budgetUsed {
		for _, grant := range j.PlanningAuthorizations {
			if grant.ID == grantID && used > grant.Budget.MaxTokens {
				return fmt.Errorf("%w: planning token usage exceeds authorization budget", ErrInvalidJob)
			}
		}
	}
	seenResultIDs := make(map[string]struct{}, len(j.ProviderResultObservations))
	seenResultCommands := make(map[string]struct{}, len(j.ProviderResultObservations))
	seenTerminalResults := make(map[string]struct{}, len(j.ProviderResultObservations))
	for _, observation := range j.ProviderResultObservations {
		if err := observation.Validate(j); err != nil {
			return fmt.Errorf("%w: invalid ProviderResultObservation: %v", ErrInvalidJob, err)
		}
		if observation.CreatedAt.After(j.UpdatedAt) || observation.CreatedRevision > j.Revision {
			return fmt.Errorf("%w: ProviderResultObservation append order is invalid", ErrInvalidJob)
		}
		if _, exists := seenResultIDs[observation.ID]; exists {
			return fmt.Errorf("%w: duplicate ProviderResultObservation ID", ErrInvalidJob)
		}
		if _, exists := seenResultCommands[observation.CommandID]; exists {
			return fmt.Errorf("%w: duplicate ProviderResultObservation command identity", ErrInvalidJob)
		}
		resultIdentity := observation.OperationID + "\x00" + observation.TurnID
		if _, exists := seenTerminalResults[resultIdentity]; exists {
			return fmt.Errorf("%w: duplicate terminal Provider Result for operation", ErrInvalidJob)
		}
		seenResultIDs[observation.ID] = struct{}{}
		seenResultCommands[observation.CommandID] = struct{}{}
		seenTerminalResults[resultIdentity] = struct{}{}
	}
	seenUsageIDs := make(map[string]struct{}, len(j.ProviderUsageObservations))
	seenUsageCommands := make(map[string]struct{}, len(j.ProviderUsageObservations))
	usageHistory := make(map[string][]ProviderUsageObservation)
	usageBindings := make(map[string]string)
	for _, observation := range j.ProviderUsageObservations {
		if err := observation.Validate(j); err != nil {
			return fmt.Errorf("%w: invalid ProviderUsageObservation: %v", ErrInvalidJob, err)
		}
		if observation.CreatedAt.After(j.UpdatedAt) || observation.CreatedRevision > j.Revision {
			return fmt.Errorf("%w: ProviderUsageObservation append order is invalid", ErrInvalidJob)
		}
		if _, exists := seenUsageIDs[observation.ID]; exists {
			return fmt.Errorf("%w: duplicate ProviderUsageObservation ID", ErrInvalidJob)
		}
		if _, exists := seenUsageCommands[observation.CommandID]; exists {
			return fmt.Errorf("%w: duplicate ProviderUsageObservation command identity", ErrInvalidJob)
		}
		usageIdentity := observation.SessionID + "\x00" + observation.UsageID
		binding := observation.OperationID + "\x00" + observation.TurnID
		if existing, exists := usageBindings[usageIdentity]; exists && existing != binding {
			return fmt.Errorf("%w: Provider Usage identity was rebound to another operation", ErrInvalidJob)
		}
		usageBindings[usageIdentity] = binding
		seenUsageIDs[observation.ID] = struct{}{}
		seenUsageCommands[observation.CommandID] = struct{}{}
		usageHistory[usageIdentity] = append(usageHistory[usageIdentity], observation)
	}
	for usageIdentity, observations := range usageHistory {
		for i, observation := range observations {
			if i == 0 {
				continue
			}
			previous := observations[i-1]
			if previous.Complete || observation.CreatedRevision <= previous.CreatedRevision ||
				observation.CreatedAt.Before(previous.CreatedAt) {
				return fmt.Errorf("%w: Provider Usage history is invalid for %s", ErrInvalidJob, usageIdentity)
			}
		}
	}
	if err := validateJobContractFacts(j); err != nil {
		return err
	}
	if err := validateExecutionPermissionFacts(j); err != nil {
		return err
	}
	if err := validateTaskFacts(j); err != nil {
		return err
	}
	if err := validateProviderStopDispatchFacts(j); err != nil {
		return err
	}
	if err := validateTaskProviderTurnFacts(j); err != nil {
		return err
	}
	if err := validateTaskTurnDispatchFacts(j); err != nil {
		return err
	}
	if err := validateJobOutputs(j); err != nil {
		return err
	}
	return validateWorktreeAssignments(j)
}
