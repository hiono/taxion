package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"
	"time"
)

// AuthenticatedPrincipal is created only by the OIDC authentication boundary.
// Its private fields prevent callers from asserting an identity from request data.
type AuthenticatedPrincipal struct {
	id              string
	issuer          string
	subject         string
	authenticatedAt time.Time
}

func newAuthenticatedPrincipal(issuer, subject string, authenticatedAt time.Time) (AuthenticatedPrincipal, error) {
	parsed, err := url.Parse(issuer)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return AuthenticatedPrincipal{}, errors.New("OIDC issuer must be an absolute HTTPS URL")
	}
	if strings.TrimSpace(subject) == "" {
		return AuthenticatedPrincipal{}, errors.New("OIDC subject is required")
	}
	if authenticatedAt.IsZero() {
		return AuthenticatedPrincipal{}, errors.New("authentication time is required")
	}
	sum := sha256.Sum256([]byte(issuer + "\x00" + subject))
	return AuthenticatedPrincipal{
		id:              "human_" + hex.EncodeToString(sum[:]),
		issuer:          issuer,
		subject:         subject,
		authenticatedAt: authenticatedAt.UTC().Round(0),
	}, nil
}

func (p AuthenticatedPrincipal) PrincipalID() string { return p.id }
func (p AuthenticatedPrincipal) Issuer() string { return p.issuer }
func (p AuthenticatedPrincipal) Subject() string { return p.subject }
func (p AuthenticatedPrincipal) AuthenticatedAt() time.Time { return p.authenticatedAt }

func (p AuthenticatedPrincipal) Validate() error {
	expected, err := newAuthenticatedPrincipal(p.issuer, p.subject, p.authenticatedAt)
	if err != nil {
		return err
	}
	if p.id == "" || p.id != expected.id {
		return errors.New("authenticated principal identity is inconsistent")
	}
	return nil
}
