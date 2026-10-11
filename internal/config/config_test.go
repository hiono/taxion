package config

import (
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
