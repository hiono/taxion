package publictext

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// GuidanceProfile describes the complete source before it is exposed to a writer.
type GuidanceProfile struct {
	ID                         string   `json:"id"`
	IntendedAudience           []string `json:"intendedAudience"`
	Visibility                 []string `json:"supportedVisibility"`
	ProjectTypes               []string `json:"supportedProjectTypes"`
	PrimaryLanguage            string   `json:"primaryLanguage"`
	SecondaryLanguages         []string `json:"permittedSecondaryLanguages"`
	DocumentTypes              []string `json:"documentTypes"`
	TerminologyDomain          string   `json:"terminologyDomain"`
	PublicationSuitability     string   `json:"publicationSuitability"`
	ConfidentialityAssumptions string   `json:"confidentialityAssumptions"`
	Content                    string   `json:"content"`
}

type SemanticReview struct {
	ProfileHash     string `json:"profileHash"`
	ContentHash     string `json:"contentHash"`
	WriterSession   string `json:"writerSession"`
	ReviewerSession string `json:"reviewerSession"`
	Decision        string `json:"decision"`
	Source          string `json:"source"`
}

func digestJSON(value any) string {
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// SelectGuidance must precede generation; incompatible content is never returned.
func SelectGuidance(p Profile, documentType string) (string, string, error) {
	if err := p.Validate(); err != nil {
		return "", "", err
	}
	g := p.Guidance
	if g == nil || strings.TrimSpace(g.ID) == "" || strings.TrimSpace(g.Content) == "" ||
		g.TerminologyDomain == "" || g.ConfidentialityAssumptions == "" ||
		g.PublicationSuitability != "suitable" || g.SecondaryLanguages == nil ||
		!slices.Contains(g.Visibility, p.Visibility) || !slices.Contains(g.ProjectTypes, p.ProjectType) ||
		!slices.Contains(g.DocumentTypes, documentType) || g.PrimaryLanguage != p.PrimaryLanguage {
		return "", "", fmt.Errorf("writing guidance metadata is missing or incompatible with the project, visibility, language or document type")
	}
	for _, audience := range p.IntendedAudience {
		if !slices.Contains(g.IntendedAudience, audience) {
			return "", "", fmt.Errorf("writing guidance does not support the project audience")
		}
	}
	for _, language := range g.SecondaryLanguages {
		if language != p.PrimaryLanguage && !slices.Contains(p.SecondaryLanguages, language) {
			return "", "", fmt.Errorf("writing guidance permits a language outside the project profile")
		}
	}
	return g.Content, digestJSON(g), nil
}

// SemanticHash binds the exact project, selected source, surface and final prose.
func SemanticHash(m Mutation) string {
	return digestJSON(struct{ Provider, Host, Repository, Surface, Title, Body, Guidance string }{
		m.Provider, m.Host, m.Repository, m.Surface, m.Title, m.Body, m.GuidanceDigest,
	})
}

func checkWritingSource(p Profile, m Mutation) error {
	_, hash, err := SelectGuidance(p, m.Surface)
	if err != nil {
		return err
	}
	if m.GuidanceDigest != hash {
		return fmt.Errorf("public mutation requires the selected writing guidance digest; changed or missing sources require regeneration")
	}
	if p.RequireSemanticReview && m.Title+m.Body != "" {
		r := m.Review
		if r == nil || r.ContentHash != SemanticHash(m) || r.ProfileHash != digestJSON(p) || r.Decision != "APPROVE" ||
			strings.TrimSpace(r.WriterSession) == "" || strings.TrimSpace(r.ReviewerSession) == "" ||
			r.WriterSession == r.ReviewerSession || strings.TrimSpace(r.Source) == "" {
			return fmt.Errorf("important public text requires an exact-content independent audience-semantic review")
		}
	}
	return nil
}

func ProfileHash(p Profile) string { return digestJSON(p) }
