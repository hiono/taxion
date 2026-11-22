package domain

import (
	"testing"
	"time"
)

func taskTestRequest(job Job, now time.Time, commandID string) TaskRequest {
	return TaskRequest{
		JobID: job.ID, CommandID: commandID,
		Title:              "Implement bounded runtime capability",
		Purpose:            "Deliver the approved execution slice",
		AcceptanceCriteria: []string{"The requested behavior is implemented"},
	}
}

func appendTaskForTest(job *Job, task Task) {
	job.Tasks = append(job.Tasks, task)
	job.Revision++
	job.UpdatedAt = task.CreatedAt
}

func TestNewTaskInheritsApprovedScopeAndBudget(t *testing.T) {
	job, now := approvedJobForExecutionPermission(t)
	request := taskTestRequest(job, now, "cmd_a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1")
	task, err := NewTask(job, request, now)
	if err != nil {
		t.Fatalf("NewTask: %v", err)
	}
	contract := job.JobContractRevisions[len(job.JobContractRevisions)-1].Contract
	if task.ID != TaskID(request.CommandID) ||
		!equalTaskStrings(task.TargetRepositories, contract.TargetRepositories) ||
		!equalTaskStrings(task.TargetResources, contract.TargetResources) ||
		!equalTaskStrings(task.Workspaces, contract.Workspaces) ||
		task.Budget != contract.Budget || task.ConcurrencyCeiling != contract.ConcurrencyCeiling ||
		!task.Deadline.Equal(contract.Deadline) ||
		!equalTaskStrings(task.ProhibitedEffects, contract.ProhibitedEffects) ||
		!equalTaskStrings(task.ApprovalRequiredEffects, contract.ApprovalRequiredEffects) {
		t.Fatalf("Task did not inherit the approved scope: %#v", task)
	}
	appendTaskForTest(&job, task)
	if err := job.Validate(); err != nil {
		t.Fatalf("Job with inherited Task: %v", err)
	}
}

func TestNewTaskNarrowsScopeAndAddsConstraints(t *testing.T) {
	job, now := approvedJobForExecutionPermission(t)
	request := taskTestRequest(job, now, "cmd_a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2")
	request.TargetRepositories = []string{}
	request.TargetResources = []string{"resource_ci"}
	request.Budget = JobContractBudget{MaxTurns: 2, MaxTokens: 500}
	request.ConcurrencyCeiling = 1
	request.Deadline = now.Add(20 * time.Minute)
	request.ProhibitedEffects = []string{"external network mutation"}
	request.ApprovalRequiredEffects = []string{"publish artifact"}
	task, err := NewTask(job, request, now)
	if err != nil {
		t.Fatalf("NewTask with narrower scope: %v", err)
	}
	if len(task.TargetRepositories) != 0 ||
		!equalTaskStrings(task.TargetResources, []string{"resource_ci"}) ||
		task.Budget.MaxTurns != 2 || task.Budget.MaxTokens != 500 ||
		!task.Deadline.Equal(request.Deadline) ||
		!taskSubset([]string{"production deployment"}, task.ProhibitedEffects) ||
		!taskSubset([]string{"publish release"}, task.ApprovalRequiredEffects) {
		t.Fatalf("Task did not preserve narrowed / inherited boundaries: %#v", task)
	}
}

func TestTaskParentAndDependencyAreSeparateRelations(t *testing.T) {
	job, now := approvedJobForExecutionPermission(t)
	parentRequest := taskTestRequest(job, now, "cmd_a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3")
	parentRequest.Budget = JobContractBudget{MaxTurns: 2, MaxTokens: 600}
	parentRequest.Deadline = now.Add(30 * time.Minute)
	parent, err := NewTask(job, parentRequest, now)
	if err != nil {
		t.Fatalf("NewTask parent: %v", err)
	}
	appendTaskForTest(&job, parent)

	childRequest := taskTestRequest(job, now.Add(time.Minute), "cmd_a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4")
	childRequest.ParentTaskID = parent.ID
	childRequest.Budget = JobContractBudget{MaxTurns: 1, MaxTokens: 200}
	childRequest.Deadline = now.Add(20 * time.Minute)
	child, err := NewTask(job, childRequest, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("NewTask child: %v", err)
	}
	if child.ParentTaskID != parent.ID || len(child.DependsOnTaskIDs) != 0 {
		t.Fatalf("parent-child relation was converted into a dependency: %#v", child)
	}
	appendTaskForTest(&job, child)
	if err := job.Validate(); err != nil {
		t.Fatalf("Job with parent / child Tasks: %v", err)
	}
}

