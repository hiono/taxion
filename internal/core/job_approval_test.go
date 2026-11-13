package core

import (
	"encoding/json"
	"testing"
	"time"
)

func validJobContract(t *testing.T) *JobContract {
	t.Helper()
	selection := json.RawMessage(`{"repository":{"path":"example/repo"},"execution":{"approvalRef":"config:v1"}}`)
	config, err := SelectApprovalConfiguration(selection, map[string]string{
		"repository.path":"project", "execution.approvalRef":"project",
	})
	if err != nil { t.Fatal(err) }
	return &JobContract{
		Revision:1, Acceptance:[]string{"tests pass"}, RequiredEvidence:[]string{"test-report"},
		Repositories:[]JobRepositoryScope{{Repository:"example/repo", FileAccess:FileAccessPolicy{Level:FileAccessCommitted}}},
		Operations:[]string{"repository.read","repository.write","test.run"},
		Budget:JobBudget{MaxRoleExecutions:10,MaxConcurrentExecutions:2,MaxNanoAIU:100},
		Deadline:time.Date(2026,12,1,0,0,0,0,time.UTC),
		SecurityRestrictions:[]string{"no-secret-export"}, Configuration:config,
	}
}

func TestJobApprovalBindsExactJobRevision(t *testing.T) {
	now:=time.Now().UTC()
	job:=Job{ID:"job-a",Request:"request",Goal:"goal",Status:JobActive,CreatedAt:now,UpdatedAt:now,Contract:validJobContract(t)}
	proposal,err:=JobProposal(job); if err!=nil { t.Fatal(err) }
	approval,err:=NewJobApproval("approval-1","human","user","approve bounded work",proposal,now.Add(time.Second))
	if err!=nil { t.Fatal(err) }
	if err:=approval.Verify(); err!=nil { t.Fatalf("valid approval rejected: %v",err) }
	approval.Proposal.Contract.Operations=append(approval.Proposal.Contract.Operations,"network.access")
	if err:=approval.Verify(); err==nil { t.Fatal("modified approval proposal must invalidate its record") }
}

func TestTaskScopeCannotWidenJobRepositoryScope(t *testing.T) {
	contract:=*validJobContract(t)
	allowed:=GraphTask{ID:"task-a",Repo:"example/repo",FileAccess:&FileAccessPolicy{Level:FileAccessExact,Paths:[]string{"src/main.go"}}}
	if !TaskWithinJobScope(allowed,contract) { t.Fatal("narrower exact file scope should be accepted") }
	tooBroad:=allowed; tooBroad.FileAccess=&FileAccessPolicy{Level:FileAccessAll}
	if TaskWithinJobScope(tooBroad,contract) { t.Fatal("Task must not widen committed Job access") }
	otherRepo:=allowed; otherRepo.Repo="other/repo"
	if TaskWithinJobScope(otherRepo,contract) { t.Fatal("Task must not add an unapproved repository") }
}

func TestJobApprovalRequiresExplicitUserSource(t *testing.T) {
	now:=time.Now().UTC()
	job:=Job{ID:"job-a",Request:"request",Goal:"goal",Status:JobActive,CreatedAt:now,UpdatedAt:now,Contract:validJobContract(t)}
	proposal,err:=JobProposal(job); if err!=nil { t.Fatal(err) }
	if _,err:=NewJobApproval("approval-1","agent","role","approve",proposal,now.Add(time.Second)); err==nil {
		t.Fatal("Role must not approve its own Job authority")
	}
}

func TestJobContractRejectsUnknownAndUnboundedScope(t *testing.T) {
	contract:=validJobContract(t)
	contract.Budget.MaxConcurrentExecutions=11
	if err:=contract.Validate(); err==nil { t.Fatal("concurrency cannot exceed total execution ceiling") }
	contract=validJobContract(t)
	contract.Repositories=append(contract.Repositories,contract.Repositories[0])
	if err:=contract.Validate(); err==nil { t.Fatal("duplicate repository scope must be rejected") }
}
