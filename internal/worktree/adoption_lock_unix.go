//go:build unix

package worktree

import (
	"context"
	"errors"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

type adoptionFileLock struct {
	file *os.File
}

func acquireAdoptionFileLock(ctx context.Context, path string) (*adoptionFileLock, error) {
	if ctx == nil {
		return nil, errors.New("Worktree registry lock requires a context")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return &adoptionFileLock{file: file}, nil
		}
		if err != unix.EWOULDBLOCK && err != unix.EAGAIN {
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
	return os.Open(path)
}

func openRegularWorktreeFile(path string, expected os.FileInfo) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("open Worktree file descriptor")
	}
	actual, err := file.Stat()
	if err != nil || !actual.Mode().IsRegular() || !os.SameFile(expected, actual) || actual.Size() != expected.Size() {
		_ = file.Close()
		if err != nil {
			return nil, err
		}
		return nil, errors.New("Worktree file changed or is not a regular file")
	}
	return file, nil
}

func (l *adoptionFileLock) release() {
	if l == nil || l.file == nil {
		return
	}
	_ = unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
	_ = l.file.Close()
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
