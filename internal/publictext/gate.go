package publictext

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"
)

// ErrMutationRejected identifies a policy refusal before any remote write.
var ErrMutationRejected = errors.New("public-text policy rejected the mutation before sending")

// Mutation describes human-visible content, not an arbitrary provider payload.
type Mutation struct {
	GuidanceDigest string           `json:"guidanceDigest"`
	Review         *SemanticReview  `json:"semanticReview,omitempty"`
	Provider       string           `json:"provider"`
	Host           string           `json:"host"`
	Repository     string           `json:"repository"`
	Surface        string           `json:"surface"`
	Title          string           `json:"title"`
	Body           string           `json:"body"`
	Statement      StatementContext `json:"statement"`
}

var markdownLink = regexp.MustCompile(`!?\[[^\]]*\]\(([^)\s]+)(?:\s[^)]*)?\)`)
var resultAssertions = []struct {
	pattern *regexp.Regexp
	state   string
	kind    string
}{
	{regexp.MustCompile(`(?i)(?:\bCI (?:has )?passed\b|\btests? (?:have )?passed\b|検証.{0,12}(?:PASS済み|成功済み)|CI.{0,12}(?:PASS|成功)(?:済み|しました))`), "COMPLETED", "ci"},
	{regexp.MustCompile(`(?i)\b(?:CI|pipelines?|jobs?|tests?|checks?|validation)(?: runs?)?\s+(?:(?:has|have|is|are|was|were)\s+)?(?:completed|finished|succeeded|passed)(?:\s+successfully)?\b`), "COMPLETED", "ci"},
	{regexp.MustCompile(`(?i)\b(?:CI|pipeline|job|tests?|checks?|validation)(?: run)?\s+(?:(?:is|are|was|were|has been|have been)\s+)?(?:green|passing|successful|success)\b`), "COMPLETED", "ci"},
	{regexp.MustCompile(`(?i)(?:\b(?:PR|pull request|merge request) (?:has been |is )?merged\b|(?:mainへ|main に)反映済み|merge済み)`), "COMPLETED", "merge"},
	{regexp.MustCompile(`(?i)(?:\brelease (?:is |has been )?(?:available|published)\b|(?:release|リリース|配布物).{0,12}(?:公開済み|利用可能))`), "PUBLISHED", "release"},
	{regexp.MustCompile(`(?i)(?:\breview (?:is |has been )?approved\b|review済み)`), "APPROVED", "review"},
}

func assertionReference(m Mutation, target, kind string) bool {
	parsed, err := url.Parse(target)
	if err != nil || parsed.Host != m.Host {
		return false
	}
	path := strings.TrimPrefix(parsed.Path, "/"+m.Repository+"/")
	if m.Provider == "gitlab" {
		path = strings.TrimPrefix(path, "-/")
	}
	switch kind {
	case "ci":
		return parsed.Fragment == "" && regexp.MustCompile(`^(?:actions/runs|jobs|pipelines)/[0-9]+$`).MatchString(path)
	case "merge":
		return parsed.Fragment == "" && regexp.MustCompile(`^(?:pull|merge_requests)/[0-9]+$`).MatchString(path)
	case "release":
		return parsed.Fragment == "" && strings.HasPrefix(path, "releases/")
	case "review":
		return strings.HasPrefix(path, "pull/") && strings.HasPrefix(parsed.Fragment, "pullrequestreview-")
	}
	return false
}

func CheckMutationText(m Mutation) (Profile, error) {
	profile, err := LoadProfile(m.Provider, m.Host, m.Repository)
	if err != nil {
		return Profile{}, err
	}
	if err := checkWritingSource(profile, m); err != nil {
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
				if claim.State == assertion.state && strings.Contains(line, claim.URL) && assertionReference(m, claim.URL, assertion.kind) {
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
