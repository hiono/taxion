package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hiono/taxion/internal/publictext"
)

func TestPrivateAuditOutputAndFailClosedUnconfiguredProject(t *testing.T) {
	inside, err := filepath.Abs("private-audit.json")
	if err != nil {
		t.Fatal(err)
	}
	if privateAuditOutput(inside) == nil {
		t.Fatal("private report allowed in public repository")
	}
	outside := filepath.Join(t.TempDir(), "audit.json")
	if err := privateAuditOutput(outside); err != nil {
		t.Fatal(err)
	}
	request := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(request, []byte(`{"provider":"gitlab","host":"forge.example","repository":"example/unconfigured","output":"unused"}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TAXION_COMMUNICATION_PROFILES", "")
	for _, mode := range []string{"-audit-gitlab", "-audit-file", "-audit-review-basis", "-guidance"} {
		var output bytes.Buffer
		if err := run([]string{mode, request}, &output, &output); err == nil || output.Len() != 0 {
			t.Fatal("unconfigured profile emitted content", mode, err)
		}
	}
}

func TestCanonicalAuditBasisCanApproveOnlyUnchangedPrivateSnapshot(t *testing.T) {
	directory := t.TempDir()
	data, err := os.ReadFile("../../internal/publictext/testdata/consumer-profiles.json")
	if err != nil {
		t.Fatal(err)
	}
	var profiles publictext.Profiles
	if err := json.Unmarshal(data, &profiles); err != nil {
		t.Fatal(err)
	}
	profiles.Projects[0].Configuration = "production"
	profileFile := filepath.Join(directory, "profiles.json")
	write := func(path string, value any) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(profileFile, profiles)
	t.Setenv("TAXION_COMMUNICATION_PROFILES", profileFile)
	t.Setenv("TAXION_PUBLIC_PRIVATE_IDENTIFIER_SHA256", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	records := []publictext.AuditRecord{{ID: "issues/1", Surface: "issue", Title: "Preserve setup choices", Body: "Invalid confirmation preserves existing choices."}}
	recordFile := filepath.Join(directory, "records.json")
	write(recordFile, records)
	requestFile := filepath.Join(directory, "request.json")
	basisFile := filepath.Join(directory, "basis.json")
	request := map[string]string{"provider": "gitlab", "host": "forge.example", "repository": "example/application", "recordsFile": recordFile, "output": basisFile}
	write(requestFile, request)
	var output bytes.Buffer
	if err := run([]string{"-audit-review-basis", requestFile}, &output, &output); err != nil || output.Len() != 0 {
		t.Fatal("basis leaked or unavailable", err)
	}
	var basis struct {
		Profile publictext.Profile      `json:"profile"`
		Bases   []publictext.AuditBasis `json:"bases"`
	}
	data, err = os.ReadFile(basisFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &basis); err != nil {
		t.Fatal(err)
	}
	if len(basis.Bases) != 1 || basis.Bases[0].Record.Body != records[0].Body {
		t.Fatal("wrong private review basis")
	}
	records[0].Review = &publictext.SemanticReview{ContentHash: basis.Bases[0].ContentHash, ProfileHash: basis.Bases[0].ProfileHash, WriterSession: "fixture-export", ReviewerSession: "independent-fixture-review", Decision: "APPROVE", Source: "private:review"}
	for i := 0; i < 3; i++ {
		if i == 1 {
			records[0].Body += " Changed."
		}
		if i == 2 {
			records[0].Body = basis.Bases[0].Record.Body
			profiles.Projects[0].TitleStyle = "Changed style"
			write(profileFile, profiles)
		}
		write(recordFile, records)
		reportFile := filepath.Join(directory, []string{"unchanged.json", "text-changed.json", "profile-changed.json"}[i])
		request["output"] = reportFile
		write(requestFile, request)
		if err := run([]string{"-audit-file", requestFile}, &output, &output); err != nil || output.Len() != 0 {
			t.Fatal("private report failed or leaked", err)
		}
		data, err := os.ReadFile(reportFile)
		if err != nil {
			t.Fatal(err)
		}
		var report struct {
			Results []publictext.AuditResult `json:"results"`
		}
		if err := json.Unmarshal(data, &report); err != nil {
			t.Fatal(err)
		}
		if len(report.Results) != 1 {
			t.Fatal(report)
		}
		approved := len(report.Results[0].Classifications) == 1 && report.Results[0].Classifications[0] == "COMPLIANT"
		if approved != (i == 0) {
			t.Fatal("canonical review did not bind exact text and profile", report)
		}
	}
}
