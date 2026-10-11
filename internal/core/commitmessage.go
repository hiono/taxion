package core

import (
	"fmt"
	"regexp"
	"strings"
)

const CommitMessagePolicy = "conventional-contextual-v1"
const CommitMessageInstruction = "Every authored commit uses a Conventional Commits subject (type(scope): description, optional !), a blank separator, and a contextual body with at least one intent(scope):, decision(scope):, rejected(scope):, constraint(scope): or learned(scope): line explaining actual reasoning. Optional Git trailers follow the action lines. Independent review checks that the recorded reasoning matches the work."

var conventionalSubject = regexp.MustCompile(`^[a-z][a-z0-9-]*(\([^()\s]+\))?!?: \S.*$`)
var contextualAction = regexp.MustCompile(`^(intent|decision|rejected|constraint|learned)\([^()\s]+\): \S.*$`)
var commitTrailer = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*: \S.*$`)

// ValidateCommitMessage enforces Taxion's authored-commit contract. Semantic
// accuracy of the reasoning remains part of independent review.
func ValidateCommitMessage(message string) error {
	lines := strings.Split(strings.TrimRight(message, "\r\n"), "\n")
	if len(lines) < 3 || !conventionalSubject.MatchString(strings.TrimSuffix(lines[0], "\r")) {
		return fmt.Errorf("commit subject must use Conventional Commits and include a contextual body")
	}
	if strings.TrimSpace(lines[1]) != "" {
		return fmt.Errorf("commit subject and contextual body require a blank separator")
	}
	actions, trailers := 0, false
	for _, raw := range lines[2:] {
		line := strings.TrimSuffix(raw, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !trailers && contextualAction.MatchString(line) {
			actions++
			continue
		}
		if commitTrailer.MatchString(line) || strings.HasPrefix(line, "BREAKING CHANGE: ") && strings.TrimSpace(strings.TrimPrefix(line, "BREAKING CHANGE: ")) != "" {
			trailers = true
			continue
		}
		return fmt.Errorf("commit body requires contextual action lines before optional trailers: %q", line)
	}
	if actions == 0 {
		return fmt.Errorf("commit body requires at least one contextual reasoning line: intent(scope), decision(scope), rejected(scope), constraint(scope) or learned(scope)")
	}
	return nil
}
