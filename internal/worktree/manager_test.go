package worktree

import (
	"context"
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
	workspace := filepath.Join(t.TempDir(), "worktrees")
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
	missingRepository := filepath.Join(t.TempDir(), "missing-repository")
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
	workspace := filepath.Join(t.TempDir(), "worktrees")
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
	manager, err := NewManager(filepath.Join(t.TempDir(), "worktrees"), []Repository{{ID: "repo.primary", Path: repository}})
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
	manager, err := NewManager(filepath.Join(t.TempDir(), "worktrees"), []Repository{{ID: "repo.primary", Path: repository}})
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
	manager, err := NewManager(filepath.Join(t.TempDir(), "worktrees"), []Repository{{ID: "repo.primary", Path: repository}})
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
	manager, err := NewManager(filepath.Join(t.TempDir(), "worktrees"), []Repository{
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

func initRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitTestRun(t, root, "init")
	gitTestRun(t, root, "config", "user.name", "Taxion Test")
	gitTestRun(t, root, "config", "user.email", "taxion-test@example.invalid")
	gitTestRun(t, root, "config", "commit.gpgSign", "false")
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
	worktreePath := filepath.Join(t.TempDir(), "existing-worktree")
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

	manager, err := NewManager(filepath.Join(t.TempDir(), "managed-worktrees"), []Repository{{ID: "repo.primary", Path: repository}})
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
	if _, err := manager.Adopt(context.Background(), "job_abcdef0123456789", task, worktreePath); err == nil {
		t.Fatal("the same physical Worktree was adopted by a second Job")
	}
}

func TestManagerAdoptsBranchAttachedWorktreeAndPreservesBranchIdentity(t *testing.T) {
	repository := initRepository(t)
	worktreePath := filepath.Join(t.TempDir(), "branch-worktree")
	gitTestRun(t, repository, "worktree", "add", worktreePath, "-b", "adopted-feature")
	manager, err := NewManager(filepath.Join(t.TempDir(), "managed-worktrees"), []Repository{{ID: "repo.primary", Path: repository}})
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
	worktreePath := filepath.Join(t.TempDir(), "reserved-worktree")
	gitTestRun(t, repository, "worktree", "add", "--detach", worktreePath, "HEAD")
	manager, err := NewManager(filepath.Join(t.TempDir(), "managed-worktrees"), []Repository{{ID: "repo.primary", Path: repository}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	task := domain.Task{ID: "task_0123456789abcdef", TargetRepositories: []string{"repo.primary"}}
	snapshot, err := manager.Adopt(context.Background(), "job_0123456789abcdef", task, worktreePath)
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	reservationDirectory := filepath.Join(manager.root, ".adoptions")
	entries, err := os.ReadDir(reservationDirectory)
	if err != nil || len(entries) != 2 {
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
	worktreeA := filepath.Join(t.TempDir(), "adoption-a")
	worktreeB := filepath.Join(t.TempDir(), "adoption-b")
	gitTestRun(t, repository, "worktree", "add", "--detach", worktreeA, "HEAD")
	gitTestRun(t, repository, "worktree", "add", "--detach", worktreeB, "HEAD")
	manager, err := NewManager(filepath.Join(t.TempDir(), "managed-worktrees"), []Repository{{ID: "repo.primary", Path: repository}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
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
		snapshot, err := manager.Adopt(context.Background(), jobID, task, worktreeB)
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
	reservationDirectory := filepath.Join(manager.root, ".adoptions")
	entries, err := os.ReadDir(reservationDirectory)
	if err != nil || len(entries) != 2 {
		t.Fatalf("losing Task reservation was not cleaned up: entries=%d err=%v", len(entries), err)
	}
	movedPath := filepath.Join(t.TempDir(), "adoption-moved")
	gitTestRun(t, repository, "worktree", "move", winner.snapshot.WorktreePath, movedPath)
	otherTask := domain.Task{ID: "task_2222222222222222", TargetRepositories: []string{"repo.primary"}}
	if _, err := manager.Adopt(context.Background(), "job_2222222222222222", otherTask, movedPath); err == nil {
		t.Fatal("the same Git admin directory was adopted after its Worktree path changed")
	}
	entries, err = os.ReadDir(reservationDirectory)
	if err != nil || len(entries) != 2 {
		t.Fatalf("losing Git admin directory reservation was not cleaned up: entries=%d err=%v", len(entries), err)
	}
}

func TestManagerCaptureOutputRejectsDirtySubmodule(t *testing.T) {
	repository := initRepository(t)
	submodule := initRepository(t)
	gitTestRun(t, repository, "-c", "protocol.file.allow=always", "submodule", "add", submodule, "nested")
	gitTestRun(t, repository, "commit", "-m", "add submodule")
	manager, err := NewManager(filepath.Join(t.TempDir(), "managed-worktrees"), []Repository{{ID: "repo.primary", Path: repository}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	task := domain.Task{ID: "task_3333333333333333", TargetRepositories: []string{"repo.primary"}}
	snapshot, err := manager.Prepare(context.Background(), "job_3333333333333333", task)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := os.WriteFile(filepath.Join(snapshot.WorktreePath, "nested", "untracked.txt"), []byte("dirty submodule\n"), 0o600); err != nil {
		t.Fatalf("write dirty submodule file: %v", err)
	}
	if _, err := manager.CaptureOutput(context.Background(), "job_3333333333333333", task, snapshot); err == nil ||
		!strings.Contains(err.Error(), "clean") {
		t.Fatalf("CaptureOutput accepted a dirty submodule: %v", err)
	}
}

func TestWriteExclusiveAtomicDoesNotOverwriteExistingRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "record.json")
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

