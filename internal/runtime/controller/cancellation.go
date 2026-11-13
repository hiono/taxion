package controller

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/store"
)

// CancelPublication invalidates an exact unsent intent, never a remote record.
func CancelPublication(db *store.Store, request store.PublicationCancellation) (store.ExternalUpdate, error) {
	var result store.ExternalUpdate
	alreadyCanceled := errors.New("exact cancellation already applied")
	if request.ID == "" || request.ContentHash == "" || request.ApprovalSource == "" ||
		strings.TrimSpace(request.Reason) == "" || strings.TrimSpace(request.Actor) == "" ||
		strings.TrimSpace(request.Authorization) == "" || request.PreviousState != "PENDING" || !request.At.IsZero() {
		return result, fmt.Errorf("publication cancellation requires exact ID, PENDING state, approval, hash, reason and sourced authorization")
	}
	err := db.UpdateWorkspace(func(state *store.Workspace) error {
		for i := range state.Records.Updates {
			saved := &state.Records.Updates[i]
			if saved.ID != request.ID {
				continue
			}
			if saved.Hash != request.ContentHash || saved.ApprovalSource != request.ApprovalSource {
				return fmt.Errorf("publication cancellation content or approval differs")
			}
			if saved.Status == "CANCELED" {
				for _, audit := range state.Records.Cancellations {
					if audit.ID == request.ID && audit.Reason == request.Reason &&
						audit.Actor == request.Actor && audit.Authorization == request.Authorization {
						result = *saved
						return alreadyCanceled
					}
				}
				return fmt.Errorf("publication cancellation differs from existing authorization")
			}
			if saved.Status != "PENDING" || saved.Claim != "" || saved.RemoteID != "" {
				return fmt.Errorf("only an unclaimed, unsent PENDING publication can be canceled")
			}
			request.At = time.Now().UTC()
			state.Records.Cancellations = append(state.Records.Cancellations, request)
			saved.Status = "CANCELED"
			result = *saved
			return nil
		}
		return fmt.Errorf("publication cancellation target not found")
	})
	if errors.Is(err, alreadyCanceled) {
		return result, nil
	}
	return result, err
}
