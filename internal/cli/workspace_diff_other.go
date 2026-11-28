//go:build !linux

package cli

import (
	"context"
	"errors"
	"os"
)

func openWorkspaceRoot(string) (*os.File, error) {
	return nil, errors.New("safe workspace snapshots are unsupported on this platform")
}
func readWorkspaceFile(context.Context, *os.File, string, bool) (workspaceFile, bool, error) {
	return workspaceFile{}, false, errors.New("safe workspace snapshots are unsupported on this platform")
}
func diffWorkspaceBytes(context.Context, []byte, []byte, bool, bool) (string, error) {
	return "", errors.New("safe workspace snapshots are unsupported on this platform")
}
