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

// Adopt registers an existing linked Git Worktree without changing its files,
// index, HEAD, or branch reference.
func (m *Manager) Adopt(ctx context.Context, jobID string, task domain.Task, worktreePath string) (Snapshot, error) {
	if m == nil || ctx == nil || !validPathIdentity(jobID) || !validPathIdentity(task.ID) {
		return Snapshot{}, errors.New("Worktree manager, context, Job ID, and Task ID are required")
	}
	if len(task.TargetRepositories) != 1 {
		return Snapshot{}, errors.New("Worktree adoption requires exactly one approved target repository")
	}
	repositoryID := task.TargetRepositories[0]
	repositoryPath, trusted := m.repositories[repositoryID]
	if !trusted {
		return Snapshot{}, errors.New("Task repository is not in the trusted repository registry")
	}
	if !filepath.IsAbs(worktreePath) || filepath.Clean(worktreePath) != worktreePath {
		return Snapshot{}, errors.New("adopted Worktree path must be absolute and canonical")
	}
	canonical, err := canonicalDirectory(worktreePath, false)
	if err != nil || canonical != worktreePath {
		return Snapshot{}, errors.New("adopted Worktree path is missing, non-canonical, or traverses a symbolic link")
	}
	if worktreePath == repositoryPath || pathWithin(repositoryPath, worktreePath) ||
		pathWithin(worktreePath, repositoryPath) || pathWithin(m.root, worktreePath) {
		return Snapshot{}, errors.New("adopted Worktree path overlaps the trusted repository or managed workspace")
	}
	top, err := gitOutput(ctx, worktreePath, "rev-parse", "--show-toplevel")
	if err != nil || filepath.Clean(top) != worktreePath {
		return Snapshot{}, errors.New("adopted path is not a Git Worktree root")
	}
	gitDir, err := gitOutput(ctx, worktreePath, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return Snapshot{}, errors.New("adopted path has no linked Git Worktree metadata")
	}
	resolvedGitDir, err := filepath.EvalSymlinks(gitDir)
	if err != nil {
		return Snapshot{}, errors.New("adopted Worktree metadata cannot be resolved")
	}
	commonGitDir, err := gitOutput(ctx, repositoryPath, "rev-parse", "--git-common-dir")
	if err != nil {
		return Snapshot{}, errors.New("trusted repository common Git metadata cannot be resolved")
	}
	if !filepath.IsAbs(commonGitDir) {
		commonGitDir = filepath.Join(repositoryPath, commonGitDir)
	}
	commonGitDir, err = filepath.EvalSymlinks(commonGitDir)
	if err != nil || !pathWithin(filepath.Join(commonGitDir, "worktrees"), resolvedGitDir) {
		return Snapshot{}, errors.New("adopted Worktree is not linked to the approved trusted repository")
	}
	head, err := gitOutput(ctx, worktreePath, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || !validCommit(head) {
		return Snapshot{}, errors.New("adopted Worktree HEAD is not a verifiable commit")
	}
	tree, err := gitOutput(ctx, worktreePath, "rev-parse", "--verify", "HEAD^{tree}")
	if err != nil || !validCommit(tree) {
		return Snapshot{}, errors.New("adopted Worktree tree identity is invalid")
	}
	headMode, headRef, err := readWorktreeHeadMode(ctx, worktreePath)
	if err != nil {
		return Snapshot{}, err
	}
	stateDigest, err := initialWorktreeStateSHA256(ctx, worktreePath, head, tree, headMode, headRef)
	if err != nil {
		return Snapshot{}, fmt.Errorf("capture adopted Worktree initial state: %w", err)
	}
	key := sha256.Sum256([]byte(jobID + "\x00" + task.ID))
	registrationPath := filepath.Join(m.root, hex.EncodeToString(key[:]))
	if err := m.reserveAdoption(jobID, task.ID, repositoryID, worktreePath); err != nil {
		return Snapshot{}, err
	}
	baselinePath := registrationPath + ".baseline.json"
	record, readErr := readBaselineRecord(baselinePath)
	if readErr == nil {
		if record.Origin != domain.WorktreeOriginAdopted || record.WorktreePath != worktreePath ||
			record.BaselineCommit != head || record.InitialStateSHA256 != stateDigest ||
			record.HeadMode != headMode || record.HeadRef != headRef {
			return Snapshot{}, errors.New("existing Worktree adoption retry does not match its original state")
		}
	} else if errors.Is(readErr, os.ErrNotExist) {
		record = baselineRecord{
			Version: 2, JobID: jobID, TaskID: task.ID,
			RepositoryID: repositoryID, RepositoryPath: repositoryPath,
			WorktreePath: worktreePath, BaselineCommit: head,
			Origin: domain.WorktreeOriginAdopted, InitialStateSHA256: stateDigest,
			HeadMode: headMode, HeadRef: headRef,
		}
		if err := writeBaselineRecord(baselinePath, record); err != nil {
			if !errors.Is(err, os.ErrExist) {
				return Snapshot{}, fmt.Errorf("persist adopted Worktree registration: %w", err)
			}
			record, err = readBaselineRecord(baselinePath)
			if err != nil || record.Origin != domain.WorktreeOriginAdopted ||
				record.WorktreePath != worktreePath || record.BaselineCommit != head ||
				record.InitialStateSHA256 != stateDigest || record.HeadMode != headMode ||
				record.HeadRef != headRef {
				return Snapshot{}, errors.New("concurrent Worktree adoption registered a different identity")
			}
		}
	} else {
		return Snapshot{}, fmt.Errorf("read adopted Worktree registration: %w", readErr)
	}
	snapshot := Snapshot{
		RepositoryID: repositoryID, RepositoryPath: repositoryPath,
		WorktreePath: worktreePath, BaselineCommit: record.BaselineCommit,
		Origin: record.Origin, InitialStateSHA256: record.InitialStateSHA256,
		HeadMode: record.HeadMode, HeadRef: record.HeadRef,
	}
	if err := m.Verify(ctx, jobID, task, snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("verify adopted Worktree: %w", err)
	}
	return snapshot, nil
}

type adoptionReservation struct {
	JobID        string `json:"job_id"`
	TaskID       string `json:"task_id"`
	RepositoryID string `json:"repository_id"`
	WorktreePath string `json:"worktree_path"`
}

func (m *Manager) reserveAdoption(jobID, taskID, repositoryID, worktreePath string) error {
	key := sha256.Sum256([]byte(worktreePath))
	directory := filepath.Join(m.root, ".adoptions")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create Worktree adoption registry: %w", err)
	}
	path := filepath.Join(directory, hex.EncodeToString(key[:])+".json")
	reservation := adoptionReservation{
		JobID: jobID, TaskID: taskID, RepositoryID: repositoryID, WorktreePath: worktreePath,
	}
	data, err := json.Marshal(reservation)
	if err != nil {
		return fmt.Errorf("encode Worktree adoption reservation: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		existing, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("read existing Worktree adoption reservation: %w", readErr)
		}
		var previous adoptionReservation
		if json.Unmarshal(existing, &previous) != nil || previous != reservation {
			return errors.New("physical Worktree is already reserved by another Job or Task")
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("reserve adopted Worktree: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write Worktree adoption reservation: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync Worktree adoption reservation: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close Worktree adoption reservation: %w", err)
	}
	return nil
}

func readWorktreeHeadMode(ctx context.Context, path string) (string, string, error) {
	ref, err := gitOutput(ctx, path, "symbolic-ref", "-q", "HEAD")
	if err == nil {
		if strings.TrimSpace(ref) == "" {
			return "", "", errors.New("attached Worktree HEAD has an empty symbolic reference")
		}
		return domain.WorktreeHeadBranchAttached, ref, nil
	}
	if isDetachedHeadProbeError(err) {
		return domain.WorktreeHeadDetached, "", nil
	}
	return "", "", fmt.Errorf("inspect Worktree HEAD mode: %w", err)
}

func initialWorktreeStateSHA256(
	ctx context.Context, path, head, tree, headMode, headRef string,
) (string, error) {
	status, err := gitBytes(ctx, path, "status", "--porcelain=v2", "--untracked-files=all", "-z")
	if err != nil {
		return "", err
	}
	staged, err := gitBytes(ctx, path, "diff", "--cached", "--binary", "HEAD")
	if err != nil {
		return "", err
	}
	working, err := gitBytes(ctx, path, "diff", "--binary")
	if err != nil {
		return "", err
	}
	untracked, err := gitBytes(ctx, path, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	for _, part := range [][]byte{
		[]byte(head), []byte(tree), []byte(headMode), []byte(headRef), status, staged, working,
	} {
		writeDigestPart(hash, part)
	}
	for _, relative := range strings.Split(string(untracked), "\x00") {
		if relative == "" {
			continue
		}
		filePath := filepath.Clean(filepath.Join(path, filepath.FromSlash(relative)))
		if !pathWithin(path, filePath) {
			return "", errors.New("untracked Worktree path escapes its root")
		}
		info, err := os.Lstat(filePath)
		if err != nil {
			return "", fmt.Errorf("inspect untracked Worktree file: %w", err)
		}
		if !info.Mode().IsRegular() {
			return "", errors.New("untracked Worktree contains a symlink or non-regular file")
		}
		content, err := os.ReadFile(filePath)
		if err != nil {
			return "", fmt.Errorf("read untracked Worktree file: %w", err)
		}
		writeDigestPart(hash, []byte(relative))
		writeDigestPart(hash, []byte(info.Mode().Perm().String()))
		writeDigestPart(hash, content)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func gitBytes(ctx context.Context, directory string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", directory}, args...)...)
	output, err := command.Output()
	if err != nil {
		return nil, err
	}
	return output, nil
}

func writeDigestPart(hash interface{ Write([]byte) (int, error) }, value []byte) {
	_, _ = hash.Write([]byte(fmt.Sprintf("%d:", len(value))))
	_, _ = hash.Write(value)
}

func cleanWorktreeStateSHA256() string {
	sum := sha256.Sum256(nil)
	return hex.EncodeToString(sum[:])
}
