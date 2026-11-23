package worktree

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

func (m *Manager) Prepare(ctx context.Context, jobID string, task domain.Task) (Snapshot, error) {
	if m == nil || ctx == nil || !validPathIdentity(jobID) || !validPathIdentity(task.ID) {
		return Snapshot{}, errors.New("Worktree manager, context, Job ID, and Task ID are required")
	}
	if len(task.TargetRepositories) != 1 {
		return Snapshot{}, errors.New("Worktree provisioning requires exactly one approved target repository")
	}
	repositoryID := task.TargetRepositories[0]
	repositoryPath, trusted := m.repositories[repositoryID]
	if !trusted {
		return Snapshot{}, errors.New("Task repository is not in the trusted repository registry")
	}
	baseline, err := gitOutput(ctx, repositoryPath, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || !validCommit(baseline) {
		return Snapshot{}, errors.New("trusted repository HEAD is not a verifiable commit")
	}
	key := sha256.Sum256([]byte(jobID + "\x00" + task.ID))
	worktreePath := filepath.Join(m.root, hex.EncodeToString(key[:]))
	snapshot := Snapshot{
		RepositoryID: repositoryID, RepositoryPath: repositoryPath,
		WorktreePath: worktreePath, BaselineCommit: baseline,
	}
	if _, err := os.Lstat(worktreePath); err == nil {
		if err := m.Verify(ctx, jobID, task, snapshot); err != nil {
			return Snapshot{}, fmt.Errorf("existing Task Worktree does not match its immutable baseline: %w", err)
		}
		return snapshot, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Snapshot{}, fmt.Errorf("inspect Task Worktree path: %w", err)
	}
	if err := os.MkdirAll(m.root, 0o700); err != nil {
		return Snapshot{}, fmt.Errorf("create Worktree workspace root: %w", err)
	}
	if err := runGit(ctx, repositoryPath, "worktree", "add", "--detach", worktreePath, baseline); err != nil {
		// Git serializes updates to the shared repository metadata. A concurrent
		// provisioner may have completed this exact deterministic Worktree first.
		if verifyErr := m.Verify(ctx, jobID, task, snapshot); verifyErr == nil {
			return snapshot, nil
		}
		return Snapshot{}, fmt.Errorf("create detached Task Worktree: %w", err)
	}
	if err := m.Verify(ctx, jobID, task, snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("verify newly created Task Worktree: %w", err)
	}
	return snapshot, nil
}

func (m *Manager) Verify(ctx context.Context, jobID string, task domain.Task, snapshot Snapshot) error {
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
	expectedPath := filepath.Join(m.root, hex.EncodeToString(key[:]))
	if filepath.Clean(snapshot.WorktreePath) != expectedPath || !validCommit(snapshot.BaselineCommit) {
		return errors.New("Worktree path or baseline commit is invalid")
	}
	resolved, err := filepath.EvalSymlinks(snapshot.WorktreePath)
	if err != nil || filepath.Clean(resolved) != expectedPath {
		return errors.New("Worktree path is missing or traverses a symbolic link")
	}
	top, err := gitOutput(ctx, expectedPath, "rev-parse", "--show-toplevel")
	if err != nil || filepath.Clean(top) != expectedPath {
		return errors.New("Task path is not the expected Git Worktree")
	}
	head, err := gitOutput(ctx, expectedPath, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || !validCommit(head) {
		return errors.New("Task Worktree HEAD is not a verifiable commit")
	}
	if err := runGit(ctx, expectedPath, "merge-base", "--is-ancestor", snapshot.BaselineCommit, head); err != nil {
		return errors.New("Task Worktree HEAD no longer descends from its immutable baseline")
	}
	if _, err := gitOutput(ctx, expectedPath, "symbolic-ref", "-q", "HEAD"); err == nil {
		return errors.New("Task Worktree must remain detached")
	}
	gitDir, err := gitOutput(ctx, expectedPath, "rev-parse", "--absolute-git-dir")
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
	return nil
}

// CaptureOutput fixes the exact committed Git tree produced in a Task Worktree.
// It refuses snapshots while staged, unstaged, or untracked changes remain.
func (m *Manager) CaptureOutput(ctx context.Context, jobID string, task domain.Task, snapshot Snapshot) (OutputSnapshot, error) {
	if err := m.Verify(ctx, jobID, task, snapshot); err != nil {
		return OutputSnapshot{}, fmt.Errorf("verify Task Worktree before output capture: %w", err)
	}
	status, err := gitOutput(ctx, snapshot.WorktreePath, "status", "--porcelain", "--untracked-files=all")
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
	tree, err := gitOutput(ctx, snapshot.WorktreePath, "rev-parse", "--verify", commit+"^{tree}")
	if err != nil || !validCommit(tree) {
		return OutputSnapshot{}, errors.New("Task output tree identity is invalid")
	}
	return OutputSnapshot{CommitSHA: commit, TreeSHA: tree}, nil
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
	command := exec.CommandContext(ctx, "git", append([]string{"-C", directory}, args...)...)
	output, err := command.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func runGit(ctx context.Context, directory string, args ...string) error {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", directory}, args...)...)
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