func TestNewTaskRejectsScopeBudgetDeadlineAndReferenceExpansion(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*TaskRequest, Job)
	}{
		{name: "foreign resource", mutate: func(r *TaskRequest, _ Job) { r.TargetResources = []string{"resource_outside"} }},
		{name: "foreign repository", mutate: func(r *TaskRequest, _ Job) { r.TargetRepositories = []string{"repo_outside"} }},
		{name: "turn budget expansion", mutate: func(r *TaskRequest, _ Job) { r.Budget.MaxTurns = 4 }},
		{name: "token budget expansion", mutate: func(r *TaskRequest, _ Job) { r.Budget.MaxTokens = 1001 }},
		{name: "concurrency expansion", mutate: func(r *TaskRequest, _ Job) { r.ConcurrencyCeiling = 2 }},
		{name: "deadline expansion", mutate: func(r *TaskRequest, _ Job) { r.Deadline = time.Now().UTC().Add(2 * time.Hour) }},
		{name: "missing parent", mutate: func(r *TaskRequest, _ Job) { r.ParentTaskID = "task_missing" }},
		{name: "missing dependency", mutate: func(r *TaskRequest, _ Job) { r.DependsOnTaskIDs = []string{"task_missing"} }},
		{name: "duplicate dependency", mutate: func(r *TaskRequest, job Job) {
			r.DependsOnTaskIDs = []string{job.Tasks[0].ID, job.Tasks[0].ID}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			job, now := approvedJobForExecutionPermission(t)
			existing, err := NewTask(job, taskTestRequest(job, now, "cmd_b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1"), now)
			if err != nil {
				t.Fatalf("NewTask existing dependency fixture: %v", err)
			}
			appendTaskForTest(&job, existing)
			request := taskTestRequest(job, now.Add(time.Minute), "cmd_b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2")
			test.mutate(&request, job)
			if _, err := NewTask(job, request, now.Add(time.Minute)); err == nil {
				t.Fatal("Task expansion or invalid reference was accepted")
			}
			if err := job.Validate(); err != nil {
				t.Fatalf("rejected request mutated Job: %v", err)
			}
		})
	}
}

func TestTaskValidationRejectsRemovedInheritedProhibition(t *testing.T) {
	job, now := approvedJobForExecutionPermission(t)
	task, err := NewTask(job, taskTestRequest(job, now, "cmd_b3b3b3b3b3b3b3b3b3b3b3b3b3b3b3b3"), now)
	if err != nil {
		t.Fatalf("NewTask: %v", err)
	}
	appendTaskForTest(&job, task)
	job.Tasks[0].ProhibitedEffects = []string{}
	if err := job.Validate(); err == nil {
		t.Fatal("Task removing an inherited prohibited effect passed aggregate validation")
	}
}

func TestTaskValidationRejectsDependencyCycleInRestoredAggregate(t *testing.T) {
	job, now := approvedJobForExecutionPermission(t)
	first, err := NewTask(job, taskTestRequest(job, now, "cmd_b4b4b4b4b4b4b4b4b4b4b4b4b4b4b4b4"), now)
	if err != nil {
		t.Fatalf("NewTask first: %v", err)
	}
	appendTaskForTest(&job, first)
	secondRequest := taskTestRequest(job, now.Add(time.Minute), "cmd_b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5")
	secondRequest.DependsOnTaskIDs = []string{first.ID}
	second, err := NewTask(job, secondRequest, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("NewTask second: %v", err)
	}
	appendTaskForTest(&job, second)
	job.Tasks[0].DependsOnTaskIDs = []string{second.ID}
	job.Tasks[0].DependsOnTaskIDs, _ = canonicalContractSet("Task DependsOnTaskIDs", job.Tasks[0].DependsOnTaskIDs, false)
	if err := job.Validate(); err == nil {
		t.Fatal("cyclic Task dependency graph passed aggregate validation")
	}
}

func TestNewTaskRejectsMissingAcceptanceCriteria(t *testing.T) {
	job, now := approvedJobForExecutionPermission(t)
	request := taskTestRequest(job, now, "cmd_b6b6b6b6b6b6b6b6b6b6b6b6b6b6b6b6")
	request.AcceptanceCriteria = nil
	if _, err := NewTask(job, request, now); err == nil {
		t.Fatal("Task without acceptance criteria was accepted")
	}
}


func TestChildTaskCannotExpandParentScope(t *testing.T) {
	job, now := approvedJobForExecutionPermission(t)
	parentRequest := taskTestRequest(job, now, "cmd_c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3")
	parentRequest.TargetRepositories = []string{}
	parentRequest.TargetResources = []string{"resource_ci"}
	parentRequest.Budget = JobContractBudget{MaxTurns: 2, MaxTokens: 500}
	parentRequest.Deadline = now.Add(30 * time.Minute)
	parent, err := NewTask(job, parentRequest, now)
	if err != nil {
		t.Fatalf("NewTask parent: %v", err)
	}
	appendTaskForTest(&job, parent)

	childRequest := taskTestRequest(job, now.Add(time.Minute), "cmd_c4c4c4c4c4c4c4c4c4c4c4c4c4c4c4c4")
	childRequest.ParentTaskID = parent.ID
	childRequest.TargetRepositories = []string{"repo_taxion"}
	childRequest.Budget = JobContractBudget{MaxTurns: 1, MaxTokens: 200}
	childRequest.Deadline = now.Add(20 * time.Minute)
	if _, err := NewTask(job, childRequest, now.Add(time.Minute)); err == nil {
		t.Fatal("child Task expanded its parent's empty repository scope")
	}
	if err := job.Validate(); err != nil {
		t.Fatalf("rejected child request mutated Job: %v", err)
	}
}
