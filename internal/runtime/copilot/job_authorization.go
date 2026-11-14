package copilot

// This file converts provider permission observations into provider-neutral Job effects.

import (
	"fmt"
	"path/filepath"
	"strings"

	sdk "github.com/github/copilot-sdk/go"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
)

func jobEffectForPermission(worktree string, request sdk.PermissionRequest) (runtime.JobEffectRequest, bool) {
	switch permission := request.(type) {
	case *sdk.PermissionRequestRead:
		target := permission.Path
		if permission.ResolvedPath != nil && *permission.ResolvedPath != "" {
			target = *permission.ResolvedPath
		}
		if target == "" {
			return runtime.JobEffectRequest{}, false
		}
		canonical, err := canonicalJobTarget(worktree, target)
		return runtime.JobEffectRequest{Effect: core.JobEffectRepositoryRead, Target: canonical}, err == nil
	case *sdk.PermissionRequestWrite:
		target := permission.FileName
		if permission.ResolvedPath != nil && *permission.ResolvedPath != "" {
			target = *permission.ResolvedPath
		}
		if target == "" {
			return runtime.JobEffectRequest{}, false
		}
		canonical, err := canonicalJobTarget(worktree, target)
		return runtime.JobEffectRequest{Effect: core.JobEffectRepositoryWrite, Target: canonical}, err == nil
	case *sdk.PermissionRequestShell:
		return runtime.JobEffectRequest{Effect: core.JobEffectValidationExecution, Target: permission.FullCommandText}, permission.FullCommandText != ""
	case *sdk.PermissionRequestCustomTool:
		switch permission.ToolName {
		case ReadContextToolName:
			return runtime.JobEffectRequest{Effect: core.JobEffectJobContextRead}, true
		case ManagedSearchToolName:
			return runtime.JobEffectRequest{Effect: core.JobEffectNetworkAccess}, true
		default:
			return runtime.JobEffectRequest{}, false
		}
	default:
		return runtime.JobEffectRequest{}, false
	}
}

func canonicalJobTarget(worktree, target string) (string, error) {
	canonical, err := canonicalWriteTarget(worktree, target)
	if err != nil || !within(worktree, canonical) {
		return "", fmt.Errorf("taxion: resource target is outside the assigned worktree or cannot be resolved")
	}
	relative, err := filepath.Rel(worktree, canonical)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("taxion: resource target is not a canonical in-worktree path")
	}
	return filepath.ToSlash(relative), nil
}
