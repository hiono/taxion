package publictext

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func configuredProfile(t *testing.T) Profile {
	t.Helper()
	data, err := os.ReadFile("testdata/consumer-profiles.json")
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := DecodeProfiles(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "profiles.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TAXION_COMMUNICATION_PROFILES", path)
	sum := sha256.Sum256([]byte("private-example-47"))
	t.Setenv("TAXION_PUBLIC_PRIVATE_IDENTIFIER_SHA256", hex.EncodeToString(sum[:]))
	return profiles.Projects[0]
}

func TestProfilesExplicitProjectIsolation(t *testing.T) {
	profile := configuredProfile(t)
	selected, err := LoadProfile(profile.Provider, profile.Host, profile.Repository)
	if err != nil || selected.PrimaryLanguage != "en" {
		t.Fatal(selected, err)
	}
	if _, err := LoadProfile("github", "github.com", "hiono/taxion"); err == nil {
		t.Fatal("consumer profile inherited by Taxion")
	}
	t.Setenv("TAXION_COMMUNICATION_PROFILES", "")
	if _, err := LoadProfile(profile.Provider, profile.Host, profile.Repository); err == nil {
		t.Fatal("missing profile did not fail closed")
	}
	for _, input := range []string{
		`{"version":2,"projects":[]}`,
		`{"version":1,"projects":[],"unknown":true}`,
		`{"version":1,"projects":[]} {}`,
	} {
		if _, err := DecodeProfiles(strings.NewReader(input)); err == nil {
			t.Fatal("invalid profile registry accepted")
		}
	}
	duplicate, _ := json.Marshal(Profiles{Version: 1, Projects: []Profile{profile, profile}})
	if _, err := DecodeProfiles(strings.NewReader(string(duplicate))); err == nil {
		t.Fatal("ambiguous duplicate project profile accepted")
	}
}

func TestAudienceTitleLanguageAndTerminology(t *testing.T) {
	en := configuredProfile(t)
	ja := en
	ja.PrimaryLanguage, ja.TitleLanguage, ja.BodyLanguage = "ja", "ja", "ja"
	ja.SecondaryLanguages = []string{"en"}
	ja.TechnicalTerms = []string{"Taxion", "CLI", "CI", "Go", "GoReleaser"}
	ja.Glossary = map[string]string{"repair": "修正"}
	for _, test := range []struct {
		name, title, body string
		profile           Profile
		reject            bool
	}{
		{"en", "Preserve setup choices", "Invalid confirmation no longer discards setup choices.", en, false},
		{"ja", "設定内容を保持する", "確認入力を認識できない場合も、設定内容を保持します。", ja, false},
		{"japanese-marks", "ハッシュチェーンを検証する", "個々のユーザーの入力を検証します。", ja, false},
		{"technical", "Explain the controller API", "The controller rejects invalid Dispatch data before storage.", en, false},
		{"product-handoff", "役割間の引継ぎ情報を変更不可にする", "受け渡す契約の改ざんを検出します。", ja, false},
		{"technical-repair", "Document the repair API", "The repair API validates input before changing settings.", en, false},
		{"assignment-design", "Document review configuration", "Reviewer assignment is an optional API field.", en, false},
		{"japanese-report", "初回設定: 最終 yes が未適用になった報告と確認入力の再質問", "", ja, true},
		{"retirement", "停止済みの不要な試行を履歴を保って退役させる", "", ja, true},
		{"internal", "Governance handoff", "operation owner: controller", en, true},
		{"mixed", "設定をrepairする", "設定を修正します。", ja, true},
		{"wrong-title", "Fix setup confirmation", "確認入力を修正します。", ja, true},
		{"wrong-body", "設定を修正する", "Setup choices are now preserved.", ja, true},
		{"mixed-lines", "設定を修正する", "設定内容を保持します。\nSetup choices are now preserved.", ja, true},
		{"mixed-sentence", "設定を修正する", "設定内容を保持します。 Setup choices are now preserved.", ja, true},
		{"mixed-title", "設定を修正する Fix setup confirmation", "設定内容を保持します。", ja, true},
		{"technical-ja", "CLI の設定を保持する", "Taxion は CI の入力を検証します。", ja, false},
		{"overlapping-terms", "GoReleaser の設定を保持する", "Go の設定を検証します。", ja, false},
		{"partial-term", "設定を修正する", "GoReleaserUnknown を検証します。", ja, true},
		{"english-policy", "Fix confirmation", "設定内容を保持します。", en, true},
		{"identifier", "設定を修正する", "`repair()` の呼び出し前に入力を検証します。", ja, false},
		{"newlines", "Fix\nsettings", "Choices are preserved.", en, true},
		{"future", "Preserve results", "The completed result is recorded in a later comment", en, true},
		{"planned", "検証結果を追加する", "完了後に結果を追記します。", ja, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := CheckPresentation(test.profile, test.title, test.body)
			if (err != nil) != test.reject {
				t.Fatal("presentation decision", err)
			}
		})
	}
}

