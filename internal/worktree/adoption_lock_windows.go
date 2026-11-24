//go:build windows

package worktree

import (
	"context"
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

type adoptionFileLock struct {
	file       *os.File
	overlapped windows.Overlapped
}

func acquireAdoptionFileLock(ctx context.Context, path string) (*adoptionFileLock, error) {
	if ctx == nil {
		return nil, errors.New("Worktree registry lock requires a context")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	lock := &adoptionFileLock{file: file}
	for {
		err = windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &lock.overlapped)
		if err == nil {
			return lock, nil
		}
		if err != windows.ERROR_LOCK_VIOLATION {
			_ = file.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (l *adoptionFileLock) release() {
	if l == nil || l.file == nil {
		return
	}
	_ = windows.UnlockFileEx(windows.Handle(l.file.Fd()), 0, 1, 0, &l.overlapped)
	_ = l.file.Close()
}

func syncDirectory(string) error {
	// Windows does not support opening a directory handle for FlushFileBuffers.
	// The record file itself is flushed before its atomic link is published.
	return nil
}
