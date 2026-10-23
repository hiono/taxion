package publictext

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// Profile is an explicit project contract, never inferred from repository prose.
type Profile struct {
	ProjectType                  string            `json:"projectType"`
	Configuration                string            `json:"configuration"`
	Guidance                     *GuidanceProfile  `json:"guidance"`
	RequireSemanticReview        bool              `json:"requireSemanticReview"`
	TitleStyle                   string            `json:"titleStyle,omitempty"`
	IssueStructure               []string          `json:"issueStructure,omitempty"`
	MergeRequestStructure        []string          `json:"mergeRequestStructure,omitempty"`
	ConfidentialIdentifierPolicy string            `json:"confidentialIdentifierPolicy,omitempty"`
	ReleaseNoteLanguage          string            `json:"releaseNoteLanguage,omitempty"`
	CorrectionPolicy             string            `json:"correctionPolicy,omitempty"`
	Version                      int               `json:"version"`
	Provider                     string            `json:"provider"`
	Host                         string            `json:"host"`
	Repository                   string            `json:"repository"`
	PrimaryLanguage              string            `json:"primaryLanguage"`
	SecondaryLanguages           []string          `json:"secondaryLanguages"`
	TitleLanguage                string            `json:"titleLanguage"`
	BodyLanguage                 string            `json:"bodyLanguage"`
	TechnicalTerms               []string          `json:"technicalTerms"`
	Glossary                     map[string]string `json:"glossary"`
	Visibility                   string            `json:"visibility"`
	IntendedAudience             []string          `json:"intendedAudience"`
}

type Profiles struct {
	Version  int       `json:"version"`
	Projects []Profile `json:"projects"`
}

