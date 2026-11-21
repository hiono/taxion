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
	PlanningReadOnlyBoundaryRepository = "repository.read-only"
	PlanningEffectRepositoryRead       = "repository.read"
)

type PlanningBudget struct {
	MaxTurns uint32 `json:"max_turns"`
}

type PlanningAuthorization struct {
	ID                  string         `json:"id"`
	JobID               string         `json:"job_id"`
	RequestSHA256       string         `json:"request_sha256"`
	PrincipalID         string         `json:"principal_id"`
	ProviderID          string         `json:"provider_id"`
	AccountID           string         `json:"account_id"`
	ConfigurationSHA256 string         `json:"configuration_sha256"`
	RepositoryID        string         `json:"repository_id"`
	ReadOnlyBoundary    string         `json:"read_only_boundary"`
	PermittedEffects    []string       `json:"permitted_effects"`
	Budget              PlanningBudget `json:"budget"`
	IssuedAt            time.Time      `json:"issued_at"`
	Deadline            time.Time      `json:"deadline"`
}

func (j Job) IntakeProvenanceDigest() string {
	if j.IntakeProvenance == nil {
		return ""
	}
	return j.IntakeProvenance.OriginalRequestSHA256
}

func NewPlanningAuthorization(id string, job Job, principalID, providerID, accountID, configurationSHA256, repositoryID string, maxTurns uint32, issuedAt, deadline time.Time) (PlanningAuthorization, error) {
	grant := PlanningAuthorization{
		ID:                  id,
		JobID:               job.ID,
		RequestSHA256:       job.IntakeProvenanceDigest(),
		PrincipalID:         principalID,
		ProviderID:          providerID,
		AccountID:           accountID,
		ConfigurationSHA256: configurationSHA256,
		RepositoryID:        repositoryID,
		ReadOnlyBoundary:    PlanningReadOnlyBoundaryRepository,
		PermittedEffects:    []string{PlanningEffectRepositoryRead},
		Budget:              PlanningBudget{MaxTurns: maxTurns},
		IssuedAt:            issuedAt.UTC().Round(0),
		Deadline:            deadline.UTC().Round(0),
	}
	if err := grant.ValidateFor(job, principalID, issuedAt); err != nil {
		return PlanningAuthorization{}, err
	}
	return grant, nil
}

func (a PlanningAuthorization) Validate(job Job) error {
	for name, value := range map[string]string{
		"id": a.ID, "job_id": a.JobID, "request_sha256": a.RequestSHA256,
		"principal_id": a.PrincipalID, "provider_id": a.ProviderID,
		"account_id": a.AccountID, "configuration_sha256": a.ConfigurationSHA256,
		"repository_id": a.RepositoryID,
	} {
		if !utf8.ValidString(value) || strings.TrimSpace(value) == "" {
			return fmt.Errorf("PlanningAuthorization %s is required", name)
		}
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
	configurationDigest, err := hex.DecodeString(a.ConfigurationSHA256)
	if err != nil || len(configurationDigest) != sha256.Size || a.ConfigurationSHA256 != strings.ToLower(a.ConfigurationSHA256) {
		return errors.New("PlanningAuthorization configuration SHA-256 is invalid")
	}
	if a.ReadOnlyBoundary != PlanningReadOnlyBoundaryRepository {
		return errors.New("PlanningAuthorization read-only boundary is invalid")
	}
	if len(a.PermittedEffects) != 1 || a.PermittedEffects[0] != PlanningEffectRepositoryRead {
		return errors.New("PlanningAuthorization permits an unsupported effect")
	}
	if a.Budget.MaxTurns == 0 {
		return errors.New("PlanningAuthorization max turns must be positive")
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
	seenGrants := make(map[string]struct{}, len(j.PlanningAuthorizations))
	for _, grant := range j.PlanningAuthorizations {
		if err := grant.Validate(j); err != nil {
			return fmt.Errorf("%w: invalid PlanningAuthorization: %v", ErrInvalidJob, err)
		}
		if _, exists := seenGrants[grant.ID]; exists {
			return fmt.Errorf("%w: duplicate PlanningAuthorization ID", ErrInvalidJob)
		}
		seenGrants[grant.ID] = struct{}{}
	}
	return nil
}
