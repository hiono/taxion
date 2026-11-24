package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

type PreDispatchError struct {
	OperationID    string
	EvidenceSHA256 string
	Cause          error
}

func NewPreDispatchError(operationID string, cause error) *PreDispatchError {
	code := "pre_dispatch_rejected"
	switch {
	case errors.Is(cause, ErrUnsupportedCapability):
		code = "unsupported_capability"
	case errors.Is(cause, ErrCredentialUnavailable):
		code = "credential_unavailable"
	case errors.Is(cause, ErrProviderInvalidRequest):
		code = "invalid_request"
	}
	sum := sha256.Sum256([]byte("taxion:provider-pre-dispatch:" + operationID + ":" + code))
	return &PreDispatchError{
		OperationID: operationID, EvidenceSHA256: hex.EncodeToString(sum[:]), Cause: cause,
	}
}

func (e *PreDispatchError) Error() string {
	if e == nil {
		return "Provider operation rejected before adapter dispatch"
	}
	return fmt.Sprintf("Provider operation %s rejected before adapter dispatch: %v", e.OperationID, e.Cause)
}

func (e *PreDispatchError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
