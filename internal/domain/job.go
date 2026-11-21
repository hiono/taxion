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
	JobDraft JobState = "DRAFT"
)

type Job struct {
	ID                     string                  `json:"id"`
	Request                string                  `json:"request"`
	Goal                   string                  `json:"goal,omitempty"`
	State                  JobState                `json:"state"`
	Revision               uint64                  `json:"revision"`
	CreatedAt              time.Time               `json:"created_at"`
	UpdatedAt              time.Time               `json:"updated_at"`
	IntakeProvenance       *IntakeProvenance       `json:"intake_provenance,omitempty"`
	PlanningAuthorizations []PlanningAuthorization `json:"planning_authorizations,omitempty"`
	RoleExecutions         []RoleExecution         `json:"role_executions,omitempty"`
	ProviderStartIntents   []ProviderStartIntent   `json:"provider_start_intents,omitempty"`
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
	RolePlanner = "PLANNER"
)

type RoleExecution struct {
	ID                       string    `json:"id"`
	CommandID                string    `json:"command_id"`
	JobID                    string    `json:"job_id"`
	Role                     string    `json:"role"`
	TaskID                   string    `json:"task_id,omitempty"`
	PlanningAuthorizationID  string    `json:"planning_authorization_id"`
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
		!validPlanningIdentity(e.JobID) || !validPlanningIdentity(e.PlanningAuthorizationID) ||
		!validPlanningIdentity(e.ProviderStartOperationID) {
		return errors.New("RoleExecution identity is invalid")
	}
	if e.ID != RoleExecutionID(e.CommandID) || e.ProviderStartOperationID != ProviderStartOperationID(e.CommandID) {
		return errors.New("RoleExecution command identity mismatch")
	}
	if e.JobID != job.ID || e.Role != RolePlanner || e.TaskID != "" {
		return errors.New("DRAFT planning RoleExecution must be a Taskless PLANNER for the target Job")
	}
	if e.CreatedAt.IsZero() || e.CreatedRevision < 2 {
		return errors.New("RoleExecution creation metadata is invalid")
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
}

func (p ProviderStartIntent) Validate(job Job, execution RoleExecution) error {
	if !validPlanningIdentity(p.ID) || !validCommandID(p.CommandID) ||
		!validPlanningIdentity(p.JobID) || !validPlanningIdentity(p.RoleExecutionID) ||
		!validPlanningIdentity(p.PlanningAuthorizationID) || !validPlanningIdentity(p.ProviderID) ||
		!validPlanningIdentity(p.AccountID) || !validSHA256(p.ConfigurationSHA256) ||
		!validSHA256(p.RequestSHA256) {
		return errors.New("Provider Start intent identity or digest is invalid")
	}
	if p.ID != ProviderStartOperationID(p.CommandID) || p.CommandID != execution.CommandID ||
		p.JobID != job.ID || p.RoleExecutionID != execution.ID ||
		p.PlanningAuthorizationID != execution.PlanningAuthorizationID ||
		p.ID != execution.ProviderStartOperationID ||
		p.CreatedAt.IsZero() || !p.CreatedAt.Equal(execution.CreatedAt) ||
		p.CreatedRevision != execution.CreatedRevision {
		return errors.New("Provider Start intent does not match RoleExecution")
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
	if j.State != JobDraft {
		return fmt.Errorf("%w: unsupported initial state %q", ErrInvalidJob, j.State)
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
	if uint64(len(j.PlanningAuthorizations))+uint64(len(j.RoleExecutions)) > j.Revision-1 ||
		len(j.RoleExecutions) != len(j.ProviderStartIntents) {
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
		if _, exists := usedGrants[execution.PlanningAuthorizationID]; exists {
			return fmt.Errorf("%w: PlanningAuthorization has multiple RoleExecutions", ErrInvalidJob)
		}
		seenExecutions[execution.ID] = struct{}{}
		usedGrants[execution.PlanningAuthorizationID] = struct{}{}
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
	return nil
}
