// Package publictext prepares and checks reader-facing text before publication approval.
package publictext

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

var privatePath = regexp.MustCompile(`(?i)(?:/(?:home|Users)/[a-z0-9_.-]+/[^ \n` + "`" + `]+|[a-z]:\\Users\\[a-z0-9_.-]+\\[^ \n` + "`" + `]+|(?:/|\\)[^ \n]*session-state(?:/|\\)|(?:/|\\)[^ \n]*checkpoint[^ \n]*\.(?:json|ya?ml))`)
var secret = regexp.MustCompile(`(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|glpat-[A-Za-z0-9_-]{20,}|-----BEGIN (?:[A-Z]+ )?PRIVATE KEY-----|(?i:(?:api[_-]?key|access[_-]?token|password)\s*[:=]\s*["']?[A-Za-z0-9_+/=-]{20,}))`)
var operationField = regexp.MustCompile(`(?m)["']?(?:blockingItem|safeCurrentState|requiredOwner|requiredDecision|completedAuthorizedChanges|reviewerPacketHash|packetSha256)["']?\s*:`)
var sessionField = regexp.MustCompile(`["']?runtimeSessionId["']?\s*:`)
var concreteSession = regexp.MustCompile(`(?i)(?:runtimeSessionId|sessionId)["']?\s*:\s*["']?[a-f0-9-]{32,}`)
var privateEvidence = regexp.MustCompile(`(?i)(?:\.git/[^\s` + "`" + `]*(?:audit|review|packet)|(?:packet|reviewerPacket|session|runtimeSessionId)\s*(?:ID|hash|SHA-256|:)\s*[:=]?\s*["']?[a-f0-9-]{32,}|session\s+[a-f0-9]{8}-[a-f0-9-]{27,})`)
var agentReport = regexp.MustCompile(`(?i)(?:repairしました|repairを実行|re-review(?:します|を実行)|別\s*session(?:で|の|へ).*(?:review|レビュー)|REQUEST_CHANGES\s+finding|Coder\s*へ戻|Reviewer packet.*(?:生成|永続化)|人間へ戻らず|一括継続|repair completed|re-review dispatched)`)
var rawEnum = regexp.MustCompile(`\b(?:APPROVE|REQUEST_CHANGES|CONTRACT_BLOCKED|USAGE_EXCEEDED|RELEASE_READY|TAXION_HOTFIX_RELEASED)\b`)
var inlineCode = regexp.MustCompile("`[^`\\n]+`")
var operationReturn = regexp.MustCompile(`(?m)(?:^\s*(?:gate|decision|reviewedCommit|packetSha256)\s*:|"(?:gate|decision|reviewedCommit|packetSha256)"\s*:)`)

// Check never rewrites content or includes a matched value in an error.
// Documents may explain public schemas and enums; messages may quote an inline
// identifier, but cannot use raw operation returns as their narrative.
func Check(text string, document bool) error {
	for _, rule := range []struct {
		name string
		re   *regexp.Regexp
	}{
		{"machine-local path", privatePath}, {"potential credential", secret},
		{"private execution evidence", privateEvidence}, {"agent-to-agent progress", agentReport},
		{"private runtime identifier", concreteSession},
	} {
		if rule.re.MatchString(text) {
			return fmt.Errorf("public text contains %s; write a reader-facing technical summary", rule.name)
		}
	}
	if operationField.MatchString(text) {
		return fmt.Errorf("public text contains private operation fields; summarize the technical facts")
	}
	if !document {
		if sessionField.MatchString(text) || operationReturn.MatchString(text) {
			return fmt.Errorf("public text contains a raw operation return; summarize the technical facts")
		}
		prose := inlineCode.ReplaceAllString(text, "")
		if rawEnum.MatchString(prose) || rawEnum.MatchString(text) && strings.TrimSpace(prose) == "" {
			return fmt.Errorf("public text contains an internal status value; use a reader-facing description")
		}
	}
	return nil
}

// CheckIdentifiers uses normalized token digests so CI need not receive private names.
// The caller must explicitly provide the protected digest list.
func CheckIdentifiers(text string, digests []string) error {
	for _, digest := range digests {
		if decoded, err := hex.DecodeString(digest); err != nil || len(decoded) != sha256.Size || digest != strings.ToLower(digest) {
			return fmt.Errorf("invalid protected identifier digest")
		}
	}
	for _, token := range regexp.MustCompile(`[a-z0-9_]+(?:-[a-z0-9_]+)*`).FindAllString(strings.ToLower(text), -1) {
		sum := sha256.Sum256([]byte(token))
		for _, digest := range digests {
			if hex.EncodeToString(sum[:]) == digest {
				return fmt.Errorf("public text contains a protected identifier; generalize unrelated environment details")
			}
		}
	}
	return nil
}

type Review struct {
	Outcome           string   `json:"outcome"`
	Commit            string   `json:"commit"`
	FeedbackAddressed []string `json:"feedbackAddressed"`
	Changes           []string `json:"changes"`
	Validation        []string `json:"validation"`
	RemainingConcerns []string `json:"remainingConcerns"`
}

// RenderReview is opt-in, before hashing/approval. It never infers that checks ran.
func RenderReview(review Review) (string, error) {
	label, ok := map[string]string{
		"APPROVE":              "Approved",
		"REQUEST_CHANGES":      "Changes requested",
		"repair completed":     "Review feedback addressed",
		"re-review dispatched": "A follow-up review has been requested",
		"CONTRACT_BLOCKED":     "Cannot proceed because a required prerequisite is unavailable.",
		"USAGE_EXCEEDED":       "The automated review could not run because the configured usage limit was reached.",
	}[review.Outcome]
	if !ok {
		return "", fmt.Errorf("unsupported public review outcome")
	}
	if !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(review.Commit) {
		return "", fmt.Errorf("public review requires an exact source commit")
	}
	sections := []struct {
		name  string
		items []string
	}{
		{"Feedback addressed", review.FeedbackAddressed},
		{"Changes made", review.Changes},
		{"Validation", review.Validation},
		{"Remaining concerns", review.RemainingConcerns},
	}
	var body strings.Builder
	fmt.Fprintf(&body, "%s\n\nCommit: `%s`\n", label, review.Commit)
	for _, section := range sections {
		if len(section.items) == 0 {
			return "", fmt.Errorf("public review requires explicit %s, including not run or not applicable where appropriate", section.name)
		}
		fmt.Fprintf(&body, "\n## %s\n", section.name)
		for _, item := range section.items {
			if strings.TrimSpace(item) == "" {
				return "", fmt.Errorf("public review section contains an empty item")
			}
			fmt.Fprintf(&body, "\n- %s\n", item)
		}
	}
	if err := Check(body.String(), false); err != nil {
		return "", err
	}
	return body.String(), nil
}
