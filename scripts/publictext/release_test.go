package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/publictext"
)

func TestReleasePresentationUsesExactProfileAndStaticSource(t *testing.T) {
	profile, err := os.ReadFile("../../.github/communication-profiles.json")
	if err != nil {
		t.Fatal(err)
	}
	var profiles publictext.Profiles
	if err := json.Unmarshal(profile, &profiles); err != nil {
		t.Fatal(err)
	}
	profiles.Projects[0].RequireSemanticReview = false
	profile, err = json.Marshal(profiles)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	t.Chdir(directory)
	if err := os.WriteFile("profiles.json", profile, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TAXION_COMMUNICATION_PROFILES", filepath.Join(directory, "profiles.json"))
	sum := sha256.Sum256([]byte("reserved-private-fixture"))
	t.Setenv("TAXION_PUBLIC_PRIVATE_IDENTIFIER_SHA256", hex.EncodeToString(sum[:]))
	if err := os.WriteFile("VERSION", []byte("1.2.3\n"), 0600); err != nil {
		t.Fatal(err)
	}
	header := "version: 2\nrelease:\n  header: |\n    ## Fixed\n\n    設定内容を保持します。\nchangelog:\n  disable: true\n"
	if err := os.WriteFile(".goreleaser.yaml", []byte(header), 0600); err != nil {
		t.Fatal(err)
	}
	var output, warnings bytes.Buffer
	if err := run([]string{"-release", "v1.2.3"}, &output, &warnings); err != nil ||
		output.String() != "## Fixed\n\n設定内容を保持します。\n" {
		t.Fatal("release notes changed or unavailable", output.String(), err)
	}
	if err := os.WriteFile("VERSION", []byte("1.2.4-rc.1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if mutation, err := releaseSource("v1.2.4-rc.1", false); err != nil || mutation.Title != "Taxion 1.2.4-rc.1" {
		t.Fatal("RC release text identity rejected", mutation, err)
	}
	for _, tag := range []string{"v1.2.4-rc.0", "v1.2.4-rc.01", "v1.2.4-beta.1", "v1.2.4-rc.1+local"} {
		if _, err := releaseSource(tag, false); err == nil {
			t.Fatal("unsupported prerelease identity accepted", tag)
		}
	}
	if err := os.WriteFile("VERSION", []byte("1.2.3\n"), 0600); err != nil {
		t.Fatal(err)
	}
	profiles.Projects[0].RequireSemanticReview = true
	profile, err = json.Marshal(profiles)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("profiles.json", profile, 0600); err != nil {
		t.Fatal(err)
	}
	basis, err := releaseSource("v1.2.3", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(".github", 0700); err != nil {
		t.Fatal(err)
	}
	review := publictext.SemanticReview{ProfileHash: publictext.ProfileHash(profiles.Projects[0]), ContentHash: publictext.SemanticHash(basis), WriterSession: "fixture-writer", ReviewerSession: "fixture-reviewer", Decision: "APPROVE", Source: "fixture:review", Statement: &basis.Statement}
	writeReview := func(review publictext.SemanticReview) {
		t.Helper()
		data, err := json.Marshal(review)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(".github/release-semantic-review.json", data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeReview(review)
	first, err := releasePresentation("v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	second, err := releasePresentation("v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if !first.Statement.At.Equal(basis.Statement.At) || publictext.SemanticHash(first) != publictext.SemanticHash(second) {
		t.Fatal("release rendering changed reviewed statement context")
	}
	output.Reset()
	if err := run([]string{"-release", "v1.2.3"}, &output, &warnings); err != nil {
		t.Fatal("exact release review rejected", err)
	}
	for _, change := range []func(*publictext.StatementContext){
		func(s *publictext.StatementContext) { s.At = s.At.Add(-time.Second) },
		func(s *publictext.StatementContext) {
			s.Claims = []publictext.ReferenceClaim{{URL: "https://github.com/hiono/taxion/issues/1", State: "EXISTS"}}
		},
	} {
		changed := review
		statement := basis.Statement
		changed.Statement = &statement
		change(changed.Statement)
		writeReview(changed)
		output.Reset()
		if run([]string{"-release", "v1.2.3"}, &output, &warnings) == nil || output.Len() != 0 {
			t.Fatal("changed statement reused release approval")
		}
	}
	profiles.Projects[0].RequireSemanticReview = false
	profile, err = json.Marshal(profiles)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("profiles.json", profile, 0600); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{
		strings.Replace(header, "disable: true", "disable: false", 1),
		strings.Replace(header, "設定内容を保持します。", "{{ .FutureResult }}", 1),
		strings.Replace(header, "設定内容を保持します。", "完了した結果は後続コメントに記載している", 1),
		strings.Replace(header, "設定内容を保持します。", "設定内容を保持します。 Setup choices are preserved.", 1),
		header + "unknown: [",
	} {
		if err := os.WriteFile(".goreleaser.yaml", []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		output.Reset()
		if run([]string{"-release", "v1.2.3"}, &output, &warnings) == nil || output.Len() != 0 {
			t.Fatal("unchecked release notes emitted")
		}
	}
}
