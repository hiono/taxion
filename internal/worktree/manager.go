package worktree

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/hiono/taxion/internal/domain"
)

type Repository struct {
	ID   string
	Path string
}

type Snapshot = domain.WorktreeSnapshot

type OutputSnapshot struct {
	CommitSHA string
	TreeSHA   string
}

type Manager struct {
	root         string
	repositories map[string]string
}

func NewManager(workspaceRoot string, repositories []Repository) (*Manager, error) {
	if strings.TrimSpace(workspaceRoot) == "" || len(repositories) == 0 {
		return nil, errors.New("workspace root and at least one trusted repository are required")
	}
	root, err := canonicalDirectory(workspaceRoot, true)
	if err != nil {
		return nil, fmt.Errorf("resolve Worktree workspace root: %w", err)
	}
	known := make(map[string]string, len(repositories))
	for _, repository := range repositories {
		if strings.TrimSpace(repository.ID) == "" {
			return nil, errors.New("trusted repository identity is required")
		}
		if _, exists := known[repository.ID]; exists {
			return nil, errors.New("trusted repository identities must be unique")
		}
		path, err := canonicalDirectory(repository.Path, false)
		if err != nil {
			return nil, fmt.Errorf("resolve trusted repository %q: %w", repository.ID, err)
		}
		top, err := gitOutput(context.Background(), path, "rev-parse", "--show-toplevel")
		if err != nil || filepath.Clean(top) != path {
			return nil, fmt.Errorf("trusted repository %q is not a Git working tree", repository.ID)
		}
		if path == root || pathWithin(path, root) || pathWithin(root, path) {
			return nil, errors.New("Worktree workspace root and trusted repository must not contain one another")
		}
		known[repository.ID] = path
	}
	return &Manager{root: root, repositories: known}, nil
}

func (m *Manager) lock(ctx context.Context) (func(), error) {
	lock, err := acquireAdoptionFileLock(ctx, filepath.Join(m.root, ".adoptions.lock"))
	if err != nil {
		return nil, fmt.Errorf("lock Worktree registry: %w", err)
	}
	return lock.release, nil
}

