package gitproof

import (
	"fmt"
	"slices"

	"github.com/hiono/taxion/internal/core"
)

func verifyChangeManifest(dispatch core.GraphDispatch, result *core.DispatchResult, files []string) error {
	policy := dispatch.Instruction.ChangeManifestPolicy
	if policy == "" || dispatch.Recipient != "CODER" {
		if result.ChangeManifest != nil || !sameFiles(files, result.ChangedFiles) {
			return fmt.Errorf("Git changed files %v do not match reported files %v", files, result.ChangedFiles)
		}
		return nil
	}
	if policy != core.ChangeManifestPolicy {
		return fmt.Errorf("unsupported change manifest policy %q", policy)
	}
	if err := CheckChangedPaths(dispatch, files); err != nil {
		return err
	}
	if manifest := result.ChangeManifest; manifest != nil {
		if manifest.Policy != policy || manifest.BaselineSHA != dispatch.Instruction.BaselineSHA ||
			manifest.OutputSHA != result.OutputSHA || manifest.OutputTreeSHA != result.OutputTreeSHA ||
			!slices.Equal(files, result.ChangedFiles) {
			return fmt.Errorf("candidate differs from the controller change manifest")
		}
		return nil
	}
	result.ChangeManifest = &core.DispatchChangeManifest{
		Policy: policy, BaselineSHA: dispatch.Instruction.BaselineSHA,
		OutputSHA: result.OutputSHA, OutputTreeSHA: result.OutputTreeSHA,
		ReportedChangedFiles: slices.Clone(result.ChangedFiles),
	}
	result.ChangedFiles = slices.Clone(files)
	return nil
}
