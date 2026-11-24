package store

import (
	"testing"

	"github.com/hiono/taxion/internal/domain"
)

func TestLegacyTaskExecutionStartWithoutWorktreeAssignmentIsDetected(t *testing.T) {
	job := domain.Job{
		RoleExecutions: []domain.RoleExecution{{
			ID: "roleexec_aaaaaaaaaaaaaaaa", TaskID: "task_aaaaaaaaaaaaaaaa",
			CommandID: "cmd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", WorktreeID: "worktree_aaaaaaaaaaaaaaaa",
		}},
		ProviderStartIntents: []domain.ProviderStartIntent{{
			ID: "providerstart_aaaaaaaaaaaaaaaa", RoleExecutionID: "roleexec_aaaaaaaaaaaaaaaa",
		}},
	}
	if !legacyTaskExecutionStartWithoutWorktreeAssignment(job, "cmd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") {
		t.Fatal("legacy Start without WorktreeAssignment was not detected")
	}
	if legacyTaskExecutionStartWithoutWorktreeAssignment(job, "cmd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb") {
		t.Fatal("legacy Start detection matched a different command")
	}
	job.WorktreeAssignments = append(job.WorktreeAssignments, domain.WorktreeAssignment{
		ID: "worktree_aaaaaaaaaaaaaaaa", OwnerRoleExecutionID: "roleexec_aaaaaaaaaaaaaaaa",
	})
	if legacyTaskExecutionStartWithoutWorktreeAssignment(job, "cmd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") {
		t.Fatal("new-format Start with a WorktreeAssignment was treated as legacy")
	}
}
