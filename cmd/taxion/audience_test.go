package main

import (
	"strings"
	"testing"
)

func TestHelpDescribesUserVisibleActions(t *testing.T) {
	for _, description := range []string{
		"Run approved coding tasks with saved results and independent review",
		"Print coding-agent usage instructions",
		"Remove stopped tasks and associated work from normal views, keeping history",
	} {
		if !strings.Contains(usage, description) {
			t.Errorf("help omits the user-visible purpose: %q", description)
		}
	}
	for _, old := range []string{"Evidence-governed control plane", "Print the orchestration prompt", "Preview/retire"} {
		if strings.Contains(usage, old) {
			t.Errorf("help reverted to internal-procedure wording: %q", old)
		}
	}
}
