package core

import "testing"

func TestEvaluateJobEffectUsesJobResourceBoundary(t *testing.T) {
	contract := *validJobContract(t)
	contract.Repositories[0].FileAccess = FileAccessPolicy{
		Level: FileAccessExact, Paths: []string{"src/main.go", "docs/guide.md"},
	}
	base := JobEffectObservation{
		Effects:     []JobEffect{JobEffectRepositoryRead},
		Targets:     []JobEffectTarget{{Repository: "example/repo", Path: "src/main.go"}},
		Inspectable: true, SandboxEnforced: true,
	}
	if got := contract.EvaluateJobEffect(base); got != JobEffectAllow {
		t.Fatalf("in-bound resource read decision = %q, want allow", got)
	}
	outside := base
	outside.Targets = []JobEffectTarget{{Repository: "example/repo", Path: "go.mod"}}
	if got := contract.EvaluateJobEffect(outside); got != JobEffectDeny {
		t.Fatalf("out-of-bound resource read decision = %q, want deny", got)
	}
	outside = base
	outside.Targets = []JobEffectTarget{{Repository: "other/repo", Path: "src/main.go"}}
	if got := contract.EvaluateJobEffect(outside); got != JobEffectDeny {
		t.Fatalf("unapproved repository decision = %q, want deny", got)
	}
	outside = base
	outside.Targets = []JobEffectTarget{{Repository: "example/repo", Path: "../secret"}}
	if got := contract.EvaluateJobEffect(outside); got != JobEffectDeny {
		t.Fatalf("path traversal decision = %q, want deny", got)
	}
	outside = base
	outside.Targets = []JobEffectTarget{{Repository: "example/repo", Path: ".env"}}
	if got := contract.EvaluateJobEffect(outside); got != JobEffectDeny {
		t.Fatalf("secret artifact decision = %q, want deny", got)
	}
}

func TestEvaluateJobEffectRequiresTrackedFilesForCommittedScope(t *testing.T) {
	contract := *validJobContract(t)
	contract.Repositories[0].FileAccess = FileAccessPolicy{Level: FileAccessCommitted}
	observation := JobEffectObservation{
		Effects:     []JobEffect{JobEffectRepositoryWrite},
		Targets:     []JobEffectTarget{{Repository: "example/repo", Path: "src/main.go"}},
		Inspectable: true, SandboxEnforced: true,
	}
	if got := contract.EvaluateJobEffect(observation); got != JobEffectDeny {
		t.Fatalf("untracked resource decision = %q, want deny", got)
	}
	observation.Targets[0].Tracked = true
	if got := contract.EvaluateJobEffect(observation); got != JobEffectAllow {
		t.Fatalf("tracked resource decision = %q, want allow", got)
	}
}

func TestEvaluateJobEffectRequiresExactSandboxedValidationCommand(t *testing.T) {
	contract := *validJobContract(t)
	contract.ValidationCommands = []string{"go test ./...", "go vet ./..."}
	observation := JobEffectObservation{
		Effects: []JobEffect{JobEffectValidationExecution},
		Command: "go test ./...", Inspectable: true, SandboxEnforced: true,
	}
	if got := contract.EvaluateJobEffect(observation); got != JobEffectAllow {
		t.Fatalf("exact validation command decision = %q, want allow", got)
	}
	observation.Command = "go test ./... -race"
	if got := contract.EvaluateJobEffect(observation); got != JobEffectDeny {
		t.Fatalf("unapproved validation command decision = %q, want deny", got)
	}
	observation.Command = "go test ./..."
	observation.SandboxBypass = true
	if got := contract.EvaluateJobEffect(observation); got != JobEffectDeny {
		t.Fatalf("sandbox bypass decision = %q, want deny", got)
	}
}

func TestEvaluateJobEffectRequiresApprovalForConfiguredEffects(t *testing.T) {
	contract := *validJobContract(t)
	observation := JobEffectObservation{
		Effects: []JobEffect{JobEffectNetworkAccess}, Inspectable: true,
	}
	if got := contract.EvaluateJobEffect(observation); got != JobEffectRequireApproval {
		t.Fatalf("network effect decision = %q, want require_approval", got)
	}
	contract.ApprovalRequiredEffects = append(contract.ApprovalRequiredEffects,
		JobEffectRule{Effect: JobEffectRepositoryWrite, Targets: []string{"example/repo:src/main.go"}})
	observation = JobEffectObservation{
		Effects:     []JobEffect{JobEffectRepositoryWrite},
		Targets:     []JobEffectTarget{{Repository: "example/repo", Path: "src/main.go"}},
		Inspectable: true, SandboxEnforced: true,
	}
	if got := contract.EvaluateJobEffect(observation); got != JobEffectRequireApproval {
		t.Fatalf("targeted write decision = %q, want require_approval", got)
	}
	contract.Repositories[0].FileAccess = FileAccessPolicy{Level: FileAccessExact, Paths: []string{"docs/guide.md"}}
	if got := contract.EvaluateJobEffect(observation); got != JobEffectDeny {
		t.Fatalf("out-of-bound target must be denied before approval routing, got %q", got)
	}
}

func TestEvaluateJobEffectFailsClosedForIncompleteOrUnknownEffects(t *testing.T) {
	contract := *validJobContract(t)
	for _, observation := range []JobEffectObservation{
		{Effects: []JobEffect{JobEffectRepositoryRead}, Inspectable: false, SandboxEnforced: true},
		{Effects: []JobEffect{JobEffect("future-effect")}, Inspectable: true, SandboxEnforced: true},
		{Effects: []JobEffect{JobEffectRepositoryRead}, Inspectable: true, SandboxEnforced: false,
			Targets: []JobEffectTarget{{Repository: "example/repo", Path: "src/main.go"}}},
		{Effects: []JobEffect{JobEffectRepositoryRead}, Inspectable: true, SandboxEnforced: true},
	} {
		if got := contract.EvaluateJobEffect(observation); got != JobEffectDeny {
			t.Fatalf("incomplete or unknown effect decision = %q, want deny: %+v", got, observation)
		}
	}
}

func TestCheckEffectRejectsInvalidContractAndNonCanonicalTargets(t *testing.T) {
	contract := *validJobContract(t)
	contract.Repositories[0].FileAccess = FileAccessPolicy{Level: FileAccessAll}
	for _, target := range []string{"", " ", "../secret", "src/../secret", ".git/config", ".env", "C:\\secrets\\key"} {
		if err := contract.CheckEffect(JobEffectRepositoryRead, "example/repo", target, false); err == nil {
			t.Errorf("non-canonical repository target %q must be rejected", target)
		}
	}
	if err := contract.CheckEffect(JobEffectJobContextRead, "example/repo", "src/main.go", false); err == nil {
		t.Fatal("Job context read must not carry a filesystem target")
	}
	contract.ProhibitedEffects = nil
	if err := contract.CheckEffect(JobEffectRepositoryRead, "example/repo", "src/main.go", false); err == nil {
		t.Fatal("invalid Job contract must fail closed during effect evaluation")
	}
}