func DecodeProfiles(input io.Reader) (Profiles, error) {
	decoder := json.NewDecoder(io.LimitReader(input, 1048577))
	decoder.DisallowUnknownFields()
	var profiles Profiles
	if err := decoder.Decode(&profiles); err != nil {
		return Profiles{}, fmt.Errorf("communication profiles: invalid JSON: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Profiles{}, fmt.Errorf("communication profiles require exactly one JSON object")
	}
	if profiles.Version != 1 || len(profiles.Projects) == 0 {
		return Profiles{}, fmt.Errorf("communication profiles require version 1 and explicit projects")
	}
	seen := map[string]bool{}
	for _, profile := range profiles.Projects {
		if err := profile.Validate(); err != nil {
			return Profiles{}, err
		}
		key := profile.Provider + ":" + profile.Host + "/" + profile.Repository
		if seen[key] {
			return Profiles{}, fmt.Errorf("communication profiles contain a duplicate project binding")
		}
		seen[key] = true
	}
	return profiles, nil
}

func (p Profile) Validate() error {
	host, err := url.Parse("https://" + p.Host)
	if err != nil || host.Hostname() == "" || host.Host != p.Host || host.User != nil ||
		host.Path != "" || host.RawQuery != "" || host.Fragment != "" || strings.ContainsAny(p.Host, "\\?#") ||
		strings.HasPrefix(p.Host, "-") ||
		p.Version != 1 || p.Provider != "github" && p.Provider != "gitlab" ||
		p.Provider == "github" && p.Host != "github.com" ||
		!regexp.MustCompile(`^[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)+$`).MatchString(p.Repository) ||
		p.Provider == "github" && strings.Count(p.Repository, "/") != 1 {
		return fmt.Errorf("communication profile requires an exact supported forge and project binding")
	}
	for _, segment := range strings.Split(p.Repository, "/") {
		if segment == "." || segment == ".." {
			return fmt.Errorf("communication profile project cannot contain relative path segments")
		}
	}
	if !slices.Contains([]string{"public", "private"}, p.Visibility) || len(p.IntendedAudience) == 0 {
		return fmt.Errorf("communication profile requires visibility and intended audience")
	}
	if !slices.Contains([]string{"public-oss", "internal-product", "enterprise-manual"}, p.ProjectType) ||
		!slices.Contains([]string{"production", "fixture"}, p.Configuration) {
		return fmt.Errorf("communication profile requires explicit project type and configuration purpose")
	}
	if p.Configuration == "production" && p.ProjectType == "internal-product" &&
		(strings.TrimSpace(p.TitleStyle) == "" || len(p.IssueStructure) == 0 || len(p.MergeRequestStructure) == 0 ||
			strings.TrimSpace(p.ConfidentialIdentifierPolicy) == "" || strings.TrimSpace(p.CorrectionPolicy) == "" ||
			!slices.Contains(append([]string{p.PrimaryLanguage}, p.SecondaryLanguages...), p.ReleaseNoteLanguage)) {
		return fmt.Errorf("production product profiles require explicit title style, Issue/MR structure, confidentiality, release language and correction policy")
	}
	for _, audience := range p.IntendedAudience {
		if !slices.Contains([]string{"users", "contributors", "maintainers", "security-researchers", "release-consumers"}, audience) {
			return fmt.Errorf("communication profile audience must describe project readers")
		}
	}
	allowed := append([]string{p.PrimaryLanguage}, p.SecondaryLanguages...)
	for _, language := range allowed {
		if language != "ja" && language != "en" {
			return fmt.Errorf("communication profile supports explicit ja or en language policies")
		}
	}
	if !slices.Contains(allowed, p.TitleLanguage) || !slices.Contains(allowed, p.BodyLanguage) ||
		p.TechnicalTerms == nil || p.Glossary == nil || p.SecondaryLanguages == nil {
		return fmt.Errorf("communication profile requires title/body languages, terminology and glossary")
	}
	for term, replacement := range p.Glossary {
		if strings.TrimSpace(term) == "" || strings.TrimSpace(replacement) == "" || term == replacement {
			return fmt.Errorf("communication profile contains an invalid glossary entry")
		}
	}
	for _, term := range p.TechnicalTerms {
		if strings.TrimSpace(term) == "" {
			return fmt.Errorf("communication profile contains an empty technical term")
		}
	}
	return nil
}

// LoadProfile never falls back to Taxion's profile for another repository.
func LoadProfile(provider, host, repository string) (Profile, error) {
	path := os.Getenv("TAXION_COMMUNICATION_PROFILES")
	if path == "" {
		return Profile{}, fmt.Errorf("publication requires TAXION_COMMUNICATION_PROFILES with an explicit project profile")
	}
	input, err := os.Open(path)
	if err != nil {
		return Profile{}, fmt.Errorf("communication profiles could not be opened")
	}
	defer input.Close()
	profiles, err := DecodeProfiles(input)
	if err != nil {
		return Profile{}, err
	}
	for _, profile := range profiles.Projects {
		if profile.Provider == provider && profile.Host == host && profile.Repository == repository {
			if profile.Configuration != "production" {
				return Profile{}, fmt.Errorf("fixture communication profiles cannot authorize production mutations")
			}
			return profile, nil
		}
	}
	return Profile{}, fmt.Errorf("no communication profile is configured for the selected project")
}

var audienceMismatch = regexp.MustCompile(`(?i)(?:初回設定.{0,50}報告と.{0,30}再質問|停止済み.{0,30}試行.{0,30}退役|(?:blocking|required|operation) owner\s*[:=]|(?:handoff required|governance handoff|repair completed|re-review dispatched)|(?:orchestrator|controller)\s+(?:state|handoff|arbitration)\s*[:=]|(?:オーナー変更|エスカレーション|再依頼)(?:を実施|しました|が必要))`)
var internalTitle = regexp.MustCompile(`(?i)(?:^(?:repair completed|re-review dispatched|finding report|role handoff required|governance handoff)\b|(?:報告と|確認入力の再質問|試行を.*退役))`)
var commitTitle = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_-]*)(\([^()\r\n]*\))?(!?)\s*[:：]`)
var classifiedTitle = regexp.MustCompile(`(?i)^(?:M[0-9]+(?:[-－][A-Za-z0-9]+|追補)*\s*[:：]|v?[0-9]+\.[0-9]+(?:\.[0-9]+)?(?:\s+UX)?\s*[:：]|(?:次版|後続候補|将来検討)\s*[:：])`)
var webLink = regexp.MustCompile(`https?://[^\s<>()\x60]+`)
var fencedCode = regexp.MustCompile("(?s)```.*?```")
var latinWord = regexp.MustCompile(`[A-Za-z][A-Za-z0-9_.:/@+-]*`)

