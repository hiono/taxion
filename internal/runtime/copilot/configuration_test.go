package copilot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	executionconfig "github.com/hiono/taxion/internal/config"
)

func TestBoundResultProtocolIsPartOfSessionIdentity(t *testing.T) {
	config := testConfig(t)
	legacy, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	config.ResponseSchema = map[string]any{"$id": "urn:taxion:dispatch-result:bound:v1", "type": "object"}
	bound, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer bound.Close()
	if legacy.UsesBoundResultIdentity() || !bound.UsesBoundResultIdentity() || legacy.ConfigHash() == bound.ConfigHash() {
		t.Fatal("bound output protocol is not isolated from legacy sessions")
	}
}

func TestConfigurationSelectionHashIsCompleteOwnedAndCanonical(t *testing.T) {
	cfg := testConfig(t)
	legacy, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	data, err := json.Marshal(struct {
		Role, SystemPrompt, WorkingDirectory, CLIPath, BaseDirectory string
		AvailableTools                                               []string
		ResponseSchema                                               map[string]any
		PermissionPolicy                                             string
		DiscoveryPolicy                                              string
	}{
		legacy.config.Role, legacy.config.SystemPrompt, legacy.config.WorkingDirectory,
		legacy.config.CLIPath, legacy.config.BaseDirectory, legacy.config.AvailableTools,
		legacy.config.ResponseSchema, "coder-worktree-write-v1", "native-cli-defaults-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if legacy.ConfigHash() != hex.EncodeToString(sum[:]) {
		t.Fatal("native discovery is not bound to the session hash")
	}
	selected, err := executionconfig.Load([]byte(`version: 1
project: {id: demo}
repository: {provider: github, host: github.com, path: owner/repo, account: scm-user, credentialRef: scm}
runtime: {provider: copilot, account: ai-user, credentialRef: ai, contractRef: contract}
execution: {requester: requester, worker: local, approvalRef: approval}
`))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Configuration, err = json.Marshal(selected)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	hash := adapter.ConfigHash()
	if hash == legacy.ConfigHash() {
		t.Fatal("selected configuration absent from session hash")
	}
	cfg.Configuration[0] = '!'
	returned := adapter.ExecutionConfiguration()
	returned[0] = '!'
	if adapter.ConfigHash() != hash || adapter.ExecutionConfiguration()[0] != '{' {
		t.Fatal("caller changed owned selection")
	}
	for _, change := range []string{"format", "project", "repository", "actor", "credential", "contract", "requester", "worker", "approval"} {
		t.Run(change, func(t *testing.T) {
			other := selected
			switch change {
			case "project":
				other.Project.ID = "different"
			case "repository":
				other.Repository.Path = "owner/other"
			case "actor":
				other.Runtime.Account = "different"
			case "credential":
				other.Runtime.CredentialRef = "different"
			case "contract":
				other.Runtime.ContractRef = "different"
			case "requester":
				other.Execution.Requester = "different"
			case "worker":
				other.Execution.Worker = "different"
			case "approval":
				other.Execution.ApprovalRef = "different"
			}
			cfg.Configuration, err = json.MarshalIndent(other, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			changed, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer changed.Close()
			if (changed.ConfigHash() == hash) != (change == "format") {
				t.Fatal("session hash does not include canonical complete configuration", change)
			}
		})
	}
	for _, bad := range []string{`null`, `{}`, `{"version":1,"unknown":"private-detail"}`, string(adapter.ExecutionConfiguration()) + `{}`} {
		cfg.Configuration = json.RawMessage(bad)
		if _, err := New(cfg); err == nil {
			t.Fatal("invalid selected configuration accepted")
		}
	}
}
