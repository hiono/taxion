package copilot

// This file converts provider permission observations into provider-neutral Job effects.

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	sdk "github.com/github/copilot-sdk/go"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
)

func jobEffectForPermission(worktree string, trackedFiles []string, request sdk.PermissionRequest) (runtime.JobEffectRequest, bool) {
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
		return runtime.JobEffectRequest{
			Effect: core.JobEffectRepositoryRead, Target: canonical, Tracked: slices.Contains(trackedFiles, canonical),
			Capability: "file.read", Action: "read", Inspectable: true, SandboxEnforced: true,
		}, err == nil
	case *sdk.PermissionRequestWrite:
		target := permission.FileName
		if permission.ResolvedPath != nil && *permission.ResolvedPath != "" {
			target = *permission.ResolvedPath
		}
		if target == "" {
			return runtime.JobEffectRequest{}, false
		}
		canonical, err := canonicalJobTarget(worktree, target)
		return runtime.JobEffectRequest{
			Effect: core.JobEffectRepositoryWrite, Target: canonical, Tracked: slices.Contains(trackedFiles, canonical),
			Capability: "file.write", Action: "write", Inspectable: true,
			SandboxEnforced: permission.RequestSandboxBypass == nil || !*permission.RequestSandboxBypass,
			SandboxBypass:   permission.RequestSandboxBypass != nil && *permission.RequestSandboxBypass,
		}, err == nil
	case *sdk.PermissionRequestShell:
		bypass := permission.RequestSandboxBypass != nil && *permission.RequestSandboxBypass
		managedApproval := permission.ManagedApprovalRequired != nil && *permission.ManagedApprovalRequired
		return runtime.JobEffectRequest{
			Effect: core.JobEffectValidationExecution, Target: permission.FullCommandText,
			Capability: "shell", Action: "execute", Inspectable: true,
			SandboxEnforced: !bypass && !managedApproval, SandboxBypass: bypass,
		}, permission.FullCommandText != ""
	case *sdk.PermissionRequestCustomTool:
		switch permission.ToolName {
		case ReadContextToolName:
			return runtime.JobEffectRequest{
				Effect: core.JobEffectJobContextRead, Capability: "job-context", Action: "read",
				Inspectable: true, SandboxEnforced: true,
			}, true
		case ManagedSearchToolName:
			return runtime.JobEffectRequest{
				Effect: core.JobEffectNetworkAccess, Resource: "external:managed-search",
				Capability: "managed-search", Action: "search", Inspectable: true,
			}, true
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
