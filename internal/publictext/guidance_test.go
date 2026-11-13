package publictext

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestGuidanceCompatibilityAndSourceBinding(t *testing.T) {
	p := configuredProfile(t)
	p.ProjectType, p.Visibility = "public-oss", "public"
	p.Guidance.ProjectTypes, p.Guidance.Visibility = []string{"public-oss"}, []string{"public"}
	for _, tc := range []struct {
		name   string
		change func(*Profile)
		reject bool
	}{
		{"OSS", func(*Profile) {}, false},
		{"internal manual", func(p *Profile) { p.Guidance.ProjectTypes = []string{"enterprise-manual"} }, true},
		{"internal compatible", func(p *Profile) {
			p.ProjectType = "enterprise-manual"
			p.Visibility = "private"
			p.Guidance.ProjectTypes = []string{"enterprise-manual"}
			p.Guidance.Visibility = []string{"private"}
		}, false},
		{"missing metadata", func(p *Profile) { p.Guidance = nil }, true},
		{"visibility", func(p *Profile) { p.Guidance.Visibility = []string{"private"} }, true},
		{"language", func(p *Profile) { p.Guidance.PrimaryLanguage = "ja" }, true},
		{"audience", func(p *Profile) { p.Guidance.IntendedAudience = []string{"maintainers"} }, true},
		{"document", func(p *Profile) { p.Guidance.DocumentTypes = []string{"enterprise-manual"} }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			var selected Profile
			if err := json.Unmarshal(data, &selected); err != nil {
				t.Fatal(err)
			}
			tc.change(&selected)
			content, _, err := SelectGuidance(selected, "issue")
			if (err != nil) != tc.reject || tc.reject && content != "" {
				t.Fatalf("content exposed or wrong decision: %q %v", content, err)
			}
		})
	}
	_, digest, err := SelectGuidance(p, "issue")
	if err != nil {
		t.Fatal(err)
	}
	m := Mutation{Provider: p.Provider, Host: p.Host, Repository: p.Repository, Surface: "issue", Title: "Preserve setup choices", Body: "Invalid confirmation preserves existing choices.", GuidanceDigest: digest}
	if err := checkWritingSource(p, m); err != nil {
		t.Fatal(err)
	}
	p.Guidance.Content += " Additional source."
	if checkWritingSource(p, m) == nil {
		t.Fatal("substituted guidance bypassed gate")
	}
	m.GuidanceDigest = ""
	if checkWritingSource(p, m) == nil {
		t.Fatal("metadata-free source bypassed gate")
	}
}

func TestSemanticReviewExactTextProfileAndSeparateSession(t *testing.T) {
	p := configuredProfile(t)
	p.RequireSemanticReview = true
	_, digest, err := SelectGuidance(p, "comment")
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		"Report sent.", "Hand over to the assigned department owner.",
		"Retire the attempt.", "The release is planned for tomorrow.",
		"Use `APIClient` to preserve the selected values.",
		"See https://forge.example/example/application/-/issues/1 for the existing problem.",
	} {
		m := Mutation{Provider: p.Provider, Host: p.Host, Repository: p.Repository, Surface: "comment", Body: body, GuidanceDigest: digest}
		if checkWritingSource(p, m) == nil {
			t.Fatal("lint was substituted for semantic review", body)
		}
		m.Review = &SemanticReview{ContentHash: SemanticHash(m), ProfileHash: ProfileHash(p), WriterSession: "writer", ReviewerSession: "independent-review", Decision: "APPROVE", Source: "review:exact-text"}
		if err := checkWritingSource(p, m); err != nil {
			t.Fatal(err)
		}
		m.Body += " Changed."
		if checkWritingSource(p, m) == nil {
			t.Fatal("changed text reused review")
		}
		m.Body = body
		m.Review.ReviewerSession = "writer"
		if checkWritingSource(p, m) == nil {
			t.Fatal("same-session semantic approval accepted")
		}
		m.Review.ReviewerSession = "independent-review"
		m.Statement.At = time.Now().UTC()
		if checkWritingSource(p, m) == nil {
			t.Fatal("changed statement timestamp reused semantic review")
		}
		m.Statement = StatementContext{}
		m.Statement.Claims = []ReferenceClaim{{URL: "https://forge.example/example/application/-/issues/1", State: "EXISTS"}}
		if checkWritingSource(p, m) == nil {
			t.Fatal("changed reference claims reused semantic review")
		}
		m.Review.ContentHash = SemanticHash(m)
		m.Statement.Claims[0].State = "COMPLETED"
		if checkWritingSource(p, m) == nil {
			t.Fatal("changed reference state reused semantic review")
		}
		m.Statement = StatementContext{}
		m.Review.ContentHash = SemanticHash(m)
		changed := p
		changed.TitleStyle = "different title contract"
		if checkWritingSource(changed, m) == nil {
			t.Fatal("changed profile reused semantic review")
		}
	}
}

