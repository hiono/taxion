package main

import (
	"strings"
	"testing"
)

func TestRuntimeCommitArgumentContract(t *testing.T) {
	for _, tc := range []struct {
		action string
		flags  []string
		want   string
	}{
		{"commit", nil, "--message-file is required"},
		{"send", []string{"--prompt-file", "prompt", "--message-file", "message"}, "--message-file applies only to commit"},
		{"stop", []string{"--scanner", "betterleaks"}, "--scanner applies only to commit"},
		{"commit", []string{"--message-file", "message", "--timeout", "0s"}, "--timeout must be positive"},
	} {
		t.Run(tc.action+" "+tc.want, func(t *testing.T) {
			args := append([]string{tc.action, "--dispatch", "dispatch", "--system-prompt-file", "system"}, tc.flags...)
			err := runtimeCmd(args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("runtime argument error = %v, want %s", err, tc.want)
			}
		})
	}
	if !strings.Contains(usage, "runtime commit") {
		t.Fatal("runtime commit is absent from help")
	}
}
