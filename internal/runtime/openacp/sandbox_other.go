//go:build !linux

package openacp

import (
	"context"
	"fmt"
	"os/exec"
)

func sandboxCommand(context.Context, string, string, bool) (*exec.Cmd, error) {
	return nil, fmt.Errorf("openacp: isolated execution currently requires Linux user, mount and PID namespaces")
}
