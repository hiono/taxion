package copilot

import (
	"testing"

	sdk "github.com/github/copilot-sdk/go"
)

func TestJobOperationForPermissionPreservesShellCommand(t *testing.T) {
	request := &sdk.PermissionRequestShell{FullCommandText: "test -s README.md"}
	operation, detail, ok := jobOperationForPermission(request)
	if !ok || operation != "process.exec" || detail != request.FullCommandText {
		t.Fatalf("jobOperationForPermission(shell) = (%q, %q, %t)", operation, detail, ok)
	}
}

func TestJobOperationForTool(t *testing.T) {
	tests := []struct {
		name string
		want string
		ok   bool
	}{
		{"view", "repository.read", true},
		{"read", "repository.read", true},
		{"glob", "repository.read", true},
		{"edit", "repository.write", true},
		{"apply_patch", "repository.write", true},
		{"bash", "process.exec", true},
		{"mcp_repository", "", false},
		{"mcp_external_api", "", false},
		{ManagedSearchToolName, "network.access", true},
		{ReviewProgressToolName, "", false},
		{"unknown_tool", "", false},
	}
	for _, test := range tests {
		got, ok := jobOperationForTool(test.name)
		if got != test.want || ok != test.ok {
			t.Errorf("jobOperationForTool(%q) = (%q, %t), want (%q, %t)", test.name, got, ok, test.want, test.ok)
		}
	}
}
