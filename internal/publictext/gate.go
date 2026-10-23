package publictext

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Mutation describes human-visible content, not an arbitrary provider payload.
type Mutation struct {
	Provider   string           `json:"provider"`
	Host       string           `json:"host"`
	Repository string           `json:"repository"`
	Surface    string           `json:"surface"`
	Title      string           `json:"title"`
	Body       string           `json:"body"`
	Statement  StatementContext `json:"statement"`
}

var markdownLink = regexp.MustCompile(`!?\[[^\]]*\]\(([^)\s]+)(?:\s[^)]*)?\)`)
var resultAssertions = []struct {
	pattern *regexp.Regexp
	state   string
}{
	{regexp.MustCompile(`(?i)(?:\bCI (?:has )?passed\b|\btests? (?:have )?passed\b|検証.{0,12}(?:PASS済み|成功済み)|CI.{0,12}(?:PASS|成功)(?:済み|しました))`), "COMPLETED"},
	{regexp.MustCompile(`(?i)(?:\b(?:PR|pull request|merge request) (?:has been |is )?merged\b|(?:mainへ|main に)反映済み|merge済み)`), "COMPLETED"},
	{regexp.MustCompile(`(?i)(?:\brelease (?:is |has been )?(?:available|published)\b|(?:release|リリース|配布物).{0,12}(?:公開済み|利用可能))`), "PUBLISHED"},
	{regexp.MustCompile(`(?i)(?:\breview (?:is |has been )?approved\b|review済み)`), "APPROVED"},
}

func CheckMutationText(m Mutation) (Profile, error) {
	profile, err := LoadProfile(m.Provider, m.Host, m.Repository)
	if err != nil {
		return Profile{}, err
	}
	if !slices.Contains([]string{"issue", "pull-request", "comment", "review", "release", "milestone", "tag", "ci-summary", "wiki"}, m.Surface) {
		return Profile{}, fmt.Errorf("unsupported public text surface")
	}
	if err := CheckPresentation(profile, m.Title, m.Body); err != nil {
		return Profile{}, err
	}
	digests := strings.Fields(os.Getenv("TAXION_PUBLIC_PRIVATE_IDENTIFIER_SHA256"))
	if len(digests) == 0 {
		return Profile{}, fmt.Errorf("publication requires TAXION_PUBLIC_PRIVATE_IDENTIFIER_SHA256")
	}
	if err := CheckIdentifiers(m.Title+"\n"+m.Body, digests); err != nil {
		return Profile{}, err
	}
	return profile, nil
}

// CheckMutation is mandatory immediately before any adapter write, including
// calls that did not pass through the controller's approval path.
func CheckMutation(ctx context.Context, m Mutation, resolve ResolveReference) error {
	if _, err := CheckMutationText(m); err != nil {
		return err
	}
	text := m.Title + "\n" + m.Body
	if m.Statement.At.IsZero() || m.Statement.At.After(time.Now().UTC()) {
		return fmt.Errorf("publication requires a nonfuture statement timestamp")
	}
	for _, link := range markdownLink.FindAllStringSubmatch(text, -1) {
		if !strings.HasPrefix(link[1], "https://") {
			return fmt.Errorf("public mutation links require absolute verifiable HTTPS references")
		}
	}
	for _, line := range strings.Split(text, "\n") {
		for _, assertion := range resultAssertions {
			if !assertion.pattern.MatchString(line) {
				continue
			}
			backed := false
			for _, claim := range m.Statement.Claims {
				if claim.State == assertion.state && strings.Contains(line, claim.URL) {
					backed = true
				}
			}
			if !backed {
				return fmt.Errorf("public completion claim requires a same-line direct reference in the matching state")
			}
		}
	}
	if err := CheckForgeReferences(ctx, text, m.Statement, m.Provider, m.Host, m.Repository, resolve); err != nil {
		return err
	}
	// The transport adds only an opaque deduplication marker, never a renderer.
	_, err := CheckMutationText(m)
	return err
}
