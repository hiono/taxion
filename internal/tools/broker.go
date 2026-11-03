package tools

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var brokerReference = regexp.MustCompile(`^env:TAXION_BROKER_[A-Z][A-Z0-9_]*$`)

func (t MCPTool) checkBrokerShape() error {
	if t.CredentialRef == "" && t.OperationID == "" && t.ExpiresAt == "" {
		return nil
	}
	if !brokerReference.MatchString(t.CredentialRef) || !validName(t.OperationID) || len(t.OperationID) > 128 {
		return fmt.Errorf("taxion: broker requires a dedicated env:TAXION_BROKER_ reference and exact operation ID")
	}
	if _, err := time.Parse(time.RFC3339, t.ExpiresAt); err != nil {
		return fmt.Errorf("taxion: broker requires an explicit RFC3339 expiry")
	}
	return nil
}

func (p Policy) HasBroker() bool {
	for _, tool := range p.MCP {
		if tool.CredentialRef != "" {
			return true
		}
	}
	return false
}

// Broker secrets are host-only, even for roles which do not use a broker.
func WithoutBrokerCredentials(env []string) []string {
	result := make([]string, 0, len(env))
	for _, item := range env {
		name, _, _ := strings.Cut(item, "=")
		if !strings.HasPrefix(strings.ToUpper(name), "TAXION_BROKER_") {
			result = append(result, item)
		}
	}
	return result
}

func (p Policy) brokerCredential(tool MCPTool) (string, error) {
	value := os.Getenv(strings.TrimPrefix(tool.CredentialRef, "env:"))
	if value == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n") {
		return "", fmt.Errorf("taxion: broker credential missing or invalid; value withheld")
	}
	encoded, err := json.Marshal(p)
	escaped, _ := json.Marshal(value)
	if err != nil || strings.Contains(string(encoded), string(escaped[1:len(escaped)-1])) {
		return "", fmt.Errorf("taxion: broker policy must contain references, not credential values")
	}
	return strings.TrimPrefix(tool.CredentialRef, "env:") + "=" + value, nil
}

func (p Policy) CheckBrokerCredentials() error {
	for _, tool := range p.MCP {
		if tool.CredentialRef != "" {
			if _, err := p.brokerCredential(tool); err != nil {
				return err
			}
		}
	}
	return nil
}

// Consume before starting the trusted process. A failure or crash never makes
// the approval reusable; the external side effect may already have occurred.
func (p Policy) consumeOperation(ctx context.Context, tool MCPTool) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("taxion: broker operation cancelled or expired")
	}
	digest := sha256.Sum256([]byte(tool.OperationID))
	path := filepath.Join(p.EvidenceDirectory, fmt.Sprintf("operation-%x.json", digest))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("taxion: broker operation already attempted or audit unavailable; do not retry")
	}
	defer file.Close()
	basis, _ := json.Marshal(tool)
	record := struct {
		OperationID string `json:"operationId"`
		BasisHash   string `json:"basisHash"`
		State       string `json:"state"`
	}{tool.OperationID, fmt.Sprintf("sha256:%x", sha256.Sum256(basis)), "attempted-outcome-unknown"}
	if err := json.NewEncoder(file).Encode(record); err != nil {
		return fmt.Errorf("taxion: broker audit unavailable; do not retry")
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("taxion: broker audit sync failed; do not retry")
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("taxion: broker audit close failed; do not retry")
	}
	directory, err := os.Open(p.EvidenceDirectory)
	if err != nil {
		return fmt.Errorf("taxion: broker audit directory unavailable; do not retry")
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("taxion: broker durable audit unavailable on this filesystem; do not retry")
	}
	return nil
}
