package core

import (
	"encoding/json"
	"slices"
	"strings"
)

const ReviewProgressVersion = 1
const ReviewProgressMaxBytes = 64 * 1024
const ReviewProgressMaxRecords = 32

// ReviewProgress is reported coverage, not a verified result or approval.
// Each submission replaces the reported scope; coverage is never inferred.
type ReviewProgress struct {
	Version         int               `json:"version"`
	DispatchID      string            `json:"dispatchId"`
	InstructionHash string            `json:"instructionHash"`
	CoderDispatchID string            `json:"coderDispatchId"`
	TargetSHA       string            `json:"targetSha"`
	TargetTreeSHA   string            `json:"targetTreeSha,omitempty"`
	ReviewedScope   []string          `json:"reviewedScope"`
	UnreviewedScope []string          `json:"unreviewedScope"`
	Findings        []DispatchFinding `json:"findings"`
	Evidence        []string          `json:"evidence"`
}

func (p ReviewProgress) Validate(reviewer, coder GraphDispatch) error {
	if p.Version != ReviewProgressVersion || reviewer.Recipient != "REVIEWER" ||
		reviewer.Relationship != RelationshipIndependent || reviewer.RelatedID != coder.ID ||
		coder.Recipient != "CODER" || coder.Status != DispatchSucceeded || coder.Result == nil ||
		p.DispatchID != reviewer.ID || p.InstructionHash != reviewer.InstructionHash ||
		p.CoderDispatchID != coder.ID || p.TargetSHA == "" ||
		p.TargetSHA != reviewer.Instruction.TargetSHA || p.TargetSHA != coder.Result.OutputSHA ||
		p.TargetTreeSHA != reviewer.Instruction.TargetTreeSHA || p.TargetTreeSHA != coder.Result.OutputTreeSHA {
		return graphError("review progress requires the exact Reviewer instruction and successful Coder target")
	}
	return p.ValidateContent()
}

func (p ReviewProgress) ValidateContent() error {
	if p.Version != ReviewProgressVersion {
		return graphError("unknown review progress version")
	}
	body, err := json.Marshal(p)
	if err != nil || len(body) > ReviewProgressMaxBytes {
		return graphError("review progress exceeds 64 KiB")
	}
	bounded := func(values []string) bool {
		if values == nil || len(values) > 128 {
			return false
		}
		seen := map[string]bool{}
		for _, value := range values {
			if strings.TrimSpace(value) == "" || len(value) > 2048 || seen[value] {
				return false
			}
			seen[value] = true
		}
		return true
	}
	if !bounded(p.ReviewedScope) || !bounded(p.UnreviewedScope) || !bounded(p.Evidence) ||
		p.Findings == nil || len(p.Findings) > 128 ||
		len(p.ReviewedScope) != 0 && len(p.Evidence) == 0 {
		return graphError("review progress requires bounded explicit scope, findings and evidence arrays")
	}
	for _, scope := range p.ReviewedScope {
		if slices.Contains(p.UnreviewedScope, scope) {
			return graphError("review progress reports the same scope as reviewed and unreviewed")
		}
	}
	for _, finding := range p.Findings {
		if !slices.Contains([]string{"CRITICAL", "HIGH", "MEDIUM", "LOW", "INFO"}, finding.Severity) ||
			strings.TrimSpace(finding.Title) == "" || len(finding.Title) > 2048 ||
			len(finding.File) > 2048 || len(finding.Lines) > 2048 ||
			!bounded(finding.Evidence) || len(finding.Evidence) == 0 {
			return graphError("invalid reported review finding")
		}
		for _, evidence := range finding.Evidence {
			if !slices.Contains(p.Evidence, evidence) {
				return graphError("review progress finding references absent evidence")
			}
		}
	}
	return nil
}
