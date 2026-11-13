package controller

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func TestBuildJobExecutionContextIsJobScopedAndExcludesRuntimeBindings(t *testing.T) {
	db, request := intakeApprovalFixture(t, false)
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		job, ok := state.Graph.JobForTask(request.TaskID)
		if !ok {
			return fmt.Errorf("intake Job is missing")
		}
		now := time.Now().UTC()
		if err := state.Graph.AppendRoleExecution(core.RoleExecution{
			ID: "context-execution", JobID: job.ID, TaskID: request.TaskID, Role: "PLANNER",
			Status: core.RoleExecutionCreated, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		if err := state.Graph.TransitionRoleExecution("context-execution", core.RoleExecutionRunning,
			"controller", "start context fixture", now.Add(time.Second)); err != nil {
			return err
		}
		if err := state.Graph.BindRoleExecutionSession("context-execution", "private-session-id", "private-config-hash"); err != nil {
			return err
		}
		return state.Graph.AppendJobMessage(core.JobMessage{
			ID: "context-message-1", JobID: job.ID, TaskID: request.TaskID,
			FromRole: "HUMAN", ToRole: "PLANNER", Body: "確認可能な前提から進めてください。",
			Source: "human:request", TargetExecutionID: "context-execution", At: now.Add(2 * time.Second),
		})
	}); err != nil {
		t.Fatal(err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	context, hash, err := BuildJobExecutionContext(state.Graph, "context-execution")
	if err != nil {
		t.Fatal(err)
	}
	if context.Job.Request != "Implement within this contract." || len(context.Tasks) != 1 ||
		len(context.Messages) != 1 || context.Messages[0].TargetExecutionID != "context-execution" ||
		context.Current.Role != "PLANNER" {
		t.Fatalf("incomplete Job context: %+v", context)
	}
	if len(hash) != 64 {
		t.Fatalf("context hash length = %d, want 64", len(hash))
	}
	rendered, err := RenderJobExecutionContext(context)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"private-session-id", "private-config-hash", "runtimeSessionId", "runtimeConfigHash", "authorizationId", "worktree"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("Role context exposes controller-only field %q", forbidden)
		}
	}
	_, repeatedHash, err := BuildJobExecutionContext(state.Graph, "context-execution")
	if err != nil || repeatedHash != hash {
		t.Fatal("identical Job context must produce a stable hash", err)
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		job, _ := state.Graph.JobForTask(request.TaskID)
		return state.Graph.AppendJobMessage(core.JobMessage{
			ID: "context-message-2", JobID: job.ID, TaskID: request.TaskID,
			FromRole: "HUMAN", ToRole: "PLANNER", Body: "追加情報です。",
			Source: "human:answer", TargetExecutionID: "context-execution", At: time.Now().UTC(),
		})
	}); err != nil {
		t.Fatal(err)
	}
	updated, _ := db.LoadWorkspace()
	_, updatedHash, err := BuildJobExecutionContext(updated.Graph, "context-execution")
	if err != nil {
		t.Fatal(err)
	}
	if updatedHash == hash {
		t.Fatal("new collaboration information must change the context hash")
	}
}

