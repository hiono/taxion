package core

import (
	"fmt"
	"strings"
)

// InputDecision describes eligibility, not permission to execute or publish.
type InputDecision struct {
	Version     int              `json:"version"`
	BasisHash   string           `json:"basisHash"`
	Metadata    string           `json:"metadata"`
	Verdict     string           `json:"verdict"`
	Conditions  []InputCondition `json:"conditions"`
	NextActions []string         `json:"nextActions"`
}

type InputCondition struct {
	Code             string `json:"code"`
	Field            string `json:"field"`
	Reason           string `json:"reason"`
	EvidenceSource   string `json:"evidenceSource"`
	Resolution       string `json:"resolution"`
	RequiresApproval bool   `json:"requiresApproval"`
}

func NewInputDecision(basis any) (InputDecision, error) {
	hash, err := CanonicalSHA256(basis)
	return InputDecision{
		Version: 1, BasisHash: hash, Metadata: "absent", Verdict: "accept",
		Conditions: []InputCondition{}, NextActions: []string{},
	}, err
}

func (d *InputDecision) Block(condition InputCondition) {
	condition.Reason = Redact(condition.Reason)
	condition.Resolution = Redact(condition.Resolution)
	d.Verdict = "blocked"
	d.Conditions = append(d.Conditions, condition)
}

func (d InputDecision) Err() error {
	if d.Verdict == "accept" {
		return nil
	}
	return &InputDecisionError{Decision: d}
}

type InputDecisionError struct {
	Decision InputDecision
}

func (e *InputDecisionError) Error() string {
	var reasons []string
	for _, condition := range e.Decision.Conditions {
		reasons = append(reasons, condition.Code+": "+condition.Reason)
	}
	return fmt.Sprintf("input admission blocked: %s", strings.Join(reasons, "; "))
}
