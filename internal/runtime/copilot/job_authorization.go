package copilot

// This file maps Copilot events into the transitional string-operation callback.
// ADR-0080 replaces that contract with structured effect/resource observations.
// Do not add custom-tool name heuristics or treat these mappings as Job authority.

import (
	sdk "github.com/github/copilot-sdk/go"
)

func jobOperationForTool(name string) (string, bool) {
	switch name {
	case "view", "read", "glob", "grep", "skill", ReadContextToolName:
		return "repository.read", true
	case "create", "edit", "apply_patch":
		return "repository.write", true
	case "bash", "powershell":
		return "process.exec", true
	case ManagedSearchToolName:
		return "network.access", true
	default:
		// Custom tools have provider-defined effects. A name prefix is not
		// evidence that a tool is a shell command or has bounded side effects.
		return "", false
	}
}

func jobOperationForPermission(request sdk.PermissionRequest) (string, string, bool) {
	switch permission := request.(type) {
	case *sdk.PermissionRequestRead:
		return "repository.read", "", true
	case *sdk.PermissionRequestWrite:
		return "repository.write", "", true
	case *sdk.PermissionRequestShell:
		return "process.exec", permission.FullCommandText, true
	case *sdk.PermissionRequestCustomTool:
		operation, ok := jobOperationForTool(permission.ToolName)
		return operation, "", ok
	default:
		return "", "", false
	}
}