func TestBuildJobExecutionContextIncludesApprovedJobBoundary(t *testing.T) {
	now := time.Now().UTC()
	selection := json.RawMessage(`{"repository":{"path":"example/repo"},"execution":{"approvalRef":"config:v1"}}`)
	configuration, err := core.SelectApprovalConfiguration(selection, map[string]string{
		"repository.path": "project", "execution.approvalRef": "project",
	})
	if err != nil {
		t.Fatal(err)
	}
	job := core.Job{
		ID: "job-authorized", Request: "request", Goal: "goal", Status: core.JobActive,
		CreatedAt: now, UpdatedAt: now,
		Contract: &core.JobContract{
			Revision: 1, Roles: []string{"PLANNER", "REVIEWER"}, Acceptance: []string{"tests pass"}, RequiredEvidence: []string{"test-report"},
			Repositories: []core.JobRepositoryScope{{Repository: "example/repo", FileAccess: core.FileAccessPolicy{Level: core.FileAccessCommitted}}},
			Operations:   []string{"repository.read", "test.run"},
			Budget:       core.JobBudget{MaxRoleExecutions: 4, MaxConcurrentExecutions: 2, MaxNanoAIU: 20},
			Deadline:     now.Add(time.Hour), Configuration: configuration,
		},
	}
	proposal, err := core.JobProposal(job)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := core.NewJobApproval("approval-job", "human", "user", "approve bounded Job", proposal, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	task := core.GraphTask{
		ID: "task-authorized", JobID: job.ID, Title: "Authorized Task", Repo: "example/repo",
		Type: "IMPLEMENTATION", Status: core.Draft, CreatedFrom: "test:job-context",
		Issue: "NOT_REQUIRED: isolated Job context unit test",
		FileAccess: &core.FileAccessPolicy{Level: core.FileAccessCommitted},
	}
	graph := core.TaskGraph{Version: 1, Jobs: []core.Job{job}, Tasks: []core.GraphTask{task}}
	if err := graph.AppendJobApproval(approval); err != nil {
		t.Fatal(err)
	}
	if err := graph.AppendRoleExecution(core.RoleExecution{
		ID: "exec-authorized", JobID: job.ID, TaskID: task.ID, Role: "PLANNER",
		Status: core.RoleExecutionCreated, CreatedAt: now.Add(2 * time.Second), UpdatedAt: now.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if err := graph.TransitionRoleExecution("exec-authorized", core.RoleExecutionRunning, "controller", "start", now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := graph.AppendRoleExecution(core.RoleExecution{
		ID: "exec-reviewer", JobID: job.ID, TaskID: task.ID, Role: "REVIEWER",
		Status: core.RoleExecutionCreated, CreatedAt: now.Add(4 * time.Second), UpdatedAt: now.Add(4 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if err := graph.TransitionRoleExecution("exec-reviewer", core.RoleExecutionRunning, "controller", "start", now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := graph.TransitionRoleExecution("exec-reviewer", core.RoleExecutionSucceeded, "controller", "verified", now.Add(6*time.Second)); err != nil {
		t.Fatal(err)
	}
	output, err := core.NewJobOutput(core.JobOutput{
		ID: "output-authorized", JobID: job.ID, TaskID: task.ID,
		RoleExecutionID: "exec-authorized", Repository: task.Repo, Kind: "test-report",
		ContentSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		CreatedAt:     now.Add(4 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.AppendJobOutput(output); err != nil {
		t.Fatal(err)
	}
	evidence, err := core.NewJobEvidence(core.JobEvidence{
		ID: "evidence-authorized", JobID: job.ID, TaskID: task.ID,
		ProducedByExecutionID: "exec-authorized", OutputID: output.ID,
		Acceptance: "tests pass", Kind: "test-report", ContentSHA256: output.ContentSHA256,
		Status: "passed", VerifiedBy: "REVIEWER", VerifiedByExecutionID: "exec-reviewer",
		CreatedAt: now.Add(5 * time.Second), VerifiedAt: now.Add(6 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.AppendJobEvidence(evidence); err != nil {
		t.Fatal(err)
	}
	context, _, err := BuildJobExecutionContext(graph, "exec-authorized")
	if err != nil {
		t.Fatal(err)
	}
	if !context.Job.Approved || context.Job.ApprovalRevision != 1 ||
		len(context.Job.Repositories) != 1 || len(context.Job.Operations) != 2 ||
		context.Job.Budget.MaxRoleExecutions != 4 || len(context.Job.Acceptance) != 1 ||
		len(context.Outputs) != 1 || len(context.Evidence) != 1 {
		t.Fatalf("Job approval boundary missing from Role context: %+v", context.Job)
	}
	if strings.Contains(mustRenderContext(t, context), "approvalRef") {
		t.Fatal("Role context must not expose configuration selection references")
	}
}

func mustRenderContext(t *testing.T, context JobExecutionContext) string {
	t.Helper()
	rendered, err := RenderJobExecutionContext(context)
	if err != nil {
		t.Fatal(err)
	}
	return rendered
}
