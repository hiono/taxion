package store

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (s *Store) AppendRequestRecord(id, kind, payload string) error {
	if strings.TrimSpace(id) == "" || kind != "planning" && kind != "execution" {
		return fmt.Errorf("request record requires an ID and planning or execution kind")
	}
	if !json.Valid([]byte(payload)) {
		return fmt.Errorf("request record requires a valid JSON payload")
	}
	return s.UpdateWorkspace(func(state *Workspace) error {
		sequence := uint64(1)
		for _, record := range state.Records.Requests {
			if record.RequestID == id && record.Kind == kind {
				sequence++
			}
		}
		record := RequestRecord{
			RequestID: id, Kind: kind, Sequence: sequence, Payload: payload,
			Hash: fmt.Sprintf("%x", sha256.Sum256([]byte(payload))), At: time.Now().UTC(),
		}
		state.Records.Requests = append(state.Records.Requests, record)
		return nil
	})
}
