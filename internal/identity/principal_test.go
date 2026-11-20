package identity

import (
	"strings"
	"testing"
	"time"
)

func TestNewAuthenticatedPrincipalUsesIssuerAndSubject(t *testing.T) {
	at := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	first, err := NewAuthenticatedPrincipal("https://issuer.example", "subject-123", at)
	if err != nil {
		t.Fatalf("NewAuthenticatedPrincipal() error = %v", err)
	}
	second, err := NewAuthenticatedPrincipal("https://issuer.example", "subject-123", at.Add(time.Minute))
	if err != nil {
		t.Fatalf("NewAuthenticatedPrincipal() error = %v", err)
	}
	otherIssuer, err := NewAuthenticatedPrincipal("https://other.example", "subject-123", at)
	if err != nil {
		t.Fatalf("NewAuthenticatedPrincipal() error = %v", err)
	}
	otherSubject, err := NewAuthenticatedPrincipal("https://issuer.example", "subject-456", at)
	if err != nil {
		t.Fatalf("NewAuthenticatedPrincipal() error = %v", err)
	}

	if first.PrincipalID() != second.PrincipalID() {
		t.Fatal("PrincipalID changed when authentication time changed")
	}
	if first.PrincipalID() == otherIssuer.PrincipalID() || first.PrincipalID() == otherSubject.PrincipalID() {
		t.Fatal("PrincipalID must include both issuer and subject")
	}
	if !strings.HasPrefix(first.PrincipalID(), "human_") {
		t.Fatalf("PrincipalID = %q, want human_ prefix", first.PrincipalID())
	}
	if err := first.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestNewAuthenticatedPrincipalRejectsInvalidClaims(t *testing.T) {
	at := time.Now().UTC()
	cases := []struct {
		name, issuer, subject string
		at                    time.Time
	}{
		{name: "http issuer", issuer: "http://issuer.example", subject: "subject", at: at},
		{name: "missing issuer", issuer: "", subject: "subject", at: at},
		{name: "missing subject", issuer: "https://issuer.example", subject: " ", at: at},
		{name: "missing time", issuer: "https://issuer.example", subject: "subject"},
		{name: "issuer userinfo", issuer: "https://user@issuer.example", subject: "subject", at: at},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewAuthenticatedPrincipal(tc.issuer, tc.subject, tc.at); err == nil {
				t.Fatal("NewAuthenticatedPrincipal() succeeded with invalid claims")
			}
		})
	}
}