func checkAudience(text string) error {
	if audienceMismatch.MatchString(text) {
		return fmt.Errorf("AUDIENCE_MISMATCH: describe the change, user impact, problem or limitation")
	}
	return nil
}

func proseText(text string) string {
	text = fencedCode.ReplaceAllString(text, "")
	text = inlineCode.ReplaceAllString(text, "")
	return webLink.ReplaceAllString(text, "")
}

func checkLanguage(text, language string, profile Profile) error {
	text = proseText(text)
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		match := regexp.MustCompile(`^>\s+\[(ja|en)\]\s+(.+)$`).FindStringSubmatch(strings.TrimSpace(line))
		if len(match) == 3 {
			if !slices.Contains(profile.SecondaryLanguages, match[1]) ||
				checkLanguage(match[2], match[1], profile) != nil {
				return fmt.Errorf("quotation language differs from the explicit project language policy")
			}
			lines[i] = ""
		}
	}
	text = strings.Join(lines, "\n")
	if strings.TrimSpace(text) == "" {
		return nil
	}
	hasJapanese := false
	for _, r := range text {
		switch {
		case unicode.In(r, unicode.Hiragana, unicode.Katakana, unicode.Han):
			hasJapanese = true
		case unicode.IsLetter(r) && !unicode.In(r, unicode.Latin) && !(language == "ja" && r >= '\u3000' && r <= '\u30ff'):
			return fmt.Errorf("public text contains prose outside the configured language policy")
		}
	}
	if language == "en" && hasJapanese {
		return fmt.Errorf("public text language differs from the configured English prose policy")
	}
	if language == "ja" {
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			for _, term := range profile.TechnicalTerms {
				line = regexp.MustCompile(`\b`+regexp.QuoteMeta(term)+`\b`).ReplaceAllString(line, "")
			}
			if latinWord.MatchString(line) {
				return fmt.Errorf("public text mixes English prose into the configured Japanese presentation")
			}
		}
	}
	for term, replacement := range profile.Glossary {
		matches := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(term) + `\b`)
		if matches.MatchString(text) && replacement != "" {
			return fmt.Errorf("public text contains terminology outside the configured glossary")
		}
	}
	return nil
}

// CheckPresentation checks the exact text. Rendering must happen before this
// function and approval; it deliberately never rewrites approved prose.
func CheckPresentation(profile Profile, title, body string) error {
	if err := profile.Validate(); err != nil {
		return err
	}
	if err := checkAudience(title + "\n" + body); err != nil {
		return err
	}
	if strings.ContainsAny(title, "\r\n") || len(title) > 256 || internalTitle.MatchString(proseText(title)) {
		return fmt.Errorf("public title must be a single reader-facing description of a change or problem")
	}
	if profile.TitleStyle == "plain-statement" {
		plain := strings.TrimSpace(strings.ReplaceAll(title, "`", ""))
		commit := commitTitle.FindStringSubmatch(plain)
		if classifiedTitle.MatchString(plain) || len(commit) > 0 &&
			(commit[2] != "" || commit[3] != "" || !slices.Contains(profile.TechnicalTerms, commit[1])) {
			return fmt.Errorf("public title style requires a plain statement without commit types, planning identifiers or release prefixes")
		}
	}
	if len(body) > 60000 {
		return fmt.Errorf("public body exceeds the publication limit")
	}
	if err := checkLanguage(title, profile.TitleLanguage, profile); err != nil {
		return err
	}
	if err := checkLanguage(body, profile.BodyLanguage, profile); err != nil {
		return err
	}
	return Check(title+"\n"+body, false)
}