func (m *Manager) Prepare(ctx context.Context, jobID string, task domain.Task) (Snapshot, error) {
	if m == nil || ctx == nil || !validPathIdentity(jobID) || !validPathIdentity(task.ID) {
		return Snapshot{}, errors.New("Worktree manager, context, Job ID, and Task ID are required")
	}
	unlock, err := m.lock(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer unlock()
	if len(task.TargetRepositories) != 1 {
		return Snapshot{}, errors.New("Worktree provisioning requires exactly one approved target repository")
	}
	repositoryID := task.TargetRepositories[0]
	repositoryPath, trusted := m.repositories[repositoryID]
	if !trusted {
		return Snapshot{}, errors.New("Task repository is not in the trusted repository registry")
	}
	key := sha256.Sum256([]byte(jobID + "\x00" + task.ID))
	worktreePath := filepath.Join(m.root, hex.EncodeToString(key[:]))
	if err := os.MkdirAll(m.root, 0o700); err != nil {
		return Snapshot{}, fmt.Errorf("create Worktree workspace root: %w", err)
	}
	_, worktreeErr := os.Lstat(worktreePath)
	worktreeExists := worktreeErr == nil
	if worktreeErr != nil && !errors.Is(worktreeErr, os.ErrNotExist) {
		return Snapshot{}, fmt.Errorf("inspect Task Worktree path: %w", worktreeErr)
	}
	baselinePath := worktreePath + ".baseline.json"
	record, recordErr := readBaselineRecord(baselinePath)
	baselineCreated := false
	var baseline string
	if recordErr == nil {
		if err := validateBaselineRecord(record, jobID, task.ID, repositoryID, repositoryPath); err != nil {
			return Snapshot{}, err
		}
		if record.Origin == domain.WorktreeOriginAdopted {
			return Snapshot{}, errors.New("adopted Worktree must be reopened through Adopt; Prepare cannot provision a second managed Worktree")
		}
		baseline = record.BaselineCommit
	} else if errors.Is(recordErr, os.ErrNotExist) {
		if worktreeExists {
			return Snapshot{}, errors.New("existing Task Worktree has no persisted immutable baseline")
		}
		resolvedBaseline, err := gitOutput(ctx, repositoryPath, "rev-parse", "--verify", "HEAD^{commit}")
		if err != nil || !validCommit(resolvedBaseline) {
			return Snapshot{}, errors.New("trusted repository HEAD is not a verifiable commit")
		}
		baseline = resolvedBaseline
		record = baselineRecord{
			Version: 2, JobID: jobID, TaskID: task.ID,
			RepositoryID: repositoryID, RepositoryPath: repositoryPath,
			WorktreePath: worktreePath, BaselineCommit: baseline,
			Origin:             domain.WorktreeOriginTaxionCreated,
			InitialStateSHA256: cleanWorktreeStateSHA256(),
			HeadMode:           domain.WorktreeHeadDetached,
		}
		if err := writeBaselineRecord(baselinePath, record); err != nil {
			if !errors.Is(err, os.ErrExist) {
				return Snapshot{}, fmt.Errorf("persist immutable Task Worktree baseline: %w", err)
			}
			record, err = readBaselineRecord(baselinePath)
			if err != nil {
				return Snapshot{}, fmt.Errorf("read concurrently persisted Task Worktree baseline: %w", err)
			}
			if err := validateBaselineRecord(record, jobID, task.ID, repositoryID, repositoryPath); err != nil {
				return Snapshot{}, err
			}
			baseline = record.BaselineCommit
		} else {
			baselineCreated = true
		}
	} else {
		return Snapshot{}, fmt.Errorf("read immutable Task Worktree baseline: %w", recordErr)
	}
	snapshot := Snapshot{
		RepositoryID: repositoryID, RepositoryPath: repositoryPath,
		WorktreePath: worktreePath, BaselineCommit: baseline,
		Origin: record.Origin, InitialStateSHA256: record.InitialStateSHA256,
		HeadMode: record.HeadMode, HeadRef: record.HeadRef,
	}
	if worktreeExists {
		if _, err := m.reserveWorktreeOwnership(ctx, jobID, task.ID, repositoryID, worktreePath); err != nil {
			return Snapshot{}, fmt.Errorf("reserve existing Task Worktree ownership: %w", err)
		}
		if err := m.verify(ctx, jobID, task, snapshot); err != nil {
			return Snapshot{}, fmt.Errorf("existing Task Worktree does not match its immutable baseline: %w", err)
		}
		return snapshot, nil
	}
	if err := runGit(ctx, repositoryPath, "worktree", "add", "--detach", worktreePath, baseline); err != nil {
		cleanupErr := m.cleanupFailedPrepare(ctx, repositoryPath, worktreePath, baselinePath, baselineCreated, nil)
		return Snapshot{}, errors.Join(fmt.Errorf("create detached Task Worktree: %w", err), cleanupErr)
	}
	reservation, err := m.reserveWorktreeOwnership(ctx, jobID, task.ID, repositoryID, worktreePath)
	if err != nil {
		cleanupErr := m.cleanupFailedPrepare(ctx, repositoryPath, worktreePath, baselinePath, baselineCreated, nil)
		return Snapshot{}, errors.Join(fmt.Errorf("reserve new Task Worktree ownership: %w", err), cleanupErr)
	}
	if err := m.verify(ctx, jobID, task, snapshot); err != nil {
		cleanupErr := m.cleanupFailedPrepare(ctx, repositoryPath, worktreePath, baselinePath, baselineCreated, &reservation)
		return Snapshot{}, errors.Join(fmt.Errorf("verify newly created Task Worktree: %w", err), cleanupErr)
	}
	if err := verifyCleanInitialWorktree(ctx, worktreePath, baseline); err != nil {
		cleanupErr := m.cleanupFailedPrepare(ctx, repositoryPath, worktreePath, baselinePath, baselineCreated, &reservation)
		return Snapshot{}, errors.Join(fmt.Errorf("new Task Worktree is not clean after checkout hooks: %w", err), cleanupErr)
	}
	return snapshot, nil
}

func (m *Manager) reserveWorktreeOwnership(
	ctx context.Context, jobID, taskID, repositoryID, worktreePath string,
) (adoptionReservation, error) {
	gitDir, err := gitOutput(ctx, worktreePath, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return adoptionReservation{}, errors.New("Task Worktree has no linked Git admin directory")
	}
	resolvedGitDir, err := filepath.EvalSymlinks(gitDir)
	if err != nil {
		return adoptionReservation{}, errors.New("Task Worktree Git admin directory cannot be resolved")
	}
	repositoryPath, trusted := m.repositories[repositoryID]
	if !trusted {
		return adoptionReservation{}, errors.New("Task Worktree repository is not trusted")
	}
	commonGitDir, err := gitOutput(ctx, repositoryPath, "rev-parse", "--git-common-dir")
	if err != nil {
		return adoptionReservation{}, errors.New("trusted repository common Git metadata cannot be resolved")
	}
	if !filepath.IsAbs(commonGitDir) {
		commonGitDir = filepath.Join(repositoryPath, commonGitDir)
	}
	commonGitDir, err = filepath.EvalSymlinks(commonGitDir)
	if err != nil || !pathWithin(filepath.Join(commonGitDir, "worktrees"), resolvedGitDir) {
		return adoptionReservation{}, errors.New("Task Worktree Git admin directory is outside the trusted repository")
	}
	reservation := adoptionReservation{
		JobID: jobID, TaskID: taskID, RepositoryID: repositoryID,
		WorktreePath: worktreePath, GitDir: resolvedGitDir,
	}
	if err := m.reserveAdoption(jobID, taskID, repositoryID, worktreePath, resolvedGitDir); err != nil {
		return adoptionReservation{}, err
	}
	return reservation, nil
}

func (m *Manager) cleanupFailedPrepare(
	ctx context.Context, repositoryPath, worktreePath, baselinePath string,
	baselineCreated bool, reservation *adoptionReservation,
) error {
	var cleanupErr error
	if _, err := os.Lstat(worktreePath); err == nil {
		cleanupErr = runGit(ctx, repositoryPath, "worktree", "remove", "--force", worktreePath)
		if cleanupErr != nil {
			cleanupErr = fmt.Errorf("remove failed Task Worktree: %w", cleanupErr)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		cleanupErr = fmt.Errorf("inspect failed Task Worktree before cleanup: %w", err)
	}
	if baselineCreated {
		if err := os.Remove(baselinePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove failed Task Worktree baseline: %w", err))
		} else if err := syncDirectory(filepath.Dir(baselinePath)); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("sync failed Task Worktree baseline cleanup: %w", err))
		}
		if reservation != nil {
			cleanupErr = errors.Join(cleanupErr, m.removeOrphanedAdoptionReservation(*reservation))
		}
	}
	return cleanupErr
}

