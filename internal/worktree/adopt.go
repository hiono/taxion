package worktree

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	unlock, err := m.lock(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer unlock()
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
		pathWithin(worktreePath, repositoryPath) || pathWithin(m.root, worktreePath) ||
		pathWithin(worktreePath, m.root) {
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
	tree, err := gitOutput(ctx, worktreePath, "rev-parse", "--verify", head+"^{tree}")
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
	headAfter, err := gitOutput(ctx, worktreePath, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || headAfter != head {
		return Snapshot{}, errors.New("adopted Worktree HEAD changed while its initial state was captured")
	}
	treeAfter, err := gitOutput(ctx, worktreePath, "rev-parse", "--verify", headAfter+"^{tree}")
	if err != nil || treeAfter != tree {
		return Snapshot{}, errors.New("adopted Worktree tree changed while its initial state was captured")
	}
	headModeAfter, headRefAfter, err := readWorktreeHeadMode(ctx, worktreePath)
	if err != nil || headModeAfter != headMode || headRefAfter != headRef {
		return Snapshot{}, errors.New("adopted Worktree HEAD mode changed while its initial state was captured")
	}
	stateDigestAfter, err := initialWorktreeStateSHA256(ctx, worktreePath, headAfter, treeAfter, headModeAfter, headRefAfter)
	if err != nil || stateDigestAfter != stateDigest {
		return Snapshot{}, errors.New("adopted Worktree changed while its initial state was captured")
	}
	registrationPath := adoptionRegistrationPath(m.root, jobID, task.ID)
	baselinePath := registrationPath + ".baseline.json"
	record, readErr := readBaselineRecord(baselinePath)
	registrationCreated := false
	reservation := adoptionReservation{
		JobID: jobID, TaskID: task.ID, RepositoryID: repositoryID,
		WorktreePath: worktreePath, GitDir: resolvedGitDir,
	}
	if readErr == nil {
		if record.Origin != domain.WorktreeOriginAdopted || record.WorktreePath != worktreePath ||
			record.BaselineCommit != head || record.InitialStateSHA256 != stateDigest ||
			record.HeadMode != headMode || record.HeadRef != headRef {
			return Snapshot{}, errors.New("existing Worktree adoption retry does not match its original state")
		}
		if err := m.reserveAdoption(jobID, task.ID, repositoryID, worktreePath, resolvedGitDir); err != nil {
			return Snapshot{}, err
		}
	} else if errors.Is(readErr, os.ErrNotExist) {
		if err := m.recoverOrphanedAdoptionReservations(reservation); err != nil {
			return Snapshot{}, fmt.Errorf("recover interrupted Worktree adoption reservation: %w", err)
		}
		if err := m.reserveAdoption(jobID, task.ID, repositoryID, worktreePath, resolvedGitDir); err != nil {
			return Snapshot{}, err
		}
		record = baselineRecord{
			Version: 2, JobID: jobID, TaskID: task.ID,
			RepositoryID: repositoryID, RepositoryPath: repositoryPath,
			WorktreePath: worktreePath, BaselineCommit: head,
			Origin: domain.WorktreeOriginAdopted, InitialStateSHA256: stateDigest,
			HeadMode: headMode, HeadRef: headRef,
		}
		if err := writeBaselineRecord(baselinePath, record); err != nil {
			if errors.Is(err, os.ErrExist) {
				existing, readErr := readBaselineRecord(baselinePath)
				if readErr == nil && existing.Origin == domain.WorktreeOriginAdopted &&
					existing.WorktreePath == worktreePath && existing.BaselineCommit == head &&
					existing.InitialStateSHA256 == stateDigest && existing.HeadMode == headMode &&
					existing.HeadRef == headRef {
					record = existing
				} else {
					cleanupErr := m.removeOrphanedAdoptionReservation(reservation)
					return Snapshot{}, errors.Join(
						errors.New("concurrent Worktree adoption registered a different identity"), cleanupErr,
					)
				}
			} else {
				cleanupErr := m.removeOrphanedAdoptionReservation(reservation)
				return Snapshot{}, errors.Join(
					fmt.Errorf("persist adopted Worktree registration: %w", err), cleanupErr,
				)
			}
		} else {
			registrationCreated = true
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
	if err := m.verify(ctx, jobID, task, snapshot); err != nil {
		verifyErr := fmt.Errorf("verify adopted Worktree: %w", err)
		if registrationCreated {
			removeErr := os.Remove(baselinePath)
			if errors.Is(removeErr, os.ErrNotExist) {
				removeErr = nil
			}
			if removeErr == nil {
				removeErr = syncDirectory(filepath.Dir(baselinePath))
			}
			cleanupErr := m.removeOrphanedAdoptionReservation(reservation)
			return Snapshot{}, errors.Join(verifyErr, removeErr, cleanupErr)
		}
		return Snapshot{}, verifyErr
	}
	return snapshot, nil
}

func adoptionRegistrationPath(root, jobID, taskID string) string {
	key := sha256.Sum256([]byte(jobID + "\x00" + taskID))
	return filepath.Join(root, hex.EncodeToString(key[:]))
}

func adoptionReservationPaths(directory string, reservation adoptionReservation) []string {
	taskKey := sha256.Sum256([]byte(reservation.JobID + "\x00" + reservation.TaskID))
	gitDirKey := sha256.Sum256([]byte(reservation.GitDir))
	worktreeKey := sha256.Sum256([]byte(reservation.WorktreePath))
	return []string{
		filepath.Join(directory, "task-"+hex.EncodeToString(taskKey[:])+".json"),
		filepath.Join(directory, "gitdir-"+hex.EncodeToString(gitDirKey[:])+".json"),
		filepath.Join(directory, "path-"+hex.EncodeToString(worktreeKey[:])+".json"),
	}
}

func (m *Manager) recoverOrphanedAdoptionReservations(reservation adoptionReservation) error {
	directory := filepath.Join(m.root, ".adoptions")
	paths := adoptionReservationPaths(directory, reservation)
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read existing Worktree adoption reservation: %w", err)
		}
		var existing adoptionReservation
		if err := json.Unmarshal(data, &existing); err != nil {
			return errors.New("existing Worktree adoption reservation is invalid")
		}
		if err := m.removeOrphanedAdoptionReservation(existing); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) removeOrphanedAdoptionReservation(reservation adoptionReservation) error {
	baselinePath := adoptionRegistrationPath(m.root, reservation.JobID, reservation.TaskID) + ".baseline.json"
	record, err := readBaselineRecord(baselinePath)
	if err == nil {
		if record.RepositoryID == reservation.RepositoryID &&
			record.WorktreePath == reservation.WorktreePath &&
			(record.Origin == domain.WorktreeOriginAdopted || record.Origin == domain.WorktreeOriginTaxionCreated) {
			return nil
		}
		if record.Origin != domain.WorktreeOriginTaxionCreated {
			return errors.New("existing adopted Worktree registration does not match its ownership reservation; refusing cleanup")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect Worktree adoption baseline before reservation cleanup: %w", err)
	}

	directory := filepath.Join(m.root, ".adoptions")
	paths := adoptionReservationPaths(directory, reservation)
	removed := false
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read Worktree adoption reservation before cleanup: %w", err)
		}
		var existing adoptionReservation
		if err := json.Unmarshal(data, &existing); err != nil {
			return errors.New("Worktree adoption reservation is invalid; refusing cleanup")
		}
		if existing != reservation {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove interrupted Worktree adoption reservation: %w", err)
		}
		removed = true
	}
	if removed {
		if err := syncDirectory(directory); err != nil {
			return fmt.Errorf("sync Worktree adoption reservation cleanup: %w", err)
		}
	}
	return nil
}

type adoptionReservation struct {
	JobID        string `json:"job_id"`
	TaskID       string `json:"task_id"`
	RepositoryID string `json:"repository_id"`
	WorktreePath string `json:"worktree_path"`
	GitDir       string `json:"git_dir"`
}

func (m *Manager) reserveAdoption(jobID, taskID, repositoryID, worktreePath, gitDir string) error {
	directory := filepath.Join(m.root, ".adoptions")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create Worktree adoption registry: %w", err)
	}
	reservation := adoptionReservation{
		JobID: jobID, TaskID: taskID, RepositoryID: repositoryID,
		WorktreePath: worktreePath, GitDir: gitDir,
	}
	createdPaths := make([]string, 0, 3)
	for _, path := range adoptionReservationPaths(directory, reservation) {
		created, err := writeAdoptionReservation(path, reservation)
		if err != nil {
			for _, createdPath := range createdPaths {
				_ = os.Remove(createdPath)
			}
			if len(createdPaths) > 0 {
				_ = syncDirectory(directory)
			}
			return err
		}
		if created {
			createdPaths = append(createdPaths, path)
		}
	}
	return nil
}

func writeAdoptionReservation(path string, reservation adoptionReservation) (bool, error) {
	data, err := json.Marshal(reservation)
	if err != nil {
		return false, fmt.Errorf("encode Worktree adoption reservation: %w", err)
	}
	if err := writeExclusiveAtomic(path, data, 0o600); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return false, fmt.Errorf("reserve adopted Worktree: %w", err)
		}
		existing, readErr := os.ReadFile(path)
		if readErr != nil {
			return false, fmt.Errorf("read existing Worktree adoption reservation: %w", readErr)
		}
		var previous adoptionReservation
		if json.Unmarshal(existing, &previous) != nil || previous != reservation {
			return false, errors.New("Task or Git Worktree admin directory is already reserved by another Job or Task")
		}
		return false, nil
	}
	return true, nil
}

