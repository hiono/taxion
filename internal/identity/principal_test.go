package identity

import "testing"

func TestPrincipalIDBindsIssuerAndSubject(t *testing.T) {
	first := PrincipalID("https://issuer.example", "subject-1")
	if first != PrincipalID("https://issuer.example", "subject-1") {
		t.Fatal("PrincipalID is not stable")
	}
	if first == PrincipalID("https://other.example", "subject-1") {
		t.Fatal("PrincipalID did not bind issuer")
	}
	if first == PrincipalID("https://issuer.example", "subject-2") {
		t.Fatal("PrincipalID did not bind subject")
	}
}
