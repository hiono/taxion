package controller

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/store"
)

// RecordFailureAnalysis keeps a Human decision and proposed lesson with its exact result.
func RecordFailureAnalysis(db *store.Store, record store.FailureRecord) (store.FailureRecord, error) {
	if record.ID == "" || record.TaskID == "" || record.DispatchID == "" || record.ResultHash == "" ||
		strings.TrimSpace(record.Cause) == "" || strings.TrimSpace(record.Correction) == "" ||
		strings.TrimSpace(record.LessonCandidate) == "" || record.Actor == "" || record.DecisionSource == "" ||
		len(record.ValidationEvidence) == 0 || !record.At.IsZero() {
		return record, fmt.Errorf("taxion: finding requires exact result, cause, correction, candidate, validation and sourced Human decision")
	}
	record.ValidationEvidence = slices.Clone(record.ValidationEvidence)
	err := db.UpdateWorkspace(func(state *store.Workspace) error {
		dispatch, ok := state.Graph.GraphDispatch(record.DispatchID)
		if !ok || dispatch.TaskID != record.TaskID || dispatch.Result == nil || active(dispatch.Status) {
			return fmt.Errorf("taxion: finding requires a terminal recorded result")
		}
		if err := validateResultReceipt(state, dispatch); err != nil {
			return err
		}
		body, err := json.Marshal(dispatch.Result)
		if err != nil || receiptHash(string(body)) != record.ResultHash {
			return fmt.Errorf("taxion: finding result hash changed")
		}
		for _, evidence := range record.ValidationEvidence {
			if evidence != "dispatch:"+dispatch.ID+":result" && !slices.Contains(dispatch.Result.Evidence, evidence) {
				return fmt.Errorf("taxion: finding validation reference requires evidence in its saved result")
			}
		}
		for _, saved := range state.Records.Findings {
			if saved.ID != record.ID {
				continue
			}
			supplied := record
			supplied.At = saved.At
			if !reflect.DeepEqual(supplied, saved) {
				return fmt.Errorf("taxion: finding replay differs from saved decision")
			}
			record = saved
			return nil
		}
		record.At = time.Now().UTC()
		state.Records.Findings = append(state.Records.Findings, record)
		return nil
	})
	return record, err
}