func TestGateAllSurfacesAndRemoteState(t *testing.T) {
	profile := configuredProfile(t)
	at := time.Now().UTC().Add(-time.Minute)
	m := Mutation{Provider: profile.Provider, Host: profile.Host, Repository: profile.Repository,
		Title: "Preserve setup choices", Body: "Invalid confirmation preserves existing choices.",
		Statement: StatementContext{At: at}}
	for _, surface := range []string{"issue", "pull-request", "comment", "review", "release", "milestone", "tag", "ci-summary", "wiki"} {
		m.Surface = surface
		if err := CheckMutation(t.Context(), m, nil); err != nil {
			t.Fatal(surface, err)
		}

	}
	m.Surface = "unknown"
	if err := CheckMutation(t.Context(), m, nil); err == nil {
		t.Fatal("unknown surface accepted")
	}
	m.Surface = "comment"
	m.Body = "private-example-47"
	if err := CheckMutation(t.Context(), m, nil); err == nil {
		t.Fatal("protected identifier accepted")
	}
	target := "https://forge.example/example/application/-/pipelines/42"
	m.Body = "Validation evidence: " + target
	m.Statement.Claims = []ReferenceClaim{{URL: target, State: "COMPLETED"}}
	observed := Reference{URL: target, CreatedAt: at.Add(-time.Hour), StateAt: at.Add(-time.Second), State: "COMPLETED"}
	resolve := func(context.Context, string) (Reference, error) { return observed, nil }
	if err := CheckMutation(t.Context(), m, resolve); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(){
		func() { observed.State = "IN_PROGRESS" },
		func() { observed.StateAt = at.Add(time.Second) },
		func() { observed.CreatedAt = at.Add(time.Second) },
		func() { observed.URL += "1" },
	} {
		saved := observed
		change()
		if err := CheckMutation(t.Context(), m, resolve); err == nil {
			t.Fatal("unverified result accepted")
		}
		observed = saved
	}
	if err := CheckMutation(t.Context(), m, nil); err == nil {
		t.Fatal("reference accepted without resolver")
	}
	m.Statement.Claims = nil
	if err := CheckMutation(t.Context(), m, resolve); err == nil {
		t.Fatal("reference accepted without state claim")
	}
}

func TestCompletionAssertionsRequireTerminalEvidence(t *testing.T) {
	profile := configuredProfile(t)
	at := time.Now().UTC().Add(-time.Minute)
	target := "https://forge.example/example/application/-/pipelines/42"
	mutation := Mutation{Provider: profile.Provider, Host: profile.Host, Repository: profile.Repository,
		Surface: "comment", Statement: StatementContext{At: at}}
	for _, claim := range []string{"CI run completed successfully", "The pipeline has succeeded", "Validation finished successfully", "Tests have passed", "CI is green", "The tests are green", "Checks are passing", "The pipeline was successful"} {
		t.Run(claim, func(t *testing.T) {
			mutation.Body, mutation.Statement.Claims = claim, nil
			if err := CheckMutation(t.Context(), mutation, nil); err == nil {
				t.Fatal("unlinked completion accepted")
			}
			mutation.Body = claim + ": " + target
			mutation.Statement.Claims = []ReferenceClaim{{URL: target, State: "IN_PROGRESS"}}
			pending := func(context.Context, string) (Reference, error) {
				return Reference{URL: target, CreatedAt: at.Add(-time.Hour), StateAt: at.Add(-time.Second), State: "IN_PROGRESS"}, nil
			}
			if err := CheckMutation(t.Context(), mutation, pending); err == nil {
				t.Fatal("pending completion accepted")
			}
			mutation.Statement.Claims[0].State = "COMPLETED"
			completed := func(context.Context, string) (Reference, error) {
				return Reference{URL: target, CreatedAt: at.Add(-time.Hour), StateAt: at.Add(-time.Second), State: "COMPLETED"}, nil
			}
			if err := CheckMutation(t.Context(), mutation, completed); err != nil {
				t.Fatal("verified completion rejected", err)
			}
		})
	}
	mutation.Body, mutation.Statement.Claims = "CI will complete after validation. This job is still pending.", nil
	if err := CheckMutation(t.Context(), mutation, nil); err != nil {
		t.Fatal("planned validation rejected", err)
	}
}
