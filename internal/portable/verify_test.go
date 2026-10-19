package portable

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/hiono/taxion/internal/config"
)

func endpointConfig(t *testing.T, apiBase, sshPort, projectID string) config.Config {
	t.Helper()
	loaded, err := config.Load([]byte(`version: 1
project:
  id: project-a
repository:
  provider: gitlab
  host: gitlab.example.com
  path: group/subgroup/application
  account: developer-a
  credentialRef: scm-credential
runtime:
  provider: copilot
  account: developer-b
  credentialRef: ai-credential
  contractRef: company-contract
execution:
  requester: developer-a
  worker: local-worker
  approvalRef: human-decision-1
endpoints:
  apiBase: ` + apiBase + `
  webBase: https://gitlab.example.com/git
  httpsCloneBase: https://gitlab.example.com/git
  sshHost: gitlab.example.com
  sshPort: "` + sshPort + `"
  projectId: "` + projectID + `"
`))
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

func legacySnapshotConfig(t *testing.T) config.Config {
	t.Helper()
	loaded, err := config.Load([]byte(`version: 1
project:
  id: project-a
repository:
  provider: gitlab
  host: gitlab.example.com
  path: group/subgroup/application
  account: developer-a
  credentialRef: scm-credential
runtime:
  provider: copilot
  account: developer-b
  credentialRef: ai-credential
  contractRef: company-contract
execution:
  requester: developer-a
  worker: local-worker
  approvalRef: human-decision-1
`))
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

// archiveWithEndpoints rewrites an empty archive into a version 2 archive whose
// repository records explicit endpoint values.
func archiveWithEndpoints(t *testing.T, endpoints *config.Endpoints, origin, version string) string {
	t.Helper()
	_, archive, _ := emptySnapshot(t)
	db, err := openSQL(archive, false)
	if err != nil {
		t.Fatal(err)
	}
	var content string
	if err := db.QueryRow("SELECT content FROM manifest").Scan(&content); err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal([]byte(content), &m); err != nil {
		t.Fatal(err)
	}
	switch version {
	case "2":
		m.Version = 2
	default:
		m.Version = 1
	}
	m.Repositories = []repository{{
		ID: "group/subgroup/application", Origin: origin, Pack: m.Workspace,
		Refs: map[string]string{}, Endpoints: endpoints,
	}}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE manifest SET content=?", string(data)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return archive
}

func TestVerifySnapshotAcceptsMatchingEffectiveEndpoints(t *testing.T) {
	current := endpointConfig(t, "https://gitlab.example.com/git/api/v4", "2222", "191")
	declared := current.Endpoints
	archive := archiveWithEndpoints(t, &declared, "https://gitlab.example.com/git/group/subgroup/application.git", "2")
	before, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	report, err := VerifySnapshot(t.Context(), archive, current)
	if err != nil {
		t.Fatal(err)
	}
	if report.Version != 2 || report.EndpointRepositories != 1 || report.LegacyRepositories != 0 {
		t.Fatal("snapshot revalidation did not report the saved contract", report)
	}
	after, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("revalidation rewrote the archive")
	}
}

func TestVerifySnapshotFailsClosedOnConfigurationDrift(t *testing.T) {
	saved := endpointConfig(t, "https://gitlab.example.com/git/api/v4", "2222", "191")
	declared := saved.Endpoints
	archive := archiveWithEndpoints(t, &declared, "https://gitlab.example.com/git/group/subgroup/application.git", "2")
	otherPath := saved
	otherPath.Repository.Path = "group/subgroup/other"
	for name, current := range map[string]config.Config{
		"legacy configuration":   legacySnapshotConfig(t),
		"other API base":         endpointConfig(t, "https://gitlab.example.com/git/api/v4/", "2222", "191"),
		"other SSH port":         endpointConfig(t, "https://gitlab.example.com/git/api/v4", "2223", "191"),
		"other project identity": endpointConfig(t, "https://gitlab.example.com/git/api/v4", "2222", "192"),
		"other repository path":  otherPath,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := VerifySnapshot(t.Context(), archive, current); err == nil {
				t.Fatal("configuration drift accepted")
			}
		})
	}
	unchanged, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if report, err := VerifySnapshot(t.Context(), archive, saved); err != nil || report.Version != 2 {
		t.Fatal("matching configuration after drift was rejected", err)
	}
	after, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if string(unchanged) != string(after) {
		t.Fatal("drift detection rewrote the archive")
	}
}

func TestReadArchiveEnforcesTheSnapshotVersionContract(t *testing.T) {
	declared := endpointConfig(t, "https://gitlab.example.com/git/api/v4", "2222", "191").Endpoints
	for name, archive := range map[string]string{
		"legacy snapshot with endpoints": archiveWithEndpoints(t, &declared,
			"https://gitlab.example.com/git/group/subgroup/application.git", "1"),
		"endpoint snapshot without values": archiveWithEndpoints(t, nil,
			"https://gitlab.example.com/git/group/subgroup/application.git", "2"),
		"endpoint snapshot with a legacy origin": archiveWithEndpoints(t, &declared,
			"https://gitlab.example.com/group/subgroup/application.git", "2"),
		"endpoint snapshot with another project": archiveWithEndpoints(t, &declared,
			"https://gitlab.example.com/git/group/subgroup/other.git", "2"),
	} {
		t.Run(name, func(t *testing.T) {
			tmp := t.TempDir()
			if _, _, _, err := readArchive(archive, tmp); err == nil {
				t.Fatal("invalid snapshot version contract accepted")
			}
		})
	}
}