func TestExplicitProfilePreparationAndNoInheritance(t *testing.T) {
	data, err := os.ReadFile("testdata/consumer-profiles.json")
	if err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/profiles.json"
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TAXION_COMMUNICATION_PROFILES", path)
	if _, err := LoadProfile("gitlab", "forge.example", "example/application"); err == nil {
		t.Fatal("fixture authorized production mutation")
	}
	if _, err := LoadProfile("gitlab", "forge.example", "example/other"); err == nil {
		t.Fatal("profile inherited across project")
	}
	p := configuredProfile(t)
	if _, _, err := SelectGuidance(p, "comment"); err != nil {
		t.Fatal("explicit profile cannot be evaluated", err)
	}
	p.TitleStyle = ""
	if p.Validate() == nil {
		t.Fatal("incomplete production product settings accepted")
	}
}

func TestLanguageQuotationAndAudienceFixtures(t *testing.T) {
	p := configuredProfile(t)
	p.PrimaryLanguage, p.TitleLanguage, p.BodyLanguage = "ja", "ja", "ja"
	p.SecondaryLanguages = []string{"en"}
	for _, text := range []string{"設定を保持します。`APIClient`を使用します。", "設定を保持します。\n> [en] Invalid confirmation preserves existing choices."} {
		if err := checkLanguage(text, "ja", p); err != nil {
			t.Fatal("valid identifier or quotation rejected", err)
		}
	}
	if checkLanguage("設定のrepair completedを報告します。", "ja", p) == nil {
		t.Fatal("mixed operational prose accepted")
	}
	for _, text := range []string{"初回設定: 最終 yes が未適用になった報告と確認入力の再質問", "停止済みの不要な試行を履歴を保って退役させる"} {
		if checkAudience(text) == nil {
			t.Fatal("audience fixture accepted", text)
		}
	}
}

func TestReadOnlyAuditClassificationAndSemanticLimit(t *testing.T) {
	p := configuredProfile(t)
	protected := strings.Fields(os.Getenv("TAXION_PUBLIC_PRIVATE_IDENTIFIER_SHA256"))
	records := []AuditRecord{
		{ID: "1", Surface: "issue", Title: "Preserve setup choices", Body: "Invalid confirmation preserves existing choices."},
		{ID: "2", Surface: "comment", Body: "operation owner: orchestrator"},
		{ID: "3", Surface: "release", Body: "日本語の説明"},
		{ID: "4", Surface: "review", Body: "internal architecture"},
		{ID: "5", Surface: "milestone", Body: "private-example-47"},
	}
	results := Audit(p, records, protected)
	for i, want := range []string{"REQUIRES_PROJECT_DECISION", "AUDIENCE_MISMATCH", "LANGUAGE_MISMATCH", "INTERNAL_ARCHITECTURE_LEAK", "CONFIDENTIALITY_RISK"} {
		if !slices.Contains(results[i].Classifications, want) {
			t.Fatalf("record %d: %#v", i, results[i])
		}
	}
	records[0].Review = &SemanticReview{ProfileHash: ProfileHash(p), ContentHash: results[0].ContentHash, WriterSession: "writer", ReviewerSession: "reviewer", Decision: "APPROVE", Source: "private:review"}
	if got := Audit(p, records[:1], protected)[0]; !slices.Contains(got.Classifications, "COMPLIANT") {
		t.Fatal(got)
	}
	records[0].Body += " Changed."
	if got := Audit(p, records[:1], protected)[0]; slices.Contains(got.Classifications, "COMPLIANT") {
		t.Fatal("changed audit text reused review")
	}
}