func verifyCleanInitialWorktree(ctx context.Context, worktreePath, baseline string) error {
	head, err := gitOutput(ctx, worktreePath, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || head != baseline {
		return errors.New("new Task Worktree HEAD differs from its immutable baseline")
	}
	if err := verifyWorktreeIndexFlags(ctx, worktreePath); err != nil {
		return err
	}
	status, err := gitOutput(
		ctx, worktreePath, "--no-optional-locks", "status", "--porcelain=v2",
		"--untracked-files=all", "--ignore-submodules=none",
	)
	if err != nil {
		return fmt.Errorf("inspect new Task Worktree after checkout hooks: %w", err)
	}
	if status != "" {
		return errors.New("new Task Worktree contains checkout-hook modifications")
	}
	submoduleStatus, err := gitOutput(ctx, worktreePath, "--no-optional-locks", "submodule", "status", "--recursive")
	if err != nil {
		return fmt.Errorf("inspect new Task Worktree submodules: %w", err)
	}
	for _, line := range strings.Split(submoduleStatus, "\n") {
		if strings.HasPrefix(line, "-") || strings.HasPrefix(line, "U") {
			return errors.New("new Task Worktree contains an uninitialized or conflicted submodule")
		}
	}
	dirtySubmodules, err := gitBytes(
		ctx, worktreePath, "--no-optional-locks", "submodule", "foreach", "--quiet", "--recursive",
		"git status --porcelain=v2 --untracked-files=all --ignore-submodules=none",
	)
	if err != nil {
		return fmt.Errorf("inspect new Task Worktree submodule state: %w", err)
	}
	if len(dirtySubmodules) != 0 {
		return errors.New("new Task Worktree contains checkout-hook modifications in a submodule")
	}
	return nil
}

func (m *Manager) Verify(ctx context.Context, jobID string, task domain.Task, snapshot Snapshot) error {
	if m == nil || ctx == nil {
		return errors.New("Worktree manager and context are required")
	}
	unlock, err := m.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	if snapshot.Origin == domain.WorktreeOriginTaxionCreated {
		if _, err := m.reserveWorktreeOwnership(
			ctx, jobID, task.ID, snapshot.RepositoryID, snapshot.WorktreePath,
		); err != nil {
			return fmt.Errorf("reserve verified Task Worktree ownership: %w", err)
		}
	}
	return m.verify(ctx, jobID, task, snapshot)
}

func (m *Manager) verify(ctx context.Context, jobID string, task domain.Task, snapshot Snapshot) error {
	if m == nil || ctx == nil || !validPathIdentity(jobID) || !validPathIdentity(task.ID) {
		return errors.New("Worktree manager, context, Job ID, and Task ID are required")
	}
	if len(task.TargetRepositories) != 1 || task.TargetRepositories[0] != snapshot.RepositoryID {
		return errors.New("Worktree repository is outside the approved Task scope")
	}
	repositoryPath, trusted := m.repositories[snapshot.RepositoryID]
	if !trusted || repositoryPath != snapshot.RepositoryPath {
		return errors.New("Worktree repository identity or trusted path mismatch")
	}
	key := sha256.Sum256([]byte(jobID + "\x00" + task.ID))
	registrationPath := filepath.Join(m.root, hex.EncodeToString(key[:]))
	worktreePath := snapshot.WorktreePath
	if worktreePath == "" || !filepath.IsAbs(worktreePath) || filepath.Clean(worktreePath) != worktreePath ||
		!validCommit(snapshot.BaselineCommit) {
		return errors.New("Worktree path or baseline commit is invalid")
	}
	record, err := readBaselineRecord(registrationPath + ".baseline.json")
	if err != nil {
		return fmt.Errorf("read immutable Task Worktree baseline: %w", err)
	}
	if err := validateBaselineRecord(record, jobID, task.ID, snapshot.RepositoryID, repositoryPath); err != nil {
		return err
	}
	if record.BaselineCommit != snapshot.BaselineCommit {
		return errors.New("Worktree snapshot baseline differs from its persisted immutable baseline")
	}
	origin := snapshot.Origin
	if origin == "" {
		origin = record.Origin
	}
	if origin == "" {
		origin = domain.WorktreeOriginTaxionCreated
	}
	if record.WorktreePath != "" && record.WorktreePath != worktreePath {
		return errors.New("Worktree path differs from its persisted registration")
	}
	if record.Origin != "" && record.Origin != origin {
		return errors.New("Worktree provenance differs from its persisted registration")
	}
	if record.InitialStateSHA256 != "" && snapshot.InitialStateSHA256 != "" &&
		record.InitialStateSHA256 != snapshot.InitialStateSHA256 {
		return errors.New("Worktree initial state digest differs from its persisted registration")
	}
	if record.HeadMode != "" && snapshot.HeadMode != "" &&
		(record.HeadMode != snapshot.HeadMode || record.HeadRef != snapshot.HeadRef) {
		return errors.New("Worktree HEAD mode or reference differs from its persisted registration")
	}
	if origin != domain.WorktreeOriginTaxionCreated && origin != domain.WorktreeOriginAdopted {
		return errors.New("Worktree provenance is invalid")
	}
	if origin == domain.WorktreeOriginTaxionCreated && worktreePath != registrationPath {
		return errors.New("Taxion-created Worktree path differs from its deterministic registration path")
	}
	if origin == domain.WorktreeOriginAdopted &&
		(worktreePath == registrationPath || pathWithin(m.root, worktreePath) ||
			pathWithin(worktreePath, m.root) || pathWithin(repositoryPath, worktreePath) ||
			pathWithin(worktreePath, repositoryPath)) {
		return errors.New("adopted Worktree path overlaps the managed workspace or source repository")
	}
	resolved, err := filepath.EvalSymlinks(worktreePath)
	if err != nil || filepath.Clean(resolved) != worktreePath {
		return errors.New("Worktree path is missing or traverses a symbolic link")
	}
	top, err := gitOutput(ctx, worktreePath, "rev-parse", "--show-toplevel")
	if err != nil || filepath.Clean(top) != worktreePath {
		return errors.New("Task path is not the registered Git Worktree")
	}
	head, err := gitOutput(ctx, worktreePath, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || !validCommit(head) {
		return errors.New("Task Worktree HEAD is not a verifiable commit")
	}
	if err := runGit(ctx, worktreePath, "merge-base", "--is-ancestor", snapshot.BaselineCommit, head); err != nil {
		return errors.New("Task Worktree HEAD no longer descends from its immutable baseline")
	}
	if snapshot.HeadMode == domain.WorktreeHeadBranchAttached || record.HeadMode == domain.WorktreeHeadBranchAttached {
		expectedRef := snapshot.HeadRef
		if expectedRef == "" {
			expectedRef = record.HeadRef
		}
		actualRef, err := gitOutput(ctx, worktreePath, "symbolic-ref", "-q", "HEAD")
		if err != nil || actualRef != expectedRef || strings.TrimSpace(expectedRef) == "" {
			return errors.New("Task Worktree branch reference changed after registration")
		}
	} else {
		_, detachedErr := gitOutput(ctx, worktreePath, "symbolic-ref", "-q", "HEAD")
		if detachedErr == nil {
			return errors.New("Task Worktree must remain detached")
		}
		if !isDetachedHeadProbeError(detachedErr) {
			return fmt.Errorf("verify Task Worktree detached HEAD: %w", detachedErr)
		}
	}
	gitDir, err := gitOutput(ctx, worktreePath, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return errors.New("Task path has no linked Git Worktree metadata")
	}
	resolvedGitDir, err := filepath.EvalSymlinks(gitDir)
	if err != nil {
		return errors.New("Task Worktree metadata cannot be resolved")
	}
	commonGitDir, err := gitOutput(ctx, repositoryPath, "rev-parse", "--git-common-dir")
	if err != nil {
		return errors.New("trusted repository common Git metadata cannot be resolved")
	}
	if !filepath.IsAbs(commonGitDir) {
		commonGitDir = filepath.Join(repositoryPath, commonGitDir)
	}
	commonGitDir, err = filepath.EvalSymlinks(commonGitDir)
	if err != nil {
		return errors.New("trusted repository common Git metadata path cannot be resolved")
	}
	expectedGitDir := filepath.Join(commonGitDir, "worktrees")
	if !pathWithin(expectedGitDir, resolvedGitDir) {
		return errors.New("Task path is not registered as a linked Worktree of the trusted repository")
	}
	if origin == domain.WorktreeOriginAdopted {
		if err := m.verifyAdoptionReservation(jobID, task.ID, snapshot.RepositoryID, worktreePath, resolvedGitDir); err != nil {
			return err
		}
	}
	return nil
}

// CaptureOutput fixes the exact committed Git tree produced in a Task Worktree.
// It refuses snapshots while staged, unstaged, or untracked changes remain.
func (m *Manager) CaptureOutput(ctx context.Context, jobID string, task domain.Task, snapshot Snapshot) (OutputSnapshot, error) {
	if m == nil || ctx == nil {
		return OutputSnapshot{}, errors.New("Worktree manager and context are required")
	}
	unlock, err := m.lock(ctx)
	if err != nil {
		return OutputSnapshot{}, err
	}
	defer unlock()
	if err := m.verify(ctx, jobID, task, snapshot); err != nil {
		return OutputSnapshot{}, fmt.Errorf("verify Task Worktree before output capture: %w", err)
	}
	indexEntries, err := gitOutput(ctx, snapshot.WorktreePath, "--no-optional-locks", "ls-files", "-v", "-z")
	if err != nil {
		return OutputSnapshot{}, errors.New("inspect Task Worktree index flags before output capture")
	}
	for _, entry := range strings.Split(indexEntries, "\x00") {
		if len(entry) == 0 {
			continue
		}
		switch entry[0] {
		case 'h':
			return OutputSnapshot{}, errors.New("Task Worktree index contains assume-unchanged entries")
		case 'S':
			return OutputSnapshot{}, errors.New("Task Worktree index contains skip-worktree entries")
		case 's':
			return OutputSnapshot{}, errors.New("Task Worktree index contains skip-worktree and assume-unchanged entries")
		}
	}
	status, err := gitOutput(ctx, snapshot.WorktreePath, "--no-optional-locks", "status", "--porcelain", "--untracked-files=all", "--ignore-submodules=none")
	if err != nil {
		return OutputSnapshot{}, errors.New("inspect Task Worktree state before output capture")
	}
	if status != "" {
		return OutputSnapshot{}, errors.New("Task Worktree must be clean before output capture")
	}
	commit, err := gitOutput(ctx, snapshot.WorktreePath, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || !validCommit(commit) {
		return OutputSnapshot{}, errors.New("Task output commit identity is invalid")
	}
	if err := runGit(ctx, snapshot.WorktreePath, "merge-base", "--is-ancestor", snapshot.BaselineCommit, commit); err != nil {
		return OutputSnapshot{}, errors.New("Task output commit does not descend from the approved Task baseline")
	}
	tree, err := gitOutput(ctx, snapshot.WorktreePath, "rev-parse", "--verify", commit+"^{tree}")
	if err != nil || !validCommit(tree) {
		return OutputSnapshot{}, errors.New("Task output tree identity is invalid")
	}
	headAfter, err := gitOutput(ctx, snapshot.WorktreePath, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || headAfter != commit {
		return OutputSnapshot{}, errors.New("Task Worktree HEAD changed during output capture")
	}
	statusAfter, err := gitOutput(ctx, snapshot.WorktreePath, "--no-optional-locks", "status", "--porcelain", "--untracked-files=all", "--ignore-submodules=none")
	if err != nil || statusAfter != "" {
		return OutputSnapshot{}, errors.New("Task Worktree changed during output capture")
	}
	return OutputSnapshot{CommitSHA: commit, TreeSHA: tree}, nil
}

type baselineRecord struct {
	Version            int    `json:"version"`
	JobID              string `json:"job_id"`
	TaskID             string `json:"task_id"`
	RepositoryID       string `json:"repository_id"`
	RepositoryPath     string `json:"repository_path"`
	WorktreePath       string `json:"worktree_path,omitempty"`
	BaselineCommit     string `json:"baseline_commit"`
	Origin             string `json:"origin,omitempty"`
	InitialStateSHA256 string `json:"initial_state_sha256,omitempty"`
	HeadMode           string `json:"head_mode,omitempty"`
	HeadRef            string `json:"head_ref,omitempty"`
}

func readBaselineRecord(path string) (baselineRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return baselineRecord{}, err
	}
	var record baselineRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return baselineRecord{}, fmt.Errorf("decode baseline record: %w", err)
	}
	if (record.Version != 1 && record.Version != 2) || !validPathIdentity(record.JobID) || !validPathIdentity(record.TaskID) ||
		strings.TrimSpace(record.RepositoryID) == "" || strings.TrimSpace(record.RepositoryPath) == "" ||
		!validCommit(record.BaselineCommit) {
		return baselineRecord{}, errors.New("immutable Task Worktree baseline record is invalid")
	}
	if record.Version == 2 &&
		(!filepath.IsAbs(record.WorktreePath) || filepath.Clean(record.WorktreePath) != record.WorktreePath ||
			(record.Origin != domain.WorktreeOriginTaxionCreated && record.Origin != domain.WorktreeOriginAdopted) ||
			!validCommit(record.InitialStateSHA256) ||
			(record.HeadMode != domain.WorktreeHeadDetached && record.HeadMode != domain.WorktreeHeadBranchAttached) ||
			(record.HeadMode == domain.WorktreeHeadBranchAttached && strings.TrimSpace(record.HeadRef) == "") ||
			(record.HeadMode == domain.WorktreeHeadDetached && record.HeadRef != "")) {
		return baselineRecord{}, errors.New("immutable Task Worktree registration identity is invalid")
	}
	return record, nil
}

func validateBaselineRecord(record baselineRecord, jobID, taskID, repositoryID, repositoryPath string) error {
	if record.JobID != jobID || record.TaskID != taskID ||
		record.RepositoryID != repositoryID || record.RepositoryPath != repositoryPath {
		return errors.New("immutable Task Worktree baseline record belongs to another Job, Task, or repository")
	}
	return nil
}

func writeBaselineRecord(path string, record baselineRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode baseline record: %w", err)
	}
	if err := writeExclusiveAtomic(path, data, 0o600); err != nil {
		return fmt.Errorf("persist baseline record atomically: %w", err)
	}
	return nil
}

func canonicalDirectory(path string, create bool) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("directory path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if create {
		if err := os.MkdirAll(absolute, 0o700); err != nil {
			return "", err
		}
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("directory must be a real directory, not a symbolic link")
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func gitOutput(ctx context.Context, directory string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"--no-replace-objects", "-C", directory}, args...)...)
	output, err := command.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func runGit(ctx context.Context, directory string, args ...string) error {
	command := exec.CommandContext(ctx, "git", append([]string{"--no-replace-objects", "-C", directory}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func pathWithin(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != "." && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func validPathIdentity(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') &&
			!(r >= '0' && r <= '9') && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

func validCommit(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func isDetachedHeadProbeError(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == 1
}
