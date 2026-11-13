package copilot

import (
	"strings"

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
		if strings.HasPrefix(name, "mcp_") && len(name) > len("mcp_") {
			return "process.exec", true
		}
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
