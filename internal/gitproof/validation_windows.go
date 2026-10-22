package gitproof

import (
	"fmt"
	"os/exec"
)

func configureValidationProcess(_ *exec.Cmd) error {
	return fmt.Errorf("controller validation requires Unix process-group cancellation; Windows execution is not supported")
}
