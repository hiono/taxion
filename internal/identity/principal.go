package identity

import (
	"crypto/sha256"
	"encoding/hex"
)

// PrincipalID derives the stable internal identity from a verified OIDC issuer and subject.
func PrincipalID(issuer, subject string) string {
	sum := sha256.Sum256([]byte(issuer + "\x00" + subject))
	return "human_" + hex.EncodeToString(sum[:])
}
