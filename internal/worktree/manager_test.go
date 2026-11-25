package worktree

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/domain"
)

func TestManagerPreparesDetachedTaskWorktreeAtImmutableBaseline(t *testing.T) {
	repository := initRepository(t)
	workspace := filepath.Join(canonicalTempDir(t), "worktrees")
	manager, err := NewManager(workspace, []Repository{{ID: "repo.primary", Path: repository}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	task := domain.Task{ID: "task_0123456789abcdef", TargetRepositories: []string{"repo.primary"}}
	before := gitTestOutput(t, repository, "rev-parse", "HEAD")
	snapshot, err := manager.Prepare(context.Background(), "job_0123456789abcdef", task)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if snapshot.BaselineCommit != before || snapshot.RepositoryID != "repo.primary" {
		t.Fatalf("unexpected Worktree snapshot: %#v", snapshot)
	}
	if err := manager.Verify(context.Background(), "job_0123456789abcdef", task, snapshot); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got := gitTestOutput(t, snapshot.WorktreePath, "rev-parse", "HEAD"); got != before {
		t.Fatalf("Worktree HEAD = %s, want %s", got, before)
	}
	command := exec.CommandContext(context.Background(), "git", "-C", snapshot.WorktreePath, "symbolic-ref", "-q", "HEAD")
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("Task Worktree is not detached")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("detached HEAD check returned unexpected error: %v: %s", err, output)
	}
	if !isDetachedHeadProbeError(err) {
		t.Fatalf("detached HEAD probe did not recognize Git exit status 1: %v", err)
	}
	if got := gitTestOutput(t, repository, "rev-parse", "HEAD"); got != before {
		t.Fatalf("source repository HEAD changed: got %s, want %s", got, before)
	}
	replayed, err := manager.Prepare(context.Background(), "job_0123456789abcdef", task)
	if err != nil {
		t.Fatalf("idempotent Prepare: %v", err)
	}
	if replayed != snapshot {
		t.Fatalf("Prepare replay changed snapshot: got %#v want %#v", replayed, snapshot)
	}
}

func TestDetachedHeadProbeRejectsUnexpectedGitErrors(t *testing.T) {
	missingRepository := filepath.Join(canonicalTempDir(t), "missing-repository")
	command := exec.CommandContext(context.Background(), "git", "-C", missingRepository, "symbolic-ref", "-q", "HEAD")
	_, err := command.Output()
	if err == nil {
		t.Fatal("symbolic-ref unexpectedly succeeded for a missing repository")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() == 1 {
		t.Fatalf("missing repository returned unexpected Git error: %v", err)
	}
	if isDetachedHeadProbeError(err) {
		t.Fatalf("Git exit status %d was incorrectly accepted as detached HEAD", exitErr.ExitCode())
	}
	if isDetachedHeadProbeError(errors.New("Git executable is unavailable")) {
		t.Fatal("non-exit error was incorrectly accepted as detached HEAD")
	}
}

func TestManagerRetryRestoresPersistedBaselineAfterSourceHEADChanges(t *testing.T) {
	repository := initRepository(t)
	workspace := filepath.Join(canonicalTempDir(t), "worktrees")
	manager, err := NewManager(workspace, []Repository{{ID: "repo.primary", Path: repository}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	jobID := "job_0123456789abcdef"
	task := domain.Task{ID: "task_0123456789abcdef", TargetRepositories: []string{"repo.primary"}}
	baseline := gitTestOutput(t, repository, "rev-parse", "HEAD")
	snapshot, err := manager.Prepare(context.Background(), jobID, task)
	if err != nil {
		t.Fatalf("initial Prepare: %v", err)
	}
	if snapshot.BaselineCommit != baseline {
		t.Fatalf("initial baseline = %s, want %s", snapshot.BaselineCommit, baseline)
	}

	// Simulate interruption after the immutable baseline was persisted but before
	// the Worktree assignment was durably recorded.
	gitTestRun(t, repository, "worktree", "remove", "--force", snapshot.WorktreePath)
	if err := os.WriteFile(filepath.Join(repository, "source-update.txt"), []byte("advanced source HEAD\n"), 0o600); err != nil {
		t.Fatalf("advance source repository: %v", err)
	}
	gitTestRun(t, repository, "add", "source-update.txt")
	gitTestRun(t, repository, "commit", "-m", "advance source repository")
	if got := gitTestOutput(t, repository, "rev-parse", "HEAD"); got == baseline {
		t.Fatal("source repository HEAD did not advance")
	}

	retried, err := manager.Prepare(context.Background(), jobID, task)
	if err != nil {
		t.Fatalf("retry Prepare: %v", err)
	}
	if retried.BaselineCommit != baseline {
		t.Fatalf("retry baseline = %s, want original %s", retried.BaselineCommit, baseline)
	}
	if got := gitTestOutput(t, retried.WorktreePath, "rev-parse", "HEAD"); got != baseline {
		t.Fatalf("retry Worktree HEAD = %s, want original baseline %s", got, baseline)
	}
	if err := manager.Verify(context.Background(), jobID, task, retried); err != nil {
		t.Fatalf("Verify retried Worktree: %v", err)
	}
}

func TestManagerRejectsExistingWorktreeWithoutPersistedBaseline(t *testing.T) {
	repository := initRepository(t)
	manager, err := NewManager(filepath.Join(canonicalTempDir(t), "worktrees"), []Repository{{ID: "repo.primary", Path: repository}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	jobID := "job_0123456789abcdef"
	task := domain.Task{ID: "task_0123456789abcdef", TargetRepositories: []string{"repo.primary"}}
	snapshot, err := manager.Prepare(context.Background(), jobID, task)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := os.Remove(snapshot.WorktreePath + ".baseline.json"); err != nil {
		t.Fatalf("remove baseline record: %v", err)
	}
	if _, err := manager.Prepare(context.Background(), jobID, task); err == nil ||
		!strings.Contains(err.Error(), "no persisted immutable baseline") {
		t.Fatalf("Prepare without baseline record error = %v", err)
	}
}

func TestManagerRejectsOutOfScopeRepositoryAndChangedBaseline(t *testing.T) {
	repository := initRepository(t)
	manager, err := NewManager(filepath.Join(canonicalTempDir(t), "worktrees"), []Repository{{ID: "repo.primary", Path: repository}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	jobID := "job_0123456789abcdef"
	task := domain.Task{ID: "task_0123456789abcdef", TargetRepositories: []string{"repo.other"}}
	if _, err := manager.Prepare(context.Background(), jobID, task); err == nil {
		t.Fatal("out-of-scope repository was accepted")
	}
	task.TargetRepositories = []string{"repo.primary"}
	snapshot, err := manager.Prepare(context.Background(), jobID, task)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	gitTestRun(t, snapshot.WorktreePath, "config", "user.name", "Taxion Test")
	gitTestRun(t, snapshot.WorktreePath, "config", "user.email", "taxion-test@example.invalid")
	if err := os.WriteFile(filepath.Join(snapshot.WorktreePath, "change.txt"), []byte("changed\n"), 0o600); err != nil {
		t.Fatalf("write Worktree: %v", err)
	}
	gitTestRun(t, snapshot.WorktreePath, "add", "change.txt")
	gitTestRun(t, snapshot.WorktreePath, "commit", "-m", "advance Worktree HEAD")
	if err := manager.Verify(context.Background(), jobID, task, snapshot); err != nil {
		t.Fatalf("descendant Worktree commit was rejected: %v", err)
	}
	gitTestRun(t, repository, "checkout", "--orphan", "unrelated")
	gitTestRun(t, repository, "rm", "-rf", ".")
	if err := os.WriteFile(filepath.Join(repository, "unrelated.txt"), []byte("unrelated\n"), 0o600); err != nil {
		t.Fatalf("write unrelated repository commit: %v", err)
	}
	gitTestRun(t, repository, "add", "unrelated.txt")
	gitTestRun(t, repository, "commit", "-m", "unrelated root")
	unrelated := gitTestOutput(t, repository, "rev-parse", "HEAD")
	gitTestRun(t, snapshot.WorktreePath, "checkout", "--detach", unrelated)
	if err := manager.Verify(context.Background(), jobID, task, snapshot); err == nil ||
		!strings.Contains(err.Error(), "no longer descends") {
		t.Fatalf("unrelated Worktree history error = %v", err)
	}
}

func TestManagerCapturesImmutableOutputSnapshotOnlyForCleanWorktree(t *testing.T) {
	repository := initRepository(t)
	manager, err := NewManager(filepath.Join(canonicalTempDir(t), "worktrees"), []Repository{{ID: "repo.primary", Path: repository}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	jobID := "job_0123456789abcdef"
	task := domain.Task{ID: "task_0123456789abcdef", TargetRepositories: []string{"repo.primary"}}
	snapshot, err := manager.Prepare(context.Background(), jobID, task)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	gitTestRun(t, snapshot.WorktreePath, "config", "user.name", "Taxion Test")
	gitTestRun(t, snapshot.WorktreePath, "config", "user.email", "taxion-test@example.invalid")
	if err := os.WriteFile(filepath.Join(snapshot.WorktreePath, "output.txt"), []byte("completed output\\n"), 0o600); err != nil {
		t.Fatalf("write output: %v", err)
	}
	gitTestRun(t, snapshot.WorktreePath, "add", "output.txt")
	gitTestRun(t, snapshot.WorktreePath, "commit", "-m", "capture completed output")

	indexPath := gitTestOutput(t, snapshot.WorktreePath, "rev-parse", "--git-path", "index")
	if !filepath.IsAbs(indexPath) {
		indexPath = filepath.Join(snapshot.WorktreePath, indexPath)
	}
	indexLockPath := indexPath + ".lock"
	if err := os.WriteFile(indexLockPath, nil, 0o600); err != nil {
		t.Fatalf("create competing Git index lock: %v", err)
	}
	if _, err := manager.CaptureOutput(context.Background(), jobID, task, snapshot); err == nil ||
		!strings.Contains(err.Error(), "already locked") {
		t.Fatalf("CaptureOutput ignored a competing Git index writer: %v", err)
	}
	if err := os.Remove(indexLockPath); err != nil {
		t.Fatalf("remove competing Git index lock: %v", err)
	}

	first, err := manager.CaptureOutput(context.Background(), jobID, task, snapshot)
	if err != nil {
		t.Fatalf("CaptureOutput: %v", err)
	}
	second, err := manager.CaptureOutput(context.Background(), jobID, task, snapshot)
	if err != nil {
		t.Fatalf("repeat CaptureOutput: %v", err)
	}
	if first != second {
		t.Fatalf("output identity changed for the same commit: first=%#v second=%#v", first, second)
	}
	if got := gitTestOutput(t, snapshot.WorktreePath, "rev-parse", "HEAD"); first.CommitSHA != got {
		t.Fatalf("captured commit = %s, want %s", first.CommitSHA, got)
	}
	if got := gitTestOutput(t, snapshot.WorktreePath, "rev-parse", first.CommitSHA+"^{tree}"); first.TreeSHA != got {
		t.Fatalf("captured tree = %s, want %s", first.TreeSHA, got)
	}

	gitTestRun(t, snapshot.WorktreePath, "checkout", "--orphan", "replacement")
	gitTestRun(t, snapshot.WorktreePath, "rm", "-rf", ".")
	if err := os.WriteFile(filepath.Join(snapshot.WorktreePath, "replacement.txt"), []byte("replacement tree\\n"), 0o600); err != nil {
		t.Fatalf("write replacement tree: %v", err)
	}
	gitTestRun(t, snapshot.WorktreePath, "add", "replacement.txt")
	gitTestRun(t, snapshot.WorktreePath, "commit", "-m", "replacement tree")
	replacementCommit := gitTestOutput(t, snapshot.WorktreePath, "rev-parse", "HEAD")
	gitTestRun(t, snapshot.WorktreePath, "checkout", "--detach", first.CommitSHA)
	gitTestRun(t, snapshot.WorktreePath, "replace", first.CommitSHA, replacementCommit)
	wantTree := gitTestOutput(t, snapshot.WorktreePath, "--no-replace-objects", "rev-parse", first.CommitSHA+"^{tree}")
	replaced, err := manager.CaptureOutput(context.Background(), jobID, task, snapshot)
	if err != nil || replaced.CommitSHA != first.CommitSHA || replaced.TreeSHA != wantTree {
		t.Fatalf("CaptureOutput trusted a replacement ref: output=%#v wantTree=%s err=%v", replaced, wantTree, err)
	}
	gitTestRun(t, snapshot.WorktreePath, "replace", "-d", first.CommitSHA)

	if err := os.WriteFile(filepath.Join(snapshot.WorktreePath, "output.txt"), []byte("tampered\\n"), 0o600); err != nil {
		t.Fatalf("write assume-unchanged content: %v", err)
	}
	gitTestRun(t, snapshot.WorktreePath, "update-index", "--assume-unchanged", "output.txt")
	if _, err := manager.CaptureOutput(context.Background(), jobID, task, snapshot); err == nil || !strings.Contains(err.Error(), "assume-unchanged") {
		t.Fatalf("CaptureOutput accepted an assume-unchanged modification: %v", err)
	}
	gitTestRun(t, snapshot.WorktreePath, "update-index", "--no-assume-unchanged", "output.txt")
	gitTestRun(t, snapshot.WorktreePath, "checkout", "--", "output.txt")

	if err := os.WriteFile(filepath.Join(snapshot.WorktreePath, "output.txt"), []byte("skip-worktree change\n"), 0o600); err != nil {
		t.Fatalf("write skip-worktree content: %v", err)
	}
	gitTestRun(t, snapshot.WorktreePath, "update-index", "--skip-worktree", "output.txt")
	if _, err := manager.CaptureOutput(context.Background(), jobID, task, snapshot); err == nil ||
		!strings.Contains(err.Error(), "skip-worktree") {
		t.Fatalf("CaptureOutput accepted a skip-worktree modification: %v", err)
	}
	gitTestRun(t, snapshot.WorktreePath, "update-index", "--no-skip-worktree", "output.txt")
	gitTestRun(t, snapshot.WorktreePath, "checkout", "--", "output.txt")

	if err := os.WriteFile(filepath.Join(snapshot.WorktreePath, "uncommitted.txt"), []byte("not committed\\n"), 0o600); err != nil {
		t.Fatalf("write uncommitted output: %v", err)
	}
	if _, err := manager.CaptureOutput(context.Background(), jobID, task, snapshot); err == nil ||
		!strings.Contains(err.Error(), "must be clean") {
		t.Fatalf("dirty Worktree capture error = %v", err)
	}
}

func TestManagerRejectsMultipleRepositoriesAndUnsafeWorkspaceRoot(t *testing.T) {
	repository := initRepository(t)
	secondaryRepository := initRepository(t)
	if _, err := NewManager(filepath.Join(repository, "nested-worktrees"), []Repository{{ID: "repo.primary", Path: repository}}); err == nil {
		t.Fatal("workspace nested in repository was accepted")
	}
	manager, err := NewManager(filepath.Join(canonicalTempDir(t), "worktrees"), []Repository{
		{ID: "repo.primary", Path: repository},
		{ID: "repo.secondary", Path: secondaryRepository},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	task := domain.Task{ID: "task_0123456789abcdef", TargetRepositories: []string{"repo.primary", "repo.secondary"}}
	if _, err := manager.Prepare(context.Background(), "job_0123456789abcdef", task); err == nil {
		t.Fatal("multi-repository Task was accepted without an explicit integration Worktree")
	}
}

func TestManagerPrepareRejectsAndCleansDirtyPostCheckoutHook(t *testing.T) {
	repository := initRepository(t)
	hookPath := filepath.Join(repository, ".git", "hooks", "post-checkout")
	hook := "#!/bin/sh\nprintf 'modified by checkout hook\\n' > README.md\nexit 1\n"
	if err := os.WriteFile(hookPath, []byte(hook), 0o700); err != nil {
		t.Fatalf("write post-checkout hook: %v", err)
	}
	workspace := filepath.Join(canonicalTempDir(t), "managed-worktrees")
	manager, err := NewManager(workspace, []Repository{{ID: "repo.primary", Path: repository}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	jobID := "job_1023456789abcdef"
	task := domain.Task{ID: "task_1023456789abcdef", TargetRepositories: []string{"repo.primary"}}
	key := sha256.Sum256([]byte(jobID + "\x00" + task.ID))
	worktreePath := filepath.Join(workspace, hex.EncodeToString(key[:]))
	if _, err := manager.Prepare(context.Background(), jobID, task); err == nil {
		t.Fatal("Prepare accepted a Worktree modified by a failing post-checkout hook")
	}
	if _, err := os.Lstat(worktreePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed Worktree was left behind: path=%s err=%v", worktreePath, err)
	}
	if _, err := os.Stat(worktreePath + ".baseline.json"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("baseline created by failed Prepare was not rolled back: err=%v", err)
	}
	if got := gitTestOutput(t, repository, "worktree", "list", "--porcelain"); strings.Contains(got, worktreePath) {
		t.Fatalf("failed Worktree remains registered: %s", got)
	}
}

func TestManagerPrepareRejectsInterruptedInitialWorktree(t *testing.T) {
	repository := initRepository(t)
	workspace := filepath.Join(canonicalTempDir(t), "managed-worktrees")
	manager, err := NewManager(workspace, []Repository{{ID: "repo.primary", Path: repository}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	jobID := "job_1123456789abcdef"
	task := domain.Task{ID: "task_1123456789abcdef", TargetRepositories: []string{"repo.primary"}}
	baseline := gitTestOutput(t, repository, "rev-parse", "HEAD")
	key := sha256.Sum256([]byte(jobID + "\x00" + task.ID))
	worktreePath := filepath.Join(workspace, hex.EncodeToString(key[:]))
	record := baselineRecord{
		Version: 2, JobID: jobID, TaskID: task.ID, RepositoryID: "repo.primary",
		RepositoryPath: repository, WorktreePath: worktreePath, BaselineCommit: baseline,
		Origin: domain.WorktreeOriginTaxionCreated, InitialStateSHA256: cleanWorktreeStateSHA256(),
		HeadMode: domain.WorktreeHeadDetached,
	}
	if err := writeBaselineRecord(worktreePath+".baseline.json", record); err != nil {
		t.Fatalf("persist interrupted baseline: %v", err)
	}
	gitTestRun(t, repository, "worktree", "add", "--detach", worktreePath, baseline)
	if err := os.Remove(filepath.Join(worktreePath, "README.md")); err != nil {
		t.Fatalf("simulate interrupted checkout: %v", err)
	}
	if _, err := manager.Prepare(context.Background(), jobID, task); err == nil {
		t.Fatal("Prepare accepted an incomplete initial Worktree at its baseline commit")
	}
	if _, err := os.Stat(worktreePath); err != nil {
		t.Fatalf("rejected Worktree was unexpectedly removed or modified: %v", err)
	}
}

func TestManagerCleanupFailedPrepareIgnoresCanceledContext(t *testing.T) {
	repository := initRepository(t)
	worktreePath := filepath.Join(canonicalTempDir(t), "failed-worktree")
	gitTestRun(t, repository, "worktree", "add", "--detach", worktreePath, "HEAD")
	manager, err := NewManager(filepath.Join(canonicalTempDir(t), "managed-worktrees"), []Repository{{ID: "repo.primary", Path: repository}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := manager.cleanupFailedPrepare(ctx, repository, worktreePath, worktreePath+".baseline.json", false, nil); err != nil {
		t.Fatalf("cleanup with canceled request context: %v", err)
	}
	if _, err := os.Lstat(worktreePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed Worktree survived cleanup: path=%s err=%v", worktreePath, err)
	}
}

func TestManagerPrepareRecoversOrphanedAdoptionReservation(t *testing.T) {
	repository := initRepository(t)
	external := filepath.Join(canonicalTempDir(t), "external-worktree")
	gitTestRun(t, repository, "worktree", "add", "--detach", external, "HEAD")
	workspace := filepath.Join(canonicalTempDir(t), "managed-worktrees")
	manager, err := NewManager(workspace, []Repository{{ID: "repo.primary", Path: repository}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	jobID := "job_2023456789abcdef"
	taskA := domain.Task{ID: "task_2023456789abcdef", TargetRepositories: []string{"repo.primary"}}
	gitDir := gitTestOutput(t, external, "rev-parse", "--absolute-git-dir")
	gitDir, err = filepath.EvalSymlinks(gitDir)
	if err != nil {
		t.Fatalf("resolve external Git admin directory: %v", err)
	}
	if err := manager.reserveAdoption(jobID, taskA.ID, "repo.primary", external, gitDir); err != nil {
		t.Fatalf("persist interrupted adoption reservation: %v", err)
	}
	prepared, err := manager.Prepare(context.Background(), jobID, taskA)
	if err != nil {
		t.Fatalf("Prepare did not recover orphaned adoption reservation: %v", err)
	}
	if prepared.WorktreePath == external {
		t.Fatal("Prepare reused the external Worktree instead of creating its managed Worktree")
	}
	taskB := domain.Task{ID: "task_3023456789abcdef", TargetRepositories: []string{"repo.primary"}}
	adopted, err := manager.Adopt(context.Background(), "job_3023456789abcdef", taskB, external)
	if err != nil {
		t.Fatalf("Task B could not adopt Worktree after orphan reservation recovery: %v", err)
	}
	if adopted.WorktreePath != external || adopted.Origin != domain.WorktreeOriginAdopted {
		t.Fatalf("unexpected adopted Worktree identity: %#v", adopted)
	}
}

func TestManagerPreparedWorktreeGitDirCannotBeAdoptedByAnotherTask(t *testing.T) {
	repository := initRepository(t)
	workspace := filepath.Join(canonicalTempDir(t), "managed-worktrees")
	manager, err := NewManager(workspace, []Repository{{ID: "repo.primary", Path: repository}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	taskA := domain.Task{ID: "task_4023456789abcdef", TargetRepositories: []string{"repo.primary"}}
	prepared, err := manager.Prepare(context.Background(), "job_4023456789abcdef", taskA)
	if err != nil {
		t.Fatalf("Prepare Task A: %v", err)
	}
	moved := filepath.Join(canonicalTempDir(t), "moved-worktree")
	gitTestRun(t, repository, "worktree", "move", prepared.WorktreePath, moved)
	taskB := domain.Task{ID: "task_5023456789abcdef", TargetRepositories: []string{"repo.primary"}}
	if _, err := manager.Adopt(context.Background(), "job_5023456789abcdef", taskB, moved); err == nil ||
		!strings.Contains(err.Error(), "reserved") {
		t.Fatalf("Task B adopted Task A's moved Git admin directory: %v", err)
	}
}

func TestManagerAdoptReservesCanonicalPathAcrossDifferentGitDirs(t *testing.T) {
	repository := initRepository(t)
	external := filepath.Join(canonicalTempDir(t), "reserved-worktree")
	gitTestRun(t, repository, "worktree", "add", "--detach", external, "HEAD")
	manager, err := NewManager(filepath.Join(canonicalTempDir(t), "managed-worktrees"), []Repository{{ID: "repo.primary", Path: repository}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	taskA := domain.Task{ID: "task_9123456789abcdef", TargetRepositories: []string{"repo.primary"}}
	if _, err := manager.Adopt(context.Background(), "job_9123456789abcdef", taskA, external); err != nil {
		t.Fatalf("Adopt Task A: %v", err)
	}
	moved := filepath.Join(canonicalTempDir(t), "moved-reserved-worktree")
	gitTestRun(t, repository, "worktree", "move", external, moved)
	gitTestRun(t, repository, "worktree", "add", "--detach", external, "HEAD")
	taskB := domain.Task{ID: "task_a123456789abcdef", TargetRepositories: []string{"repo.primary"}}
	if _, err := manager.Adopt(context.Background(), "job_a123456789abcdef", taskB, external); err == nil ||
		!strings.Contains(err.Error(), "reserved") {
		t.Fatalf("Task B adopted a path still reserved by Task A with a different Git admin directory: %v", err)
	}
}

func TestManagerCleanupFailedPreparePreservesBaselineWhenWorktreeRemovalFails(t *testing.T) {
	repository := initRepository(t)
	manager, err := NewManager(filepath.Join(canonicalTempDir(t), "managed-worktrees"), []Repository{{ID: "repo.primary", Path: repository}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	worktreePath := filepath.Join(canonicalTempDir(t), "not-a-linked-worktree")
	if err := os.MkdirAll(worktreePath, 0o700); err != nil {
		t.Fatalf("create non-worktree directory: %v", err)
	}
	baselinePath := worktreePath + ".baseline.json"
	if err := os.WriteFile(baselinePath, []byte("baseline"), 0o600); err != nil {
		t.Fatalf("write baseline: %v", err)
	}
	if err := manager.cleanupFailedPrepare(context.Background(), repository, worktreePath, baselinePath, true, nil); err == nil {
		t.Fatal("cleanup unexpectedly removed a path that is not a linked Worktree")
	}
	if content, err := os.ReadFile(baselinePath); err != nil || string(content) != "baseline" {
		t.Fatalf("baseline was not preserved for cleanup retry: content=%q err=%v", content, err)
	}
}

func TestManagerCaptureOutputRequiresPersistentReservationForCreatedWorktree(t *testing.T) {
	repository := initRepository(t)
	manager, err := NewManager(filepath.Join(canonicalTempDir(t), "managed-worktrees"), []Repository{{ID: "repo.primary", Path: repository}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	jobID := "job_b123456789abcdef"
	task := domain.Task{ID: "task_b123456789abcdef", TargetRepositories: []string{"repo.primary"}}
	snapshot, err := manager.Prepare(context.Background(), jobID, task)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	reservationDirectory, err := manager.adoptionReservationDirectory("repo.primary", false)
	if err != nil {
		t.Fatalf("resolve Worktree reservation directory: %v", err)
	}
	entries, err := os.ReadDir(reservationDirectory)
	if err != nil || len(entries) != 3 {
		t.Fatalf("read Worktree reservations: entries=%d err=%v", len(entries), err)
	}
	if err := os.Remove(filepath.Join(reservationDirectory, entries[0].Name())); err != nil {
		t.Fatalf("remove Worktree reservation: %v", err)
	}
	if _, err := manager.CaptureOutput(context.Background(), jobID, task, snapshot); err == nil ||
		!strings.Contains(err.Error(), "Worktree adoption reservation") {
		t.Fatalf("CaptureOutput accepted a Worktree without its persistent reservation: %v", err)
	}
}

func TestManagerAdoptRejectsSubmoduleAssumeUnchangedIndex(t *testing.T) {
	submoduleRepository := initRepository(t)
	repository := initRepository(t)
	gitTestRun(t, repository, "-c", "protocol.file.allow=always", "submodule", "add", submoduleRepository, "modules/example")
	gitTestRun(t, repository, "add", ".gitmodules", "modules/example")
	gitTestRun(t, repository, "commit", "-m", "add submodule")
	worktreePath := filepath.Join(canonicalTempDir(t), "submodule-worktree")
	gitTestRun(t, repository, "worktree", "add", "--detach", worktreePath, "HEAD")
	gitTestRun(t, worktreePath, "-c", "protocol.file.allow=always", "submodule", "update", "--init", "--recursive")
	submodulePath := filepath.Join(worktreePath, "modules", "example")
	gitTestRun(t, submodulePath, "update-index", "--assume-unchanged", "README.md")
	if err := os.WriteFile(filepath.Join(submodulePath, "README.md"), []byte("hidden submodule change\n"), 0o600); err != nil {
		t.Fatalf("modify assume-unchanged submodule file: %v", err)
	}
	manager, err := NewManager(
		filepath.Join(canonicalTempDir(t), "managed-worktrees"),
		[]Repository{{ID: "repo.primary", Path: repository}},
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	task := domain.Task{ID: "task_6023456789abcdef", TargetRepositories: []string{"repo.primary"}}
	if _, err := manager.Adopt(context.Background(), "job_6023456789abcdef", task, worktreePath); err == nil ||
		!strings.Contains(err.Error(), "assume-unchanged") {
		t.Fatalf("Adopt accepted a submodule assume-unchanged index entry: %v", err)
	}
}

func TestManagerAdoptionDigestIgnoresSubmoduleTagLabels(t *testing.T) {
	submoduleRepository := initRepository(t)
	repository := initRepository(t)
	gitTestRun(t, repository, "-c", "protocol.file.allow=always", "submodule", "add", submoduleRepository, "modules/example")
	gitTestRun(t, repository, "add", ".gitmodules", "modules/example")
	gitTestRun(t, repository, "commit", "-m", "add submodule")
	worktreePath := filepath.Join(canonicalTempDir(t), "tagged-submodule-worktree")
	gitTestRun(t, repository, "worktree", "add", "--detach", worktreePath, "HEAD")
	gitTestRun(t, worktreePath, "-c", "protocol.file.allow=always", "submodule", "update", "--init", "--recursive")
	manager, err := NewManager(
		filepath.Join(canonicalTempDir(t), "managed-worktrees"),
		[]Repository{{ID: "repo.primary", Path: repository}},
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	task := domain.Task{ID: "task_7023456789abcdef", TargetRepositories: []string{"repo.primary"}}
	jobID := "job_7023456789abcdef"
	first, err := manager.Adopt(context.Background(), jobID, task, worktreePath)
	if err != nil {
		t.Fatalf("initial Adopt: %v", err)
	}
	submodulePath := filepath.Join(worktreePath, "modules", "example")
	gitTestRun(t, submodulePath, "config", "user.name", "Taxion Test")
	gitTestRun(t, submodulePath, "config", "user.email", "taxion-test@example.invalid")
	gitTestRun(t, submodulePath, "tag", "-a", "state-label", "-m", "state label")
	replayed, err := manager.Adopt(context.Background(), jobID, task, worktreePath)
	if err != nil || replayed != first {
		t.Fatalf("submodule tag label changed the adoption digest: first=%#v replayed=%#v err=%v", first, replayed, err)
	}
}

func TestManagerCaptureOutputRejectsSubmoduleAssumeUnchangedIndex(t *testing.T) {
	submoduleRepository := initRepository(t)
	repository := initRepository(t)
	gitTestRun(t, repository, "-c", "protocol.file.allow=always", "submodule", "add", submoduleRepository, "modules/example")
	gitTestRun(t, repository, "add", ".gitmodules", "modules/example")
	gitTestRun(t, repository, "commit", "-m", "add submodule")
	manager, err := NewManager(
		filepath.Join(canonicalTempDir(t), "managed-worktrees"),
		[]Repository{{ID: "repo.primary", Path: repository}},
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	task := domain.Task{ID: "task_8023456789abcdef", TargetRepositories: []string{"repo.primary"}}
	snapshot, err := manager.Prepare(context.Background(), "job_8023456789abcdef", task)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	gitTestRun(t, snapshot.WorktreePath, "-c", "protocol.file.allow=always", "submodule", "update", "--init", "--recursive")
	submodulePath := filepath.Join(snapshot.WorktreePath, "modules", "example")
	gitTestRun(t, submodulePath, "update-index", "--assume-unchanged", "README.md")
	if err := os.WriteFile(filepath.Join(submodulePath, "README.md"), []byte("hidden output change\n"), 0o600); err != nil {
		t.Fatalf("modify assume-unchanged submodule file: %v", err)
	}
	if _, err := manager.CaptureOutput(context.Background(), "job_8023456789abcdef", task, snapshot); err == nil ||
		!strings.Contains(err.Error(), "assume-unchanged") {
		t.Fatalf("CaptureOutput accepted a submodule assume-unchanged index entry: %v", err)
	}
}

func TestParseWorktreeStatusHeadBindsCleanStateToObservedCommit(t *testing.T) {
	commit := strings.Repeat("a", 40)
	status := "# branch.oid " + commit + "\n# branch.head (detached)\n"
	head, dirty, err := parseWorktreeStatusHead(status)
	if err != nil || head != commit || dirty {
		t.Fatalf("clean status did not report its exact commit: head=%q dirty=%v err=%v", head, dirty, err)
	}
	dirtyStatus := status + "1 .M N... 100644 100644 100644 " + strings.Repeat("b", 40) + " " + strings.Repeat("b", 40) + " README.md\n"
	head, dirty, err = parseWorktreeStatusHead(dirtyStatus)
	if err != nil || head != commit || !dirty {
		t.Fatalf("dirty status was not detected: head=%q dirty=%v err=%v", head, dirty, err)
	}
}

func canonicalTempDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("resolve temporary test directory: %v", err)
	}
	return canonical
}

func initRepository(t *testing.T) string {
	t.Helper()
	root := canonicalTempDir(t)
	gitTestRun(t, root, "init", "--initial-branch=taxion-test-base")
	gitTestRun(t, root, "config", "user.name", "Taxion Test")
	gitTestRun(t, root, "config", "user.email", "taxion-test@example.invalid")
	gitTestRun(t, root, "config", "commit.gpgSign", "false")
	excludesFile := filepath.Join(root, "empty-global-excludes")
	if err := os.WriteFile(excludesFile, nil, 0o600); err != nil {
		t.Fatalf("write empty global excludes file: %v", err)
	}
	gitTestRun(t, root, "config", "core.excludesFile", excludesFile)
	if got := gitTestOutput(t, root, "config", "--path", "core.excludesFile"); got != excludesFile {
		t.Fatalf("repository-local core.excludesFile = %q, want %q", got, excludesFile)
	}
	if got := gitTestOutput(t, root, "config", "--bool", "commit.gpgSign"); got != "false" {
		t.Fatalf("repository-local commit.gpgSign = %q, want false", got)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("baseline\n"), 0o600); err != nil {
		t.Fatalf("write repository file: %v", err)
	}
	gitTestRun(t, root, "add", "README.md")
	gitTestRun(t, root, "commit", "-m", "initial commit")
	return root
}

func gitTestRun(t *testing.T, directory string, args ...string) {
	t.Helper()
	command := exec.CommandContext(context.Background(), "git", append([]string{"-C", directory}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func gitTestOutput(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.CommandContext(context.Background(), "git", append([]string{"-C", directory}, args...)...)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(output))
}

func TestManagerAdoptsExistingLinkedWorktreeWithoutChangingItsState(t *testing.T) {
	repository := initRepository(t)
	worktreePath := filepath.Join(canonicalTempDir(t), "existing-worktree")
	gitTestRun(t, repository, "worktree", "add", "--detach", worktreePath, "HEAD")
	if err := os.WriteFile(filepath.Join(worktreePath, "README.md"), []byte("staged change\n"), 0o600); err != nil {
		t.Fatalf("write staged change: %v", err)
	}
	gitTestRun(t, worktreePath, "add", "README.md")
	if err := os.WriteFile(filepath.Join(worktreePath, "README.md"), []byte("unstaged change\n"), 0o600); err != nil {
		t.Fatalf("write unstaged change: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktreePath, "new-file.txt"), []byte("untracked content\n"), 0o600); err != nil {
		t.Fatalf("write untracked file: %v", err)
	}
	beforeHEAD := gitTestOutput(t, worktreePath, "rev-parse", "HEAD")
	beforeStatus := gitTestOutput(t, worktreePath, "--no-optional-locks", "status", "--porcelain=v2", "--untracked-files=all", "--ignore-submodules=none")
	beforeStaged := gitTestOutput(t, worktreePath, "--no-optional-locks", "diff", "--cached", "--stat", "--ignore-submodules=none", "HEAD")
	indexPath := gitTestOutput(t, worktreePath, "rev-parse", "--git-path", "index")
	if !filepath.IsAbs(indexPath) {
		indexPath = filepath.Join(worktreePath, indexPath)
	}
	beforeIndex, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read Worktree index before adoption: %v", err)
	}
	beforeTracked, err := os.ReadFile(filepath.Join(worktreePath, "README.md"))
	if err != nil {
		t.Fatalf("read tracked file before adoption: %v", err)
	}
	beforeUntracked, err := os.ReadFile(filepath.Join(worktreePath, "new-file.txt"))
	if err != nil {
		t.Fatalf("read untracked file before adoption: %v", err)
	}

	manager, err := NewManager(filepath.Join(canonicalTempDir(t), "managed-worktrees"), []Repository{{ID: "repo.primary", Path: repository}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	task := domain.Task{ID: "task_0123456789abcdef", TargetRepositories: []string{"repo.primary"}}
	jobID := "job_0123456789abcdef"
	snapshot, err := manager.Adopt(context.Background(), jobID, task, worktreePath)
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if snapshot.Origin != domain.WorktreeOriginAdopted ||
		snapshot.InitialStateSHA256 == "" ||
		snapshot.WorktreePath != worktreePath ||
		snapshot.BaselineCommit != beforeHEAD {
		t.Fatalf("adoption identity is incomplete: %#v", snapshot)
	}
	if got := gitTestOutput(t, worktreePath, "rev-parse", "HEAD"); got != beforeHEAD {
		t.Fatalf("adoption changed HEAD: got %s, want %s", got, beforeHEAD)
	}
	if got := gitTestOutput(t, worktreePath, "--no-optional-locks", "status", "--porcelain=v2", "--untracked-files=all", "--ignore-submodules=none"); got != beforeStatus {
		t.Fatalf("adoption changed Worktree status: got %q, want %q", got, beforeStatus)
	}
	if got := gitTestOutput(t, worktreePath, "--no-optional-locks", "diff", "--cached", "--stat", "--ignore-submodules=none", "HEAD"); got != beforeStaged {
		t.Fatalf("adoption changed index: got %q, want %q", got, beforeStaged)
	}
	afterIndex, err := os.ReadFile(indexPath)
	if err != nil || string(afterIndex) != string(beforeIndex) {
		t.Fatalf("adoption changed Git index bytes: err=%v", err)
	}
	afterTracked, err := os.ReadFile(filepath.Join(worktreePath, "README.md"))
	if err != nil || string(afterTracked) != string(beforeTracked) {
		t.Fatalf("adoption changed tracked file: content=%q err=%v", afterTracked, err)
	}
	afterUntracked, err := os.ReadFile(filepath.Join(worktreePath, "new-file.txt"))
	if err != nil || string(afterUntracked) != string(beforeUntracked) {
		t.Fatalf("adoption changed untracked file: content=%q err=%v", afterUntracked, err)
	}
	if err := manager.Verify(context.Background(), jobID, task, snapshot); err != nil {
		t.Fatalf("Verify adopted Worktree: %v", err)
	}
	beforeWorktreeList := gitTestOutput(t, repository, "worktree", "list", "--porcelain")
	if _, err := manager.Prepare(context.Background(), jobID, task); err == nil {
		t.Fatal("Prepare created a second managed Worktree for an adopted Task")
	}
	if afterWorktreeList := gitTestOutput(t, repository, "worktree", "list", "--porcelain"); afterWorktreeList != beforeWorktreeList {
		t.Fatalf("Prepare changed Git Worktree registrations for adopted Task: before=%q after=%q", beforeWorktreeList, afterWorktreeList)
	}
	replayed, err := manager.Adopt(context.Background(), jobID, task, worktreePath)
	if err != nil || replayed != snapshot {
		t.Fatalf("exact adoption replay changed identity: got=%#v want=%#v err=%v", replayed, snapshot, err)
	}
	if err := os.WriteFile(filepath.Join(worktreePath, "new-file.txt"), []byte("changed after failed Start\\n"), 0o600); err != nil {
		t.Fatalf("change Worktree before same-Start retry: %v", err)
	}
	if _, err := manager.Adopt(context.Background(), jobID, task, worktreePath); err == nil ||
		!strings.Contains(err.Error(), "original state") {
		t.Fatalf("same-Start retry accepted a changed Worktree: %v", err)
	}
	if _, err := manager.Adopt(context.Background(), "job_abcdef0123456789", task, worktreePath); err == nil {
		t.Fatal("the same physical Worktree was adopted by a second Job")
	}
}

func TestManagerAdoptsBranchAttachedWorktreeAndPreservesBranchIdentity(t *testing.T) {
	repository := initRepository(t)
	worktreePath := filepath.Join(canonicalTempDir(t), "branch-worktree")
	gitTestRun(t, repository, "worktree", "add", worktreePath, "-b", "adopted-feature")
	manager, err := NewManager(filepath.Join(canonicalTempDir(t), "managed-worktrees"), []Repository{{ID: "repo.primary", Path: repository}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	task := domain.Task{ID: "task_abcdef0123456789", TargetRepositories: []string{"repo.primary"}}
	snapshot, err := manager.Adopt(context.Background(), "job_abcdef0123456789", task, worktreePath)
	if err != nil {
		t.Fatalf("Adopt branch-attached Worktree: %v", err)
	}
	if snapshot.HeadMode != domain.WorktreeHeadBranchAttached || snapshot.HeadRef != "refs/heads/adopted-feature" {
		t.Fatalf("branch identity was not recorded: %#v", snapshot)
	}
	if err := manager.Verify(context.Background(), "job_abcdef0123456789", task, snapshot); err != nil {
		t.Fatalf("Verify branch-attached Worktree: %v", err)
	}
	gitTestRun(t, worktreePath, "checkout", "--detach", "HEAD")
	if err := manager.Verify(context.Background(), "job_abcdef0123456789", task, snapshot); err == nil {
		t.Fatal("Worktree HEAD mode change was accepted")
	}
}

func TestManagerRequiresPersistentReservationForAdoptedWorktree(t *testing.T) {
	repository := initRepository(t)
	worktreePath := filepath.Join(canonicalTempDir(t), "reserved-worktree")
	gitTestRun(t, repository, "worktree", "add", "--detach", worktreePath, "HEAD")
	manager, err := NewManager(filepath.Join(canonicalTempDir(t), "managed-worktrees"), []Repository{{ID: "repo.primary", Path: repository}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	task := domain.Task{ID: "task_0123456789abcdef", TargetRepositories: []string{"repo.primary"}}
	snapshot, err := manager.Adopt(context.Background(), "job_0123456789abcdef", task, worktreePath)
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	reservationDirectory, err := manager.adoptionReservationDirectory("repo.primary", false)
	if err != nil {
		t.Fatalf("resolve adoption reservation directory: %v", err)
	}
	entries, err := os.ReadDir(reservationDirectory)
	if err != nil || len(entries) != 3 {
		t.Fatalf("read adoption reservation: entries=%v err=%v", entries, err)
	}
	if err := os.Remove(filepath.Join(reservationDirectory, entries[0].Name())); err != nil {
		t.Fatalf("remove adoption reservation: %v", err)
	}
	if err := manager.Verify(context.Background(), "job_0123456789abcdef", task, snapshot); err == nil ||
		!strings.Contains(err.Error(), "adoption reservation") {
		t.Fatalf("Verify without reservation error = %v", err)
	}
}

func TestManagerAdoptionReservationsAreUniqueByTaskAndGitAdminDirectory(t *testing.T) {
	repository := initRepository(t)
	worktreeA := filepath.Join(canonicalTempDir(t), "adoption-a")
	worktreeB := filepath.Join(canonicalTempDir(t), "adoption-b")
	gitTestRun(t, repository, "worktree", "add", "--detach", worktreeA, "HEAD")
	gitTestRun(t, repository, "worktree", "add", "--detach", worktreeB, "HEAD")
	workspace := filepath.Join(canonicalTempDir(t), "managed-worktrees")
	manager, err := NewManager(workspace, []Repository{{ID: "repo.primary", Path: repository}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	managerB, err := NewManager(workspace, []Repository{{ID: "repo.primary", Path: repository}})
	if err != nil {
		t.Fatalf("NewManager second instance: %v", err)
	}
	task := domain.Task{ID: "task_1111111111111111", TargetRepositories: []string{"repo.primary"}}
	jobID := "job_1111111111111111"
	type adoptResult struct {
		snapshot Snapshot
		err      error
	}
	results := make(chan adoptResult, 2)
	go func() {
		snapshot, err := manager.Adopt(context.Background(), jobID, task, worktreeA)
		results <- adoptResult{snapshot: snapshot, err: err}
	}()
	go func() {
		snapshot, err := managerB.Adopt(context.Background(), jobID, task, worktreeB)
		results <- adoptResult{snapshot: snapshot, err: err}
	}()
	first, second := <-results, <-results
	if (first.err == nil) == (second.err == nil) {
		t.Fatalf("concurrent adoption did not select exactly one Worktree: first=%v second=%v", first.err, second.err)
	}
	winner := first
	if winner.err != nil {
		winner = second
	}
	reservationDirectory, err := manager.adoptionReservationDirectory("repo.primary", false)
	if err != nil {
		t.Fatalf("resolve adoption reservation directory: %v", err)
	}
	entries, err := os.ReadDir(reservationDirectory)
	if err != nil || len(entries) != 3 {
		t.Fatalf("losing Task reservation was not cleaned up: entries=%d err=%v", len(entries), err)
	}
	movedPath := filepath.Join(canonicalTempDir(t), "adoption-moved")
	gitTestRun(t, repository, "worktree", "move", winner.snapshot.WorktreePath, movedPath)
	otherTask := domain.Task{ID: "task_2222222222222222", TargetRepositories: []string{"repo.primary"}}
	if _, err := manager.Adopt(context.Background(), "job_2222222222222222", otherTask, movedPath); err == nil {
		t.Fatal("the same Git admin directory was adopted after its Worktree path changed")
	}
	entries, err = os.ReadDir(reservationDirectory)
	if err != nil || len(entries) != 3 {
		t.Fatalf("losing Git admin directory reservation was not cleaned up: entries=%d err=%v", len(entries), err)
	}
}

func TestManagerCaptureOutputRejectsDirtySubmodule(t *testing.T) {
	repository := initRepository(t)
	submodule := initRepository(t)
	gitTestRun(t, repository, "-c", "protocol.file.allow=always", "submodule", "add", submodule, "nested")
	gitTestRun(t, repository, "commit", "-m", "add submodule")
	manager, err := NewManager(filepath.Join(canonicalTempDir(t), "managed-worktrees"), []Repository{{ID: "repo.primary", Path: repository}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	task := domain.Task{ID: "task_3333333333333333", TargetRepositories: []string{"repo.primary"}}
	snapshot, err := manager.Prepare(context.Background(), "job_3333333333333333", task)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	gitTestRun(t, snapshot.WorktreePath, "-c", "protocol.file.allow=always", "submodule", "update", "--init")
	if err := os.WriteFile(filepath.Join(snapshot.WorktreePath, "nested", "untracked.txt"), []byte("dirty submodule\n"), 0o600); err != nil {
		t.Fatalf("write dirty submodule file: %v", err)
	}
	if _, err := manager.CaptureOutput(context.Background(), "job_3333333333333333", task, snapshot); err == nil ||
		!strings.Contains(err.Error(), "clean") {
		t.Fatalf("CaptureOutput accepted a dirty submodule: %v", err)
	}
}

func TestWriteExclusiveAtomicDoesNotOverwriteExistingRecord(t *testing.T) {
	path := filepath.Join(canonicalTempDir(t), "nested", "record.json")
	first := []byte("{\"state\":\"complete\"}")
	if err := writeExclusiveAtomic(path, first, 0o600); err != nil {
		t.Fatalf("write first atomic record: %v", err)
	}
	if err := writeExclusiveAtomic(path, []byte("{\"state\":\"partial\"}"), 0o600); !errors.Is(err, os.ErrExist) {
		t.Fatalf("second atomic write error = %v, want os.ErrExist", err)
	}
	actual, err := os.ReadFile(path)
	if err != nil || string(actual) != string(first) {
		t.Fatalf("atomic record was overwritten or incomplete: data=%q err=%v", actual, err)
	}
}

func TestManagerAdoptRejectsDirtySubmoduleState(t *testing.T) {
	repository := initRepository(t)
	submodule := initRepository(t)
	gitTestRun(t, repository, "-c", "protocol.file.allow=always", "submodule", "add", submodule, "nested")
	gitTestRun(t, repository, "commit", "-m", "add submodule")
	worktreePath := filepath.Join(canonicalTempDir(t), "adopted-with-submodule")
	gitTestRun(t, repository, "worktree", "add", "--detach", worktreePath, "HEAD")
	gitTestRun(t, worktreePath, "-c", "protocol.file.allow=always", "submodule", "update", "--init")
	manager, err := NewManager(filepath.Join(canonicalTempDir(t), "managed-worktrees"), []Repository{{ID: "repo.primary", Path: repository}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	task := domain.Task{ID: "task_4444444444444444", TargetRepositories: []string{"repo.primary"}}
	if _, err := manager.Adopt(context.Background(), "job_4444444444444444", task, worktreePath); err != nil {
		t.Fatalf("Adopt clean submodule: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktreePath, "nested", "untracked.txt"), []byte("dirty submodule\\n"), 0o600); err != nil {
		t.Fatalf("write dirty submodule file: %v", err)
	}
	if _, err := manager.Adopt(context.Background(), "job_4444444444444444", task, worktreePath); err == nil || !strings.Contains(err.Error(), "dirty submodule") {
		t.Fatalf("Adopt accepted changed submodule contents: %v", err)
	}
}

func TestManagerAdoptRejectsIndexFlagsAndStreamsLargeUntrackedFiles(t *testing.T) {
	repository := initRepository(t)
	worktreePath := filepath.Join(canonicalTempDir(t), "adopt-index-flags")
	gitTestRun(t, repository, "worktree", "add", "--detach", worktreePath, "HEAD")
	manager, err := NewManager(filepath.Join(canonicalTempDir(t), "managed-worktrees"), []Repository{{ID: "repo.primary", Path: repository}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	task := domain.Task{ID: "task_5555555555555555", TargetRepositories: []string{"repo.primary"}}
	jobID := "job_5555555555555555"
	readme := filepath.Join(worktreePath, "README.md")
	if err := os.WriteFile(readme, []byte("hidden tracked change\n"), 0o600); err != nil {
		t.Fatalf("write tracked change: %v", err)
	}
	gitTestRun(t, worktreePath, "update-index", "--assume-unchanged", "README.md")
	if _, err := manager.Adopt(context.Background(), jobID, task, worktreePath); err == nil ||
		!strings.Contains(err.Error(), "assume-unchanged") {
		t.Fatalf("Adopt accepted assume-unchanged tracked content: %v", err)
	}
	gitTestRun(t, worktreePath, "update-index", "--no-assume-unchanged", "README.md")
	gitTestRun(t, worktreePath, "checkout", "--", "README.md")

	if err := os.WriteFile(readme, []byte("hidden skip-worktree change\n"), 0o600); err != nil {
		t.Fatalf("write skip-worktree change: %v", err)
	}
	gitTestRun(t, worktreePath, "update-index", "--skip-worktree", "README.md")
	if _, err := manager.Adopt(context.Background(), jobID, task, worktreePath); err == nil ||
		!strings.Contains(err.Error(), "skip-worktree") {
		t.Fatalf("Adopt accepted skip-worktree tracked content: %v", err)
	}
	gitTestRun(t, worktreePath, "update-index", "--no-skip-worktree", "README.md")
	gitTestRun(t, worktreePath, "checkout", "--", "README.md")

	largePath := filepath.Join(worktreePath, "large-untracked.bin")
	if err := os.WriteFile(largePath, []byte(strings.Repeat("x", 4*1024*1024)), 0o600); err != nil {
		t.Fatalf("write large untracked file: %v", err)
	}
	snapshot, err := manager.Adopt(context.Background(), jobID, task, worktreePath)
	if err != nil {
		t.Fatalf("Adopt with large untracked file: %v", err)
	}
	if err := manager.Verify(context.Background(), jobID, task, snapshot); err != nil {
		t.Fatalf("Verify adopted Worktree with large untracked file: %v", err)
	}
}

func TestManagerAdoptRecoversOrphanedTaskReservationAfterGitDirConflict(t *testing.T) {
	repository := initRepository(t)
	worktreeA := filepath.Join(canonicalTempDir(t), "orphaned-task-worktree")
	worktreeB := filepath.Join(canonicalTempDir(t), "reserved-worktree")
	worktreeC := filepath.Join(canonicalTempDir(t), "replacement-worktree")
	gitTestRun(t, repository, "worktree", "add", "--detach", worktreeA, "HEAD")
	gitTestRun(t, repository, "worktree", "add", "--detach", worktreeB, "HEAD")
	gitTestRun(t, repository, "worktree", "add", "--detach", worktreeC, "HEAD")
	manager, err := NewManager(filepath.Join(canonicalTempDir(t), "managed-worktrees"), []Repository{{ID: "repo.primary", Path: repository}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	taskB := domain.Task{ID: "task_bbbbbbbbbbbbbbbb", TargetRepositories: []string{"repo.primary"}}
	if _, err := manager.Adopt(context.Background(), "job_bbbbbbbbbbbbbbbb", taskB, worktreeB); err != nil {
		t.Fatalf("Adopt reserved Worktree B: %v", err)
	}
	gitDirA, err := gitOutput(context.Background(), worktreeA, "rev-parse", "--absolute-git-dir")
	if err != nil {
		t.Fatalf("resolve orphaned Worktree Git directory: %v", err)
	}
	gitDirA, err = filepath.EvalSymlinks(gitDirA)
	if err != nil {
		t.Fatalf("resolve orphaned Worktree Git directory symlinks: %v", err)
	}
	taskA := domain.Task{ID: "task_aaaaaaaaaaaaaaaa", TargetRepositories: []string{"repo.primary"}}
	jobA := "job_aaaaaaaaaaaaaaaa"
	orphan := adoptionReservation{
		JobID: jobA, TaskID: taskA.ID, RepositoryID: "repo.primary",
		WorktreePath: worktreeA, GitDir: gitDirA,
	}
	reservationDirectory, err := manager.adoptionReservationDirectory("repo.primary", true)
	if err != nil {
		t.Fatalf("create adoption reservation directory: %v", err)
	}
	taskKey := sha256.Sum256([]byte(jobA + "\x00" + taskA.ID))
	taskReservationPath := filepath.Join(reservationDirectory, "task-"+hex.EncodeToString(taskKey[:])+".json")
	gitDirKeyA := sha256.Sum256([]byte(gitDirA))
	gitDirReservationPath := filepath.Join(reservationDirectory, "gitdir-"+hex.EncodeToString(gitDirKeyA[:])+".json")
	if _, err := writeAdoptionReservation(taskReservationPath, orphan); err != nil {
		t.Fatalf("write interrupted Task reservation: %v", err)
	}
	if _, err := writeAdoptionReservation(gitDirReservationPath, orphan); err != nil {
		t.Fatalf("write interrupted Git directory reservation: %v", err)
	}

	if _, err := manager.Adopt(context.Background(), jobA, taskA, worktreeB); err == nil ||
		!strings.Contains(err.Error(), "already reserved") {
		t.Fatalf("Adopt accepted another Task's reserved Git directory: %v", err)
	}
	for _, path := range []string{taskReservationPath, gitDirReservationPath} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("orphaned adoption reservation %s remained after conflict: stat err=%v", path, err)
		}
	}
	if _, err := manager.Adopt(context.Background(), jobA, taskA, worktreeC); err != nil {
		t.Fatalf("Adopt retry with a different Worktree after orphan recovery: %v", err)
	}
}

func TestManagerOrphanedReservationCleanupPreservesCommittedAdoption(t *testing.T) {
	repository := initRepository(t)
	manager, err := NewManager(filepath.Join(canonicalTempDir(t), "managed-worktrees"), []Repository{{ID: "repo.primary", Path: repository}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	reservation := adoptionReservation{
		JobID: "job_cccccccccccccccc", TaskID: "task_cccccccccccccccc",
		RepositoryID: "repo.primary", WorktreePath: filepath.Join(canonicalTempDir(t), "worktree"),
		GitDir: filepath.Join(canonicalTempDir(t), "git", "worktrees", "task"),
	}
	directory, err := manager.adoptionReservationDirectory("repo.primary", true)
	if err != nil {
		t.Fatalf("create adoption reservation directory: %v", err)
	}
	taskKey := sha256.Sum256([]byte(reservation.JobID + "\x00" + reservation.TaskID))
	gitDirKey := sha256.Sum256([]byte(reservation.GitDir))
	taskPath := filepath.Join(directory, "task-"+hex.EncodeToString(taskKey[:])+".json")
	gitDirPath := filepath.Join(directory, "gitdir-"+hex.EncodeToString(gitDirKey[:])+".json")
	if _, err := writeAdoptionReservation(taskPath, reservation); err != nil {
		t.Fatalf("write Task reservation: %v", err)
	}
	if _, err := writeAdoptionReservation(gitDirPath, reservation); err != nil {
		t.Fatalf("write Git directory reservation: %v", err)
	}
	record := baselineRecord{
		Version: 2, JobID: reservation.JobID, TaskID: reservation.TaskID,
		RepositoryID: reservation.RepositoryID, RepositoryPath: filepath.Join(canonicalTempDir(t), "source"),
		WorktreePath: reservation.WorktreePath, BaselineCommit: strings.Repeat("a", 40),
		Origin: domain.WorktreeOriginAdopted, InitialStateSHA256: strings.Repeat("b", 64),
		HeadMode: domain.WorktreeHeadDetached,
	}
	if err := writeBaselineRecord(adoptionRegistrationPath(manager.root, reservation.JobID, reservation.TaskID)+".baseline.json", record); err != nil {
		t.Fatalf("write committed baseline record: %v", err)
	}
	if err := manager.removeOrphanedAdoptionReservation(reservation); err != nil {
		t.Fatalf("cleanup committed reservation: %v", err)
	}
	for _, path := range []string{taskPath, gitDirPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("committed reservation %s was removed: %v", path, err)
		}
	}
}

func TestHashTrackedWorktreeFilesIncludesUnstagedDeletion(t *testing.T) {
	firstRepository := initRepository(t)
	secondRepository := initRepository(t)
	for _, repository := range []string{firstRepository, secondRepository} {
		if err := os.WriteFile(filepath.Join(repository, "second.txt"), []byte("second baseline\n"), 0o600); err != nil {
			t.Fatalf("write second tracked file: %v", err)
		}
		gitTestRun(t, repository, "add", "second.txt")
		gitTestRun(t, repository, "commit", "-m", "add second tracked file")
	}
	if err := os.Remove(filepath.Join(firstRepository, "README.md")); err != nil {
		t.Fatalf("remove first tracked file: %v", err)
	}
	if err := os.Remove(filepath.Join(secondRepository, "second.txt")); err != nil {
		t.Fatalf("remove second tracked file: %v", err)
	}
	firstHash := sha256.New()
	if err := hashTrackedWorktreeFiles(context.Background(), firstRepository, firstHash); err != nil {
		t.Fatalf("hash first Worktree with unstaged deletion: %v", err)
	}
	secondHash := sha256.New()
	if err := hashTrackedWorktreeFiles(context.Background(), secondRepository, secondHash); err != nil {
		t.Fatalf("hash second Worktree with unstaged deletion: %v", err)
	}
	if got := len(firstHash.Sum(nil)); got != sha256.Size {
		t.Fatalf("tracked Worktree digest size = %d, want %d", got, sha256.Size)
	}
	if string(firstHash.Sum(nil)) == string(secondHash.Sum(nil)) {
		t.Fatal("tracked Worktree digest did not distinguish different unstaged deletions")
	}
}

type countingReader struct {
	reads int
}

func (r *countingReader) Read(buffer []byte) (int, error) {
	r.reads++
	return copy(buffer, "x"), nil
}

func TestContextReaderStopsReadingAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := &countingReader{}
	wrapped := contextReader{ctx: ctx, reader: reader}
	buffer := make([]byte, 1)
	if _, err := wrapped.Read(buffer); err != nil {
		t.Fatalf("first read: %v", err)
	}
	cancel()
	if _, err := wrapped.Read(buffer); !errors.Is(err, context.Canceled) {
		t.Fatalf("read after cancellation = %v, want context.Canceled", err)
	}
	if reader.reads != 1 {
		t.Fatalf("underlying reader calls = %d, want 1", reader.reads)
	}
}

func TestManagerAdoptRejectsReservationAcrossWorkspaceRoots(t *testing.T) {
	repository := initRepository(t)
	managerA, err := NewManager(filepath.Join(canonicalTempDir(t), "workspace-a"), []Repository{{ID: "repo.primary", Path: repository}})
	if err != nil {
		t.Fatalf("NewManager A: %v", err)
	}
	managerB, err := NewManager(filepath.Join(canonicalTempDir(t), "workspace-b"), []Repository{{ID: "repo.primary", Path: repository}})
	if err != nil {
		t.Fatalf("NewManager B: %v", err)
	}
	taskA := domain.Task{ID: "task_aaaaaaaaaaaaaaaa", TargetRepositories: []string{"repo.primary"}}
	snapshot, err := managerA.Prepare(context.Background(), "job_aaaaaaaaaaaaaaaa", taskA)
	if err != nil {
		t.Fatalf("Prepare through Manager A: %v", err)
	}
	taskB := domain.Task{ID: "task_bbbbbbbbbbbbbbbb", TargetRepositories: []string{"repo.primary"}}
	if _, err := managerB.Adopt(context.Background(), "job_bbbbbbbbbbbbbbbb", taskB, snapshot.WorktreePath); err == nil {
		t.Fatal("Manager B adopted a Worktree already owned through a different workspace root")
	}
	if err := managerA.Verify(context.Background(), "job_aaaaaaaaaaaaaaaa", taskA, snapshot); err != nil {
		t.Fatalf("Manager A lost its Worktree ownership after the rejected cross-root adoption: %v", err)
	}
}

func TestAcquireWorktreeIndexLockRecoversStaleTaxionLock(t *testing.T) {
	repository := initRepository(t)
	manager, err := NewManager(filepath.Join(canonicalTempDir(t), "worktrees"), []Repository{{ID: "repo.primary", Path: repository}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	task := domain.Task{ID: "task_caaaaaaaaaaaaaaa", TargetRepositories: []string{"repo.primary"}}
	snapshot, err := manager.Prepare(context.Background(), "job_caaaaaaaaaaaaaaa", task)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	indexPath := gitTestOutput(t, snapshot.WorktreePath, "rev-parse", "--git-path", "index")
	if !filepath.IsAbs(indexPath) {
		indexPath = filepath.Join(snapshot.WorktreePath, indexPath)
	}
	lockPath := indexPath + ".lock"
	if err := os.WriteFile(lockPath, []byte(taxionIndexLockMarker), 0o600); err != nil {
		t.Fatalf("write stale Taxion index lock: %v", err)
	}
	_, release, err := acquireWorktreeIndexLock(context.Background(), snapshot.WorktreePath)
	if err != nil {
		t.Fatalf("recover stale Taxion index lock: %v", err)
	}
	if err := release(); err != nil {
		t.Fatalf("release recovered index lock: %v", err)
	}
	if _, err := os.Stat(lockPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovered index lock still exists: %v", err)
	}
}

func TestAcquireWorktreeIndexLockPreservesForeignLock(t *testing.T) {
	repository := initRepository(t)
	manager, err := NewManager(filepath.Join(canonicalTempDir(t), "worktrees"), []Repository{{ID: "repo.primary", Path: repository}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	task := domain.Task{ID: "task_daaaaaaaaaaaaaaa", TargetRepositories: []string{"repo.primary"}}
	snapshot, err := manager.Prepare(context.Background(), "job_daaaaaaaaaaaaaaa", task)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	indexPath := gitTestOutput(t, snapshot.WorktreePath, "rev-parse", "--git-path", "index")
	if !filepath.IsAbs(indexPath) {
		indexPath = filepath.Join(snapshot.WorktreePath, indexPath)
	}
	lockPath := indexPath + ".lock"
	foreignLock := []byte("foreign Git index lock")
	if err := os.WriteFile(lockPath, foreignLock, 0o600); err != nil {
		t.Fatalf("write foreign Git index lock: %v", err)
	}
	if _, _, err := acquireWorktreeIndexLock(context.Background(), snapshot.WorktreePath); err == nil {
		t.Fatal("CaptureOutput accepted a foreign Git index lock")
	}
	actual, err := os.ReadFile(lockPath)
	if err != nil || string(actual) != string(foreignLock) {
		t.Fatalf("foreign Git index lock was modified: %q %v", actual, err)
	}
}

func TestCaptureOutputSupportsBracketInGitMetadataParentPath(t *testing.T) {
	repository := initRepository(t)
	bracketParent := filepath.Join(canonicalTempDir(t), "client[west")
	if err := os.MkdirAll(bracketParent, 0o700); err != nil {
		t.Fatalf("create bracket parent: %v", err)
	}
	movedRepository := filepath.Join(bracketParent, "repository")
	if err := os.Rename(repository, movedRepository); err != nil {
		t.Fatalf("move repository beneath bracket parent: %v", err)
	}
	gitTestRun(t, movedRepository, "config", "core.excludesFile", filepath.Join(movedRepository, "empty-global-excludes"))
	workspace := filepath.Join(canonicalTempDir(t), "managed-worktrees")
	manager, err := NewManager(workspace, []Repository{{ID: "repo.primary", Path: movedRepository}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	task := domain.Task{ID: "task_0123456789abcdef", TargetRepositories: []string{"repo.primary"}}
	snapshot, err := manager.Prepare(context.Background(), "job_0123456789abcdef", task)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := os.WriteFile(filepath.Join(snapshot.WorktreePath, "output.txt"), []byte("captured\n"), 0o600); err != nil {
		t.Fatalf("write output: %v", err)
	}
	gitTestRun(t, snapshot.WorktreePath, "add", "output.txt")
	gitTestRun(t, snapshot.WorktreePath, "commit", "-m", "capture output under bracket path")
	if _, err := manager.CaptureOutput(context.Background(), "job_0123456789abcdef", task, snapshot); err != nil {
		t.Fatalf("CaptureOutput with literal bracket parent path: %v", err)
	}
}

func TestOrphanAdoptionRecoveryUsesCurrentRegistryAndIgnoresAdjacentBaseline(t *testing.T) {
	repository := initRepository(t)
	workspaceA := filepath.Join(canonicalTempDir(t), "workspace-a")
	workspaceB := filepath.Join(canonicalTempDir(t), "workspace-b")
	if _, err := NewManager(workspaceA, []Repository{{ID: "repo.primary", Path: repository}}); err != nil {
		t.Fatalf("NewManager A: %v", err)
	}
	managerB, err := NewManager(workspaceB, []Repository{{ID: "repo.alias", Path: repository}})
	if err != nil {
		t.Fatalf("NewManager B: %v", err)
	}
	external := filepath.Join(canonicalTempDir(t), "external-worktree")
	gitTestRun(t, repository, "worktree", "add", "--detach", external, "HEAD")
	gitDir := gitTestOutput(t, external, "rev-parse", "--absolute-git-dir")
	gitDir, err = filepath.EvalSymlinks(gitDir)
	if err != nil {
		t.Fatalf("resolve external Git admin directory: %v", err)
	}
	reservation := adoptionReservation{
		JobID: "job_0123456789abcdef", TaskID: "task_0123456789abcdef",
		RepositoryID: "repo.primary", WorktreePath: external, GitDir: gitDir, WorkspaceRoot: workspaceA,
	}
	directory, err := managerB.adoptionReservationDirectory("repo.alias", true)
	if err != nil {
		t.Fatalf("resolve alias reservation registry: %v", err)
	}
	for _, path := range adoptionReservationPaths(directory, reservation) {
		if _, err := writeAdoptionReservation(path, reservation); err != nil {
			t.Fatalf("write orphan reservation: %v", err)
		}
	}
	if err := os.WriteFile(external+".baseline.json", []byte("not a Taxion baseline"), 0o600); err != nil {
		t.Fatalf("write unrelated adjacent baseline: %v", err)
	}
	if err := managerB.recoverOrphanedAdoptionReservations(reservation, directory); err != nil {
		t.Fatalf("recover reservation using current trusted registry alias: %v", err)
	}
	for _, path := range adoptionReservationPaths(directory, reservation) {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("orphan reservation remains: %s (%v)", path, err)
		}
	}
}
