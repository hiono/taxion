package copilot

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	executionconfig "github.com/hiono/taxion/internal/config"
)

func TestExplicitCredentialRemainsOutsideDurableBinding(t *testing.T) {
	selected, err := executionconfig.Load([]byte(`version: 1
project: {id: demo}
repository: {provider: github, host: github.com, path: owner/repo, account: scm-user, credentialRef: scm}
runtime: {provider: copilot, account: ai-user, credentialRef: env:TAXION_TEST_BINDING_TOKEN, contractRef: contract}
execution: {requester: requester, worker: local, approvalRef: approval}
`))
	if err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(t)
	cfg.Configuration, err = json.Marshal(selected)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TAXION_TEST_BINDING_TOKEN", "private-fixture-one")
	first, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	t.Setenv("TAXION_TEST_BINDING_TOKEN", "private-fixture-two")
	second, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if first.ConfigHash() != second.ConfigHash() ||
		strings.Contains(string(first.ExecutionConfiguration()), "private-fixture") {
		t.Fatal("secret rotation changed durable identity or stored token values")
	}
	t.Setenv("TAXION_TEST_BINDING_TOKEN", "")
	missing, err := New(cfg)
	if err != nil {
		t.Fatal("offline construction blocked safe-stop identity", err)
	}
	defer missing.Close()
	if missing.ConfigHash() != first.ConfigHash() {
		t.Fatal("credential loss changed safe-stop identity")
	}
	if err := missing.Resume(context.Background(), "saved-session"); err == nil ||
		!strings.Contains(err.Error(), "selected runtime credential") || missing.started {
		t.Fatal("missing credential launched a client or permitted saved-session attachment", err)
	}
	if err := missing.CheckAuthentication(context.Background(), "ai-user"); err == nil || missing.started {
		t.Fatal("missing selected credential fell back to another account", err)
	}
	selected.Runtime.CredentialRef = "copilot:logged-in"
	cfg.Configuration, err = json.Marshal(selected)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer stored.Close()
	if stored.ConfigHash() == first.ConfigHash() {
		t.Fatal("changing credential selection policy retained same session identity")
	}
}
