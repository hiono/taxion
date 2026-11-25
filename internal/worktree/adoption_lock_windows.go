//go:build windows

package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

func openWorktreeIndexLockFile(path string) (*os.File, error) {
	handle, err := windows.CreateFile(
		windows.StringToUTF16Ptr(path),
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, errors.New("open Task Worktree Git index lock handle")
	}
	return file, nil
}


func openRegularWorktreeFile(root *os.Root, rootPath, relative string) (*os.File, os.FileInfo, error) {
	_ = rootPath
	if root == nil || relative == "" || filepath.IsAbs(relative) || filepath.Clean(relative) != relative {
		return nil, nil, errors.New("Worktree file path must be relative to its root")
	}
	initialInfo, err := root.Lstat(relative)
	if err != nil {
		return nil, nil, err
	}
	if !initialInfo.Mode().IsRegular() {
		return nil, nil, errors.New("Worktree file is not a regular file")
	}
	file, err := root.Open(relative)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, errors.New("Worktree file is not a regular file")
	}
	pathInfo, err := root.Lstat(relative)
	if err != nil || !pathInfo.Mode().IsRegular() || !os.SameFile(info, pathInfo) {
		_ = file.Close()
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, errors.New("Worktree file changed while it was being opened")
	}
	return file, info, nil
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
