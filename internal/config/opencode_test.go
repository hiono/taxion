package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOpenCodeSettingsAreExactApprovedTupleNotCredentialReuse(t *testing.T) {
	var candidate Config
	if err := json.Unmarshal([]byte(`{
		"version":1,"project":{"id":"taxion"},
		"repository":{"provider":"github","host":"github.com","path":"hiono/taxion","account":"operator","credentialRef":"repo-ref"},
		"runtime":{"provider":"opencode","account":"opencode-managed","credentialRef":"opencode-managed","contractRef":"operator-confirmed",
			"acpCommand":"/opt/opencode","acpCommandHash":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"acpConfig":"/opt/server.json","acpConfigHash":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
		"execution":{"requester":"human","worker":"taxion","approvalRef":"approved"}
	}`), &candidate); err != nil {
		t.Fatal(err)
	}
	if err := candidate.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Config){
		func(c *Config) { c.Runtime.Account = "github-account" },
		func(c *Config) { c.Runtime.CredentialRef = "github-token" },
		func(c *Config) { c.Runtime.ACPCommand = "opencode" },
		func(c *Config) { c.Runtime.ACPConfigHash = "sha256:invalid" },
		func(c *Config) { c.Runtime.Provider = "copilot" },
	} {
		invalid := candidate
		change(&invalid)
		if err := invalid.Validate(); err == nil {
			t.Fatal("unsafe/incomplete runtime selection accepted")
		}
	}
	approval, err := json.Marshal(Approval{Kind: ApprovalKind, TaskID: "task", Reason: "approved isolated ACP",
		Allowed: []Config{candidate}})
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckApproval(string(approval), "task", "approved", candidate); err != nil {
		t.Fatal(err)
	}
	changed := candidate
	changed.Runtime.ACPConfigHash = "sha256:" + strings.Repeat("c", 64)
	if err := CheckApproval(string(approval), "task", "approved", changed); err == nil {
		t.Fatal("server configuration widened without new authorization")
	}
}