func writeExclusiveAtomic(path string, data []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create atomic record directory: %w", err)
	}
	file, err := os.CreateTemp(directory, ".taxion-atomic-*")
	if err != nil {
		return err
	}
	temporaryPath := file.Name()
	defer os.Remove(temporaryPath)
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	written, err := file.Write(data)
	if err != nil {
		_ = file.Close()
		return err
	}
	if written != len(data) {
		_ = file.Close()
		return io.ErrShortWrite
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Link(temporaryPath, path); err != nil {
		return err
	}
	cleanup := func(cause error) error {
		_ = os.Remove(path)
		_ = syncDirectory(directory)
		return cause
	}
	if err := syncDirectory(directory); err != nil {
		return cleanup(err)
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

func verifyWorktreeIndexFlags(ctx context.Context, path string) error {
	check := func(entries []byte) error {
		for _, entry := range strings.Split(string(entries), "\x00") {
			if len(entry) == 0 {
				continue
			}
			switch entry[0] {
			case 'h':
				return errors.New("Worktree contains assume-unchanged tracked files")
			case 'S':
				return errors.New("Worktree contains skip-worktree tracked files")
			case 's':
				return errors.New("Worktree contains skip-worktree and assume-unchanged tracked files")
			}
		}
		return nil
	}
	entries, err := gitBytes(ctx, path, "--no-optional-locks", "ls-files", "-v", "-z")
	if err != nil {
		return fmt.Errorf("inspect Worktree index flags: %w", err)
	}
	if err := check(entries); err != nil {
		return err
	}
	submoduleEntries, err := gitBytes(
		ctx, path, "--no-optional-locks", "submodule", "foreach", "--quiet", "--recursive", "git ls-files -v -z",
	)
	if err != nil {
		return fmt.Errorf("inspect recursive submodule index flags: %w", err)
	}
	return check(submoduleEntries)
}

func initialWorktreeStateSHA256(
	ctx context.Context, path, head, tree, headMode, headRef string,
) (string, error) {
	if err := verifyWorktreeIndexFlags(ctx, path); err != nil {
		return "", err
	}
	status, err := gitBytes(ctx, path, "-c", "core.quotePath=true", "-c", "status.relativePaths=false", "-c", "status.renames=false", "--no-optional-locks", "status", "--porcelain=v2", "--untracked-files=all", "--ignore-submodules=none", "-z")
	if err != nil {
		return "", err
	}
	staged, err := gitBytes(ctx, path, "-c", "core.quotePath=true", "-c", "diff.algorithm=myers", "-c", "diff.renames=false", "-c", "color.ui=false", "--no-optional-locks", "diff", "--cached", "--binary", "--no-ext-diff", "--no-textconv", "--no-renames", "--no-color", "--ignore-submodules=none", head, "--")
	if err != nil {
		return "", err
	}
	working, err := gitBytes(ctx, path, "-c", "core.quotePath=true", "-c", "diff.algorithm=myers", "-c", "diff.renames=false", "-c", "color.ui=false", "--no-optional-locks", "diff", "--binary", "--no-ext-diff", "--no-textconv", "--no-renames", "--no-color", "--ignore-submodules=none")
	if err != nil {
		return "", err
	}
	untracked, err := gitBytes(ctx, path, "--no-optional-locks", "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return "", err
	}
	submoduleStatus, err := gitBytes(ctx, path, "--no-optional-locks", "submodule", "status", "--recursive")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(submoduleStatus), "\n") {
		if strings.HasPrefix(line, "-") || strings.HasPrefix(line, "U") {
			return "", errors.New("Worktree contains an uninitialized or conflicted submodule")
		}
	}
	dirtySubmodules, err := gitBytes(ctx, path, "--no-optional-locks", "submodule", "foreach", "--quiet", "--recursive", "git status --porcelain=v2 --untracked-files=all --ignore-submodules=none")
	if err != nil {
		return "", err
	}
	if len(dirtySubmodules) > 0 {
		return "", errors.New("Worktree contains a dirty submodule")
	}
	submoduleIndexEntries, err := gitBytes(ctx, path, "--no-optional-locks", "ls-files", "--stage", "-z")
	if err != nil {
		return "", fmt.Errorf("capture stable submodule gitlink identities: %w", err)
	}
	hash := sha256.New()
	for _, part := range [][]byte{
		[]byte(head), []byte(tree), []byte(headMode), []byte(headRef), status, staged, working, submoduleIndexEntries,
	} {
		writeDigestPart(hash, part)
	}
	if err := hashTrackedWorktreeFiles(ctx, path, hash); err != nil {
		return "", err
	}
	for _, relative := range strings.Split(string(untracked), "\x00") {
		if err := ctx.Err(); err != nil {
			return "", err
		}
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
		file, err := openRegularWorktreeFile(filePath, info)
		if err != nil {
			return "", fmt.Errorf("open untracked Worktree file without following replacements: %w", err)
		}
		openedInfo, statErr := file.Stat()
		if statErr != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) || openedInfo.Size() != info.Size() {
			_ = file.Close()
			return "", errors.New("untracked Worktree file changed before it could be hashed")
		}
		writeDigestPart(hash, []byte(relative))
		writeDigestPart(hash, []byte(info.Mode().Perm().String()))
		if _, err := fmt.Fprintf(hash, "%d:", info.Size()); err != nil {
			_ = file.Close()
			return "", fmt.Errorf("write untracked Worktree file length to digest: %w", err)
		}
		written, copyErr := io.CopyBuffer(hash, contextReader{ctx: ctx, reader: file}, make([]byte, 64*1024))
		afterInfo, afterStatErr := file.Stat()
		closeErr := file.Close()
		if copyErr != nil {
			return "", fmt.Errorf("hash untracked Worktree file: %w", copyErr)
		}
		if afterStatErr != nil || closeErr != nil || written != info.Size() ||
			!os.SameFile(info, afterInfo) || afterInfo.Size() != info.Size() ||
			!afterInfo.ModTime().Equal(info.ModTime()) {
			return "", errors.New("untracked Worktree file changed while it was being hashed")
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

func hashTrackedWorktreeFiles(ctx context.Context, path string, hash interface{ Write([]byte) (int, error) }) error {
	entries, err := gitBytes(ctx, path, "--no-optional-locks", "ls-files", "--stage", "-z")
	if err != nil {
		return fmt.Errorf("list tracked Worktree files for raw-state digest: %w", err)
	}
	for _, entry := range strings.Split(string(entries), "\x00") {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry == "" {
			continue
		}
		separator := strings.IndexByte(entry, '\t')
		if separator <= 0 || separator == len(entry)-1 {
			return errors.New("tracked Worktree index entry is malformed")
		}
		fields := strings.Fields(entry[:separator])
		if len(fields) != 3 {
			return errors.New("tracked Worktree index metadata is malformed")
		}
		mode, relative := fields[0], entry[separator+1:]
		if mode == "160000" {
			continue
		}
		filePath := filepath.Clean(filepath.Join(path, filepath.FromSlash(relative)))
		if !pathWithin(path, filePath) {
			return errors.New("tracked Worktree path escapes its root")
		}
		info, err := os.Lstat(filePath)
		if errors.Is(err, os.ErrNotExist) {
			writeDigestPart(hash, []byte(relative))
			writeDigestPart(hash, []byte(mode))
			writeDigestPart(hash, []byte("missing"))
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect tracked Worktree file %q: %w", relative, err)
		}
		writeDigestPart(hash, []byte(relative))
		writeDigestPart(hash, []byte(mode))
		writeDigestPart(hash, []byte(fmt.Sprintf("%o", uint32(info.Mode()))))
		switch mode {
		case "120000":
			if info.Mode()&os.ModeSymlink == 0 {
				return errors.New("tracked symlink changed file type during adoption")
			}
			target, err := os.Readlink(filePath)
			if err != nil {
				return fmt.Errorf("read tracked Worktree symlink %q: %w", relative, err)
			}
			after, err := os.Lstat(filePath)
			if err != nil || !os.SameFile(info, after) || after.Mode()&os.ModeSymlink == 0 {
				return errors.New("tracked Worktree symlink changed while its state was captured")
			}
			writeDigestPart(hash, []byte(target))
		case "100644", "100755":
			if !info.Mode().IsRegular() {
				return errors.New("tracked Worktree file is not a regular file")
			}
			file, err := openRegularWorktreeFile(filePath, info)
			if err != nil {
				return fmt.Errorf("open tracked Worktree file %q: %w", relative, err)
			}
			if _, err := fmt.Fprintf(hash, "%d:", info.Size()); err != nil {
				_ = file.Close()
				return err
			}
			written, copyErr := io.CopyBuffer(hash, contextReader{ctx: ctx, reader: file}, make([]byte, 64*1024))
			after, statErr := file.Stat()
			closeErr := file.Close()
			if copyErr != nil {
				return fmt.Errorf("hash tracked Worktree file %q: %w", relative, copyErr)
			}
			if statErr != nil || closeErr != nil || written != info.Size() ||
				!os.SameFile(info, after) || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
				return errors.New("tracked Worktree file changed while its state was captured")
			}
		default:
			return fmt.Errorf("unsupported tracked Worktree file mode %q", mode)
		}
	}
	return nil
}

func gitBytes(ctx context.Context, directory string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"--no-replace-objects", "-C", directory}, args...)...)
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

func (m *Manager) verifyAdoptionReservation(jobID, taskID, repositoryID, worktreePath, gitDir string) error {
	directory := filepath.Join(m.root, ".adoptions")
	reservation := adoptionReservation{
		JobID: jobID, TaskID: taskID, RepositoryID: repositoryID,
		WorktreePath: worktreePath, GitDir: gitDir,
	}
	for _, path := range adoptionReservationPaths(directory, reservation) {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read Worktree adoption reservation: %w", err)
		}
		var actual adoptionReservation
		if err := json.Unmarshal(data, &actual); err != nil || actual != reservation {
			return errors.New("Worktree adoption reservation identity is invalid")
		}
	}
	return nil
}
