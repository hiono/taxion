package config

import (
	"encoding/json"
	"strings"
	"testing"
)

const validConfig = `version: 1
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
`

func TestLoadPreservesIndependentIdentities(t *testing.T) {
	for _, provider := range []string{"github", "gitlab", "bitbucket"} {
		c, err := Load([]byte(strings.Replace(validConfig, "provider: gitlab", "provider: "+provider, 1)))
		if err != nil {
			t.Fatal(err)
		}
		if c.Repository.Account == c.Runtime.Account || c.Runtime.ContractRef != "company-contract" ||
			c.Execution.ApprovalRef != "human-decision-1" {
			t.Fatal("configuration collapsed independent identity or approval references")
		}
	}
}

func TestLoadRejectsMalformedAndIncompleteContracts(t *testing.T) {
	for name, input := range map[string]string{
		"empty":            "",
		"new version":      strings.Replace(validConfig, "version: 1", "version: 2", 1),
		"string version":   strings.Replace(validConfig, "version: 1", `version: "1"`, 1),
		"coerced account":  strings.Replace(validConfig, "account: developer-b", "account: 123", 1),
		"null account":     strings.Replace(validConfig, "account: developer-b", "account: null", 1),
		"missing approval": strings.Replace(validConfig, "  approvalRef: human-decision-1\n", "", 1),
		"duplicate key":    validConfig + "version: 1\n",
		"unknown secret":   validConfig + "token: secret-value\n",
		"extra document":   validConfig + "---\nversion: 1\n",
		"invalid YAML":     "project: [",
		"host credentials": strings.Replace(validConfig, "gitlab.example.com", "user:secret@gitlab.example.com", 1),
		"host path":        strings.Replace(validConfig, "gitlab.example.com", "gitlab.example.com/path", 1),
		"path traversal":   strings.Replace(validConfig, "group/subgroup/application", "group/../application", 1),
		"alias":            strings.Replace(validConfig, "id: project-a", "id: &id project-a", 1) + "other: *id\n",
		"sequence":         strings.Replace(validConfig, "account: developer-b", "account: [developer-b]", 1),
		"runtime":          strings.Replace(validConfig, "provider: copilot", "provider: unknown", 1),
		"provider":         strings.Replace(validConfig, "provider: gitlab", "provider: unknown", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load([]byte(input)); err == nil {
				t.Fatal("invalid configuration accepted")
			} else if strings.Contains(err.Error(), "secret") {
				t.Fatal("error exposed input credentials")
			}
		})
	}
}

const endpointConfig = `version: 1
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
  apiBase: https://gitlab.example.com/git/api/v4
  webBase: https://gitlab.example.com/git
  httpsCloneBase: https://gitlab.example.com/git
  sshHost: gitlab.example.com
  sshPort: "2222"
  projectId: "191"
`

func endpointWith(field, value string) []byte {
	var lines []string
	for _, line := range strings.Split(endpointConfig, "\n") {
		if strings.HasPrefix(line, "  "+field+":") {
			if value == "" {
				continue
			}
			line = "  " + field + ": " + value
		}
		lines = append(lines, line)
	}
	return []byte(strings.Join(lines, "\n"))
}

func TestEndpointsRequireTheCompleteGroup(t *testing.T) {
	loaded, err := Load([]byte(endpointConfig))
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.HasEndpoints() || loaded.Endpoints.SSHHost != "gitlab.example.com" ||
		loaded.Endpoints.ProjectID != "191" {
		t.Fatal("explicit endpoints were not preserved as separate identities")
	}
	for _, field := range []string{"apiBase", "webBase", "httpsCloneBase", "sshHost", "sshPort", "projectId"} {
		if _, err := Load(endpointWith(field, "")); err == nil {
			t.Fatal("partial endpoint group accepted for " + field)
		} else if !strings.Contains(err.Error(), "REP-002") {
			t.Fatal("endpoint failure lacks its diagnostic code: " + err.Error())
		}
	}
}

func TestEndpointsEnforceTheHostnameAndPortContract(t *testing.T) {
	for name, change := range map[string][2]string{
		"hostname mismatch":      {"apiBase", "https://other.example.com/git/api/v4"},
		"scheme":                 {"apiBase", "http://gitlab.example.com/git/api/v4"},
		"port mismatch":          {"apiBase", "https://gitlab.example.com:8443/git/api/v4"},
		"credentials":            {"apiBase", "https://user:secret@gitlab.example.com/git/api/v4"},
		"query":                  {"apiBase", "https://gitlab.example.com/git/api/v4?per_page=1"},
		"fragment":               {"apiBase", "https://gitlab.example.com/git/api/v4#top"},
		"percent encoding":       {"apiBase", "https://gitlab.example.com/git%2Fother/api/v4"},
		"non canonical segment":  {"apiBase", "https://gitlab.example.com/git/../api/v4"},
		"non API path":           {"apiBase", "https://gitlab.example.com/git/rest"},
		"web base host":          {"webBase", "https://other.example.com/git"},
		"clone prefix port":      {"httpsCloneBase", "https://gitlab.example.com:8443/git"},
		"ssh host with port":     {"sshHost", "gitlab.example.com:2222"},
		"ssh port out of range":  {"sshPort", "\"70000\""},
		"project id non decimal": {"projectId", "project-a"},
		"project id zero":        {"projectId", "\"0\""},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(endpointWith(change[0], change[1])); err == nil {
				t.Fatal("invalid endpoints accepted")
			} else if !strings.Contains(err.Error(), "REP-002") {
				t.Fatal("endpoint failure lacks its diagnostic code: " + err.Error())
			}
		})
	}
}

func TestEndpointsAcceptAZeroPaddedPortAndKeepTheLegacyJSONShape(t *testing.T) {
	loaded, err := Load([]byte(endpointConfig))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"endpoints"`) {
		t.Fatal("explicit endpoints are missing from the selection")
	}
	legacy, err := Load([]byte(validConfig))
	if err != nil {
		t.Fatal(err)
	}
	legacyJSON, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(legacyJSON), "endpoints") {
		t.Fatal("legacy selection gained an endpoints block")
	}
}

func TestEndpointsMergeAcrossLayers(t *testing.T) {
	base := []byte(`version: 1
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
`)
	lower := append(append([]byte{}, base...), []byte("endpoints:\n  apiBase: https://gitlab.example.com/git/api/v4\n  webBase: https://gitlab.example.com/git\n  httpsCloneBase: https://gitlab.example.com/git\n")...)
	upper := []byte("version: 1\nendpoints:\n  sshHost: gitlab.example.com\n  sshPort: \"2222\"\n  projectId: \"191\"\n")
	resolved, err := Resolve([]Layer{{Source: "project", Input: lower}, {Source: "user", Input: upper}})
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.Config.HasEndpoints() || resolved.Config.Endpoints.SSHPort != "2222" ||
		resolved.Sources["endpoints.apiBase"] != "project" || resolved.Sources["endpoints.sshPort"] != "user" {
		t.Fatal("endpoints did not merge field by field with recorded origins")
	}
}
