package main

import (
	"strings"
	"testing"
)

func TestRuntimeAndWorkflowRejectInvalidCreditCapsBeforeLoadingWorkspace(t *testing.T) {
	for _, value := range []string{"-1", "29", "NaN", "+Inf", "-Inf"} {
		for name, command := range map[string]func([]string) error{
			"runtime": runtimeCmd, "workflow": workflowCmd,
		} {
			t.Run(name+"/"+value, func(t *testing.T) {
				action := "start"
				if name == "workflow" {
					action = "run"
				}
				err := command([]string{action, "--max-ai-credits", value})
				if err == nil || !strings.Contains(err.Error(), "AI credit limit") {
					t.Fatal("invalid cap reached workspace access or was accepted", err)
				}
			})
		}
	}
}
