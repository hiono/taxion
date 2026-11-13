//go:build !windows

package gitproof

import (
	"os/exec"
	"syscall"
)

func configureValidationProcess(command *exec.Cmd) error {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
	return nil
}
