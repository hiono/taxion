package controller

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/runtime/copilot"
)

func TestSelectedExecutionChangesRequireNewDispatchForEveryOperation(t *testing.T) {
	for _, field := range []string{"repository-account", "runtime-account", "contract", "host", "repository", "repository-credential", "runtime-credential", "requester", "project"} {
		t.Run(field, func(t *testing.T) {
			db, id, worker, resolved := executionConfigurationFixture(t)
			control, err := New(db, worker)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := control.Start(context.Background(), id); err != nil {
				t.Fatal(err)
			}
			before, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			changed := resolved.Config
			switch field {
			case "repository-account":
				changed.Repository.Account = "other-scm-user"
			case "runtime-account":
				changed.Runtime.Account = "other-ai-user"
			case "contract":
				changed.Runtime.ContractRef = "other-contract"
			case "host":
				changed.Repository.Host = "other.example"
			case "repository":
				changed.Repository.Path = "owner/other"
			case "repository-credential":
				changed.Repository.CredentialRef = "env:OTHER_SCM_TOKEN"
			case "runtime-credential":
				changed.Runtime.CredentialRef = "env:OTHER_AI_TOKEN"
			case "requester":
				changed.Execution.Requester = "other-requester"
			case "project":
				changed.Project.ID = "other-project"
			}
			worker.selection, err = json.Marshal(changed)
			if err != nil {
				t.Fatal(err)
			}
			authCalls, accessCalls, resumes := len(worker.accounts), worker.repositoryChecks, worker.resumes
			for _, operation := range []func() error{
				func() error { _, err := control.Start(context.Background(), id); return err },
				func() error { return control.Resume(context.Background(), id) },
				func() error { return control.Send(context.Background(), id, "changed selection") },
				func() error {
					return BindConfiguration(db, id, config.Resolution{Config: changed, Sources: resolved.Sources})
				},
			} {
				if err := operation(); err == nil {
					t.Fatal("changed tuple reused issued authorization", field)
				}
			}
			after, err := db.LoadWorkspace()
			if err != nil || !reflect.DeepEqual(before, after) || len(worker.accounts) != authCalls ||
				worker.repositoryChecks != accessCalls || worker.resumes != resumes || len(worker.sends) != 0 {
				t.Fatal("changed tuple reached provider operations or changed durable state", field, err)
			}
		})
	}
}

func TestCredentialValueRenewalRequiresFreshIdentityChecksWithoutNewTuple(t *testing.T) {
	db, id, worker, _ := executionConfigurationCredentialFixture(t, "env:TAXION_TEST_RENEW_TOKEN")
	cfg := copilot.Config{
		Role: worker.Role(), SystemPrompt: "renewal boundary",
		WorkingDirectory: worker.Worktree(), CLIPath: "/bin/false",
		BaseDirectory: t.TempDir(), Configuration: worker.selection,
	}
	t.Setenv("TAXION_TEST_RENEW_TOKEN", "unissued-renewal-fixture-one")
	first, err := copilot.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	worker.hash = first.ConfigHash()
	control, err := New(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Start(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TAXION_TEST_RENEW_TOKEN", "unissued-renewal-fixture-two")
	second, err := copilot.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if second.ConfigHash() != first.ConfigHash() {
		t.Fatal("credential value renewal changed selected identity")
	}
	renewed := &configuredRuntime{
		fakeRuntime: worker.fakeRuntime, selection: second.ExecutionConfiguration(),
	}
	control, err = New(db, renewed)
	if err != nil {
		t.Fatal(err)
	}
	if err := control.Resume(context.Background(), id); err != nil {
		t.Fatal("same selected tuple could not resume after credential renewal", err)
	}
	if !reflect.DeepEqual(renewed.accounts, []string{"ai-user"}) || renewed.repositoryChecks != 1 {
		t.Fatal("renewal bypassed fresh provider identity checks")
	}
}
