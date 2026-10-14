package gitproof

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
)

func CapturePartial(dispatch core.GraphDispatch) (*core.DispatchPartialGitEvidence, error) {
	if dispatch.Recipient != "CODER" || !filepath.IsAbs(dispatch.Instruction.Worktree) ||
		!fullSHA(dispatch.Instruction.BaselineSHA) {
		return nil, fmt.Errorf("partial Git evidence requires an absolute Coder worktree")
	}
	root, err := git(dispatch.Instruction.Worktree, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	root, err = canonical(root)
	if err != nil {
		return nil, fmt.Errorf("resolve partial worktree: %w", err)
	}
	requestedRoot, err := canonical(dispatch.Instruction.Worktree)
	if err != nil || requestedRoot != root {
		return nil, fmt.Errorf("partial evidence worktree must identify Git root %s", root)
	}
	remote, err := git(root, "remote", "get-url", "origin")
	if err != nil {
		return nil, fmt.Errorf("read partial worktree origin: %w", err)
	}
	if err := VerifyRepositoryRemote(dispatch, remote); err != nil {
		return nil, fmt.Errorf("partial worktree origin does not match repository: %w", err)
	}
	baseline, err := git(root, "rev-parse", dispatch.Instruction.BaselineSHA+"^{commit}")
	if err != nil || baseline != dispatch.Instruction.BaselineSHA || !fullSHA(baseline) {
		return nil, fmt.Errorf("partial evidence requires the exact approved baseline")
	}
	head, err := git(root, "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("read partial worktree HEAD: %w", err)
	}
	if !fullSHA(head) {
		return nil, fmt.Errorf("partial worktree HEAD is not a full commit SHA")
	}
	if _, err := git(root, "merge-base", "--is-ancestor", baseline, head); err != nil {
		return nil, fmt.Errorf("partial worktree HEAD does not descend from the approved baseline: %w", err)
	}
	branch, err := git(root, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || branch != dispatch.Instruction.Branch {
		return nil, fmt.Errorf("partial worktree branch does not match the Coder Dispatch")
	}

	index, err := os.CreateTemp("", "taxion-partial-index-*")
	if err != nil {
		return nil, fmt.Errorf("create partial Git index: %w", err)
	}
	indexPath := index.Name()
	if err := index.Close(); err != nil {
		os.Remove(indexPath)
		return nil, fmt.Errorf("close partial Git index: %w", err)
	}
	if err := os.Remove(indexPath); err != nil {
		return nil, fmt.Errorf("initialize partial Git index: %w", err)
	}
	defer os.Remove(indexPath)
	if _, err := gitWithIndex(root, indexPath, "read-tree", head); err != nil {
		return nil, err
	}
	if _, err := gitWithIndex(root, indexPath, "add", "-A", "--", "."); err != nil {
		return nil, fmt.Errorf("snapshot partial worktree: %w", err)
	}
	tree, err := gitWithIndex(root, indexPath, "write-tree")
	if err != nil {
		return nil, fmt.Errorf("write partial Git tree: %w", err)
	}
	if !fullSHA(tree) {
		return nil, fmt.Errorf("partial Git tree is not a full tree SHA")
	}
	headTree, err := git(root, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return nil, err
	}
	files, err := changedFiles(root, baseline, tree)
	if err != nil {
		return nil, err
	}
	if err := CheckChangedPaths(dispatch, files); err != nil {
		return nil, err
	}
	if dispatch.Instruction.OutputMode == core.OutputModeWorktreeTreeSnapshot {
		if err := pinSnapshot(root, dispatch.ID, tree); err != nil {
			return nil, fmt.Errorf("retain partial tree snapshot: %w", err)
		}
	}
	return &core.DispatchPartialGitEvidence{
		Worktree: root, Repository: dispatch.Repository, Branch: branch,
		BaselineSHA: baseline, HeadSHA: head, TreeSHA: tree, ChangedFiles: files,
		UncommittedChanges: tree != headTree, CapturedAt: time.Now().UTC(),
	}, nil
}

func VerifyPartialSuccessor(source core.GraphDispatch, task core.GraphTask) error {
	if source.Status != core.DispatchCancelled || source.Result == nil ||
		source.Result.PartialGitEvidence == nil || task.Repo != source.Repository ||
		task.BaselineSHA != source.Result.PartialGitEvidence.BaselineSHA ||
		!fullSHA(task.TargetSHA) || !filepath.IsAbs(task.Worktree) ||
		task.Worktree == source.Result.PartialGitEvidence.Worktree {
		return fmt.Errorf("successor must preserve the stopped dispatch baseline and target its recorded partial tree")
	}
	partial := source.Result.PartialGitEvidence
	if source.Instruction.OutputMode == core.OutputModeWorktreeTreeSnapshot {
		if task.OutputMode != core.OutputModeWorktreeTreeSnapshot ||
			task.TargetSHA != source.ExpectedStartSHA() || task.TargetTreeSHA != partial.TreeSHA ||
			!sameFiles(task.TargetChangedFiles, partial.ChangedFiles) {
			return fmt.Errorf("successor Task does not target the saved partial tree")
		}
		return verifySnapshotSuccessor(source, task, partial.TreeSHA,
			partial.ChangedFiles, partial.Worktree)
	}
	if task.OutputMode == core.OutputModeWorktreeTreeSnapshot {
		return fmt.Errorf("commit-mode partial result cannot continue into snapshot mode")
	}
	return verifySuccessorTree(source, task, partial.Worktree, partial.TreeSHA, partial.ChangedFiles)
}

func VerifyCompletedSuccessor(source core.GraphDispatch, task core.GraphTask) error {
	if source.Status != core.DispatchSucceeded || source.Recipient != "CODER" || source.Result == nil ||
		source.Result.Status != core.DispatchSucceeded || source.Result.GitEvidence == nil ||
		source.Result.OutputSHA != task.TargetSHA || task.BaselineSHA != source.Instruction.BaselineSHA {
		return fmt.Errorf("successor must target the saved successful Coder result")
	}
	if source.Instruction.OutputMode == core.OutputModeWorktreeTreeSnapshot {
		if task.TargetTreeSHA != source.Result.OutputTreeSHA ||
			task.OutputMode != core.OutputModeWorktreeTreeSnapshot ||
			!sameFiles(task.TargetChangedFiles, source.Result.ChangedFiles) {
			return fmt.Errorf("successor Task does not target the saved Coder tree snapshot")
		}
		return verifySnapshotSuccessor(source, task, source.Result.OutputTreeSHA,
			source.Result.ChangedFiles, source.Result.GitEvidence.Worktree)
	}
	if task.OutputMode == core.OutputModeWorktreeTreeSnapshot {
		return fmt.Errorf("commit-mode Coder result cannot continue into snapshot mode")
	}
	return verifySuccessorTree(source, task, source.Result.GitEvidence.Worktree, "", source.Result.ChangedFiles)
}

func verifySnapshotSuccessor(source core.GraphDispatch, task core.GraphTask,
	tree string, files []string, sourceWorktree string) error {
	if task.OutputMode != core.OutputModeWorktreeTreeSnapshot ||
		task.Repo != source.Repository || task.BaselineSHA != source.Instruction.BaselineSHA ||
		task.TargetSHA != source.ExpectedStartSHA() || !fullSHA(tree) ||
		!filepath.IsAbs(task.Worktree) || !filepath.IsAbs(sourceWorktree) {
		return fmt.Errorf("snapshot successor must preserve the source commit and tree")
	}
	root, err := worktreeRoot(task.Worktree)
	if err != nil {
		return err
	}
	sourceRoot, err := worktreeRoot(sourceWorktree)
	if err != nil {
		return err
	}
	if root == sourceRoot {
		return fmt.Errorf("snapshot successor requires a distinct worktree")
	}
	sourceGit, err := commonDirectory(sourceRoot)
	if err != nil {
		return err
	}
	targetGit, err := commonDirectory(root)
	if err != nil || sourceGit != targetGit {
		return fmt.Errorf("snapshot successor must share the source repository object database")
	}
	remote, err := git(root, "remote", "get-url", "origin")
	if err != nil {
		return fmt.Errorf("read snapshot successor origin: %w", err)
	}
	if err := VerifyRepositoryRemote(source, remote); err != nil {
		return fmt.Errorf("snapshot successor origin differs: %w", err)
	}
	head, err := git(root, "rev-parse", "HEAD")
	if err != nil || head != task.TargetSHA {
		return fmt.Errorf("snapshot successor HEAD does not match its approved baseline commit")
	}
	branch, err := git(root, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || branch != task.Branch {
		return fmt.Errorf("snapshot successor branch differs from its approved Task")
	}
	if err := verifySnapshotRef(root, source.ID, tree); err != nil {
		return err
	}
	actualTree, actualFiles, _, err := captureWorktreeTree(root, task.BaselineSHA, head)
	if err != nil {
		return err
	}
	if actualTree != tree || !sameFiles(actualFiles, files) ||
		!sameFiles(task.TargetChangedFiles, files) {
		return fmt.Errorf("snapshot successor worktree differs from the saved tree")
	}
	contract := core.GraphDispatch{Recipient: "CODER", Instruction: core.DispatchInstruction{
		Version: core.DispatchInstructionVersion, AuthorizedActions: task.Scope,
		ForbiddenActions: task.OutOfScope, Worktree: task.Worktree, Branch: task.Branch,
		BaselineSHA: task.BaselineSHA, TargetSHA: task.TargetSHA,
		OutputMode: task.OutputMode, TargetTreeSHA: task.TargetTreeSHA,
		TargetChangedFiles: task.TargetChangedFiles,
		FileAccess:         core.CloneFileAccessPolicy(task.FileAccess),
	}}
	if err := CheckChangedPaths(contract, files); err != nil {
		return fmt.Errorf("snapshot successor scope does not include the saved work: %w", err)
	}
	return nil
}

func verifySuccessorTree(source core.GraphDispatch, task core.GraphTask, sourceWorktree, expectedTree string, expectedFiles []string) error {
	if task.Repo != source.Repository || task.BaselineSHA != source.Instruction.BaselineSHA ||
		!fullSHA(task.BaselineSHA) || !fullSHA(task.TargetSHA) || !filepath.IsAbs(task.Worktree) {
		return fmt.Errorf("successor must preserve the source baseline and use an absolute clean worktree")
	}
	root, err := git(task.Worktree, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	root, err = canonical(root)
	if err != nil {
		return fmt.Errorf("resolve successor worktree: %w", err)
	}
	requestedRoot, err := canonical(task.Worktree)
	if err != nil || requestedRoot != root {
		return fmt.Errorf("successor path must identify its Git root")
	}
	sourceAbsolute, err := filepath.Abs(sourceWorktree)
	if err != nil || filepath.Clean(sourceAbsolute) == root {
		return fmt.Errorf("successor requires a distinct clean worktree")
	}
	if sourceRoot, err := canonical(sourceWorktree); err == nil && sourceRoot == root {
		return fmt.Errorf("successor requires a distinct clean worktree")
	}
	remote, err := git(root, "remote", "get-url", "origin")
	if err != nil {
		return fmt.Errorf("read successor worktree origin: %w", err)
	}
	if err := VerifyRepositoryRemote(source, remote); err != nil {
		return fmt.Errorf("successor worktree origin differs: %w", err)
	}
	head, err := git(root, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("read successor worktree HEAD: %w", err)
	}
	if head != task.TargetSHA {
		return fmt.Errorf("successor worktree HEAD must match its approved target SHA")
	}
	tree, err := git(root, "rev-parse", task.TargetSHA+"^{tree}")
	if err != nil {
		return fmt.Errorf("read successor target tree: %w", err)
	}
	if expectedTree != "" && tree != expectedTree {
		return fmt.Errorf("successor target tree does not match stopped partial work evidence")
	}
	branch, err := git(root, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return fmt.Errorf("read successor branch: %w", err)
	}
	if branch != task.Branch {
		return fmt.Errorf("successor worktree branch differs from its approved Task")
	}
	if _, err := git(root, "merge-base", "--is-ancestor", task.BaselineSHA, task.TargetSHA); err != nil {
		return fmt.Errorf("successor target does not descend from the approved baseline: %w", err)
	}
	status, err := git(root, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return fmt.Errorf("read successor worktree status: %w", err)
	}
	if status != "" {
		return fmt.Errorf("successor worktree must be clean")
	}
	files, err := changedFiles(root, task.BaselineSHA, task.TargetSHA)
	if err != nil {
		return err
	}
	if !sameFiles(files, expectedFiles) {
		return fmt.Errorf("successor changed files differ from the saved source result")
	}
	contract := core.GraphDispatch{Recipient: "CODER", Instruction: core.DispatchInstruction{
		Version: core.DispatchInstructionVersion, AuthorizedActions: task.Scope,
		ForbiddenActions: task.OutOfScope, Worktree: task.Worktree,
		BaselineSHA: task.BaselineSHA, TargetSHA: task.TargetSHA,
		FileAccess: core.CloneFileAccessPolicy(task.FileAccess),
	}}
	if err := CheckChangedPaths(contract, files); err != nil {
		return fmt.Errorf("successor scope does not include the partial work: %w", err)
	}
	return nil
}

func gitWithIndex(directory, index string, args ...string) (string, error) {
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_INDEX_FILE=") {
			env = append(env, entry)
		}
	}
	command.Env = append(env, "GIT_INDEX_FILE="+index)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(output)), nil
}
