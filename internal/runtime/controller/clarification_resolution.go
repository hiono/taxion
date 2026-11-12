package controller

import (
	"fmt"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

type ClarificationAnswerCandidate struct {
	Receipt ClarificationReceipt `json:"receipt"`
	Answer  ClarificationAnswer  `json:"answer"`
}

type ClarificationResolutionRequest struct {
	ID           string               `json:"id"`
	ExpectedHash string               `json:"expectedHash"`
	Selected     ClarificationReceipt `json:"selected"`
	Role         string               `json:"role"`
	Actor        string               `json:"actor"`
	Source       string               `json:"source"`
	Reason       string               `json:"reason"`
}

type ClarificationResolution struct {
	Selected        ClarificationReceipt `json:"selected"`
	Basis           ClarificationReceipt `json:"basis"`
	ObservationHash string               `json:"observationHash"`
	Role            string               `json:"role"`
	Actor           string               `json:"actor"`
	Decision        core.TaskDecision    `json:"decision"`
	At              time.Time            `json:"at"`
}

// ResolveClarificationConflict records a source-attributed judgment, not acceptance or authority.
func ResolveClarificationConflict(db *store.Store, input ClarificationResolutionRequest) (Clarification, error) {
	var result Clarification
	identity, err := core.CanonicalSHA256(input)
	if err != nil {
		return result, err
	}
	err = db.UpdateWorkspace(func(state *store.Workspace) error {
		history, err := clarifications(*state)
		if err != nil {
			return err
		}
		for _, saved := range history {
			if saved.Request.ID == input.ID {
				result = saved
			}
		}
		if result.Conflict == nil || input.Role != "HUMAN" && input.Role != result.Recipient ||
			strings.TrimSpace(input.Actor) == "" || strings.TrimSpace(input.Source) == "" || strings.TrimSpace(input.Reason) == "" {
			return fmt.Errorf("taxion: resolution requires conflicting evidence, the routed respondent or human, and explicit provenance")
		}
		if prior := result.Resolution; prior != nil {
			if err := validateClarificationResolution(*state, result); err != nil {
				return err
			}
			if prior.Decision.ID == identity {
				return nil
			}
			return fmt.Errorf("taxion: recorded resolution is immutable; new evidence requires a new assessment")
		}
		report, err := assessClarification(*state, input.ID)
		if err != nil {
			return err
		}
		if input.ExpectedHash == "" || input.ExpectedHash != report.Decision.BasisHash {
			return fmt.Errorf("taxion: clarification assessment changed; observe current evidence before selecting an answer")
		}
		found := false
		for _, candidate := range report.Candidates {
			found = found || candidate.Receipt == input.Selected
		}
		if !found {
			return fmt.Errorf("taxion: selected answer does not match this question's canonical evidence")
		}
		result.Phase = "clarification.resolution"
		result.Resolution = &ClarificationResolution{
			Selected: input.Selected, Basis: report.Receipt, ObservationHash: input.ExpectedHash,
			Role: input.Role, Actor: input.Actor,
			Decision: core.TaskDecision{ID: identity, Source: input.Source, Text: input.Reason},
			At:       time.Now().UTC(),
		}
		return appendClarification(state, result)
	})
	return result, err
}

func clarificationAnswerCandidates(state store.Workspace, id string) ([]ClarificationAnswerCandidate, error) {
	candidates := []ClarificationAnswerCandidate{}
	for _, record := range state.Records.Requests {
		if record.Kind != "planning" || record.RequestID != "clarification:"+id {
			continue
		}
		var saved Clarification
		if err := core.DecodeGraphInput(strings.NewReader(record.Payload), &saved); err != nil {
			return nil, err
		}
		answer := saved.Answer
		if saved.Phase == "clarification.conflict" {
			answer = saved.Conflict
		} else if saved.Phase != "clarification.answer" {
			continue
		}
		if answer == nil {
			return nil, fmt.Errorf("taxion: canonical answer evidence is missing")
		}
		if err := hydrateClarificationAnswer(state, answer); err != nil {
			return nil, err
		}
		candidates = append(candidates, ClarificationAnswerCandidate{
			Receipt: ClarificationReceipt{record.RequestID, record.Sequence, record.Hash}, Answer: *answer,
		})
	}
	return candidates, nil
}

func validateClarificationResolution(state store.Workspace, saved Clarification) error {
	resolution := saved.Resolution
	identity, err := core.CanonicalSHA256(ClarificationResolutionRequest{
		ID: saved.Request.ID, ExpectedHash: resolution.ObservationHash, Selected: resolution.Selected,
		Role: resolution.Role, Actor: resolution.Actor, Source: resolution.Decision.Source, Reason: resolution.Decision.Text,
	})
	if resolution.Role != "HUMAN" && resolution.Role != saved.Recipient ||
		strings.TrimSpace(resolution.Actor) == "" || strings.TrimSpace(resolution.Decision.Source) == "" ||
		strings.TrimSpace(resolution.Decision.Text) == "" || err != nil || resolution.Decision.ID != identity ||
		len(resolution.ObservationHash) != 64 || resolution.At.IsZero() {
		return fmt.Errorf("taxion: saved clarification resolution lacks explicit provenance")
	}
	candidates, err := clarificationAnswerCandidates(state, saved.Request.ID)
	if err != nil {
		return err
	}
	selected := false
	for _, candidate := range candidates {
		selected = selected || candidate.Receipt == resolution.Selected && candidate.Receipt.Sequence <= resolution.Basis.Sequence
	}
	var prior ClarificationReceipt
	for _, record := range state.Records.Requests {
		if record.Kind != "planning" || record.RequestID != "clarification:"+saved.Request.ID {
			continue
		}
		var entry Clarification
		if err := core.DecodeGraphInput(strings.NewReader(record.Payload), &entry); err != nil {
			return err
		}
		if entry.Resolution != nil && entry.Resolution.Decision.ID == resolution.Decision.ID {
			if selected && prior == resolution.Basis {
				return nil
			}
			break
		}
		prior = ClarificationReceipt{record.RequestID, record.Sequence, record.Hash}
	}
	return fmt.Errorf("taxion: saved clarification resolution evidence is unavailable or mismatched")
}
