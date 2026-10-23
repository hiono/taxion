package publictext

import (
	"regexp"
	"slices"
	"strings"
)

type AuditRecord struct {
	ID      string          `json:"id"`
	Surface string          `json:"surface"`
	Title   string          `json:"title"`
	Body    string          `json:"body"`
	Review  *SemanticReview `json:"review,omitempty"`
}
type AuditResult struct {
	ID              string   `json:"id"`
	Surface         string   `json:"surface"`
	ContentHash     string   `json:"contentHash"`
	Classifications []string `json:"classifications"`
}

var auditArchitectureLeak = regexp.MustCompile(`(?i)(?:internal architecture|private topology|非公開構成)`)

// Audit is read-only and never emits the source prose or proposes a correction.
func Audit(p Profile, records []AuditRecord, protected []string) []AuditResult {
	results := make([]AuditResult, 0, len(records))
	for _, record := range records {
		review := record.Review
		record.Review = nil
		result := AuditResult{ID: record.ID, Surface: record.Surface, ContentHash: digestJSON(record)}
		text := record.Title + "\n" + record.Body
		add := func(classification string) {
			if !slices.Contains(result.Classifications, classification) {
				result.Classifications = append(result.Classifications, classification)
			}
		}
		knownSurface := slices.Contains([]string{"issue", "pull-request", "comment", "review", "release", "milestone", "label", "ci-summary", "generated-text"}, record.Surface)
		if checkAudience(text) != nil || internalTitle.MatchString(record.Title) {
			add("AUDIENCE_MISMATCH")
		}
		if checkLanguage(record.Title, p.TitleLanguage, p) != nil || checkLanguage(record.Body, p.BodyLanguage, p) != nil {
			add("LANGUAGE_MISMATCH")
		}
		if auditArchitectureLeak.MatchString(text) {
			add("INTERNAL_ARCHITECTURE_LEAK")
		}
		if operationField.MatchString(text) || agentReport.MatchString(text) || concreteSession.MatchString(text) ||
			sessionField.MatchString(text) || privateEvidence.MatchString(text) || operationReturn.MatchString(text) {
			add("ORCHESTRATION_LEAK")
		}
		if checkTemporal(text, false) != nil {
			add("TEMPORAL_INTEGRITY_FAILURE")
		}
		if len(protected) == 0 || CheckIdentifiers(text, protected) != nil || secret.MatchString(text) || privatePath.MatchString(text) {
			add("CONFIDENTIALITY_RISK")
		}
		// A deterministic pass cannot establish historical truth or semantic quality.
		if len(result.Classifications) == 0 {
			if review != nil && review.ContentHash == result.ContentHash && review.ProfileHash == ProfileHash(p) &&
				review.Decision == "APPROVE" && strings.TrimSpace(review.WriterSession) != "" &&
				strings.TrimSpace(review.ReviewerSession) != "" && review.WriterSession != review.ReviewerSession &&
				strings.TrimSpace(review.Source) != "" && strings.TrimSpace(record.ID) != "" && knownSurface {
				add("COMPLIANT")
			} else {
				add("REQUIRES_PROJECT_DECISION")
			}
		}
		if strings.TrimSpace(record.ID) == "" {
			add("REQUIRES_PROJECT_DECISION")
		}
		if !knownSurface {
			add("REQUIRES_PROJECT_DECISION")
		}
		results = append(results, result)
	}
	return results
}
