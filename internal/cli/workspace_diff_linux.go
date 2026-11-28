//go:build linux

package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/sys/unix"
)

func openWorkspaceRoot(directory string) (*os.File, error) {
	fd, err := unix.Open(directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), directory), nil
}

func readWorkspaceFile(ctx context.Context, root *os.File, path string, submodule bool) (workspaceFile, bool, error) {
	parts := strings.Split(path, "/")
	parent := int(root.Fd())
	var opened []int
	defer func() {
		for _, fd := range opened {
			unix.Close(fd)
		}
	}()
	for _, part := range parts[:len(parts)-1] {
		if part == "" || part == "." || part == ".." {
			return workspaceFile{}, false, errors.New("invalid workspace path")
		}
		fd, err := unix.Openat(parent, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.ELOOP) {
			return workspaceFile{}, false, nil
		}
		if err != nil {
			return workspaceFile{}, false, err
		}
		opened = append(opened, fd)
		parent = fd
	}
	name := parts[len(parts)-1]
	if name == "" || name == "." || name == ".." {
		return workspaceFile{}, false, errors.New("invalid workspace path")
	}
	fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ENOTDIR) {
		return workspaceFile{}, false, nil
	}
	if errors.Is(err, unix.ELOOP) {
		buf := make([]byte, maxWorkspaceDiffBytes+1)
		n, err := unix.Readlinkat(parent, name, buf)
		if err != nil {
			return workspaceFile{}, false, err
		}
		f, err := snapshotData(strings.NewReader(string(buf[:n])), "120000")
		return f, true, err
	}
	if err != nil {
		return workspaceFile{}, false, err
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return workspaceFile{}, false, err
	}
	if info.IsDir() && submodule {
		var gitInfo unix.Stat_t
		err := unix.Fstatat(fd, ".git", &gitInfo, unix.AT_SYMLINK_NOFOLLOW)
		if errors.Is(err, unix.ENOENT) {
			f, err := snapshotData(strings.NewReader("uninitialized"), "160000")
			return f, true, err
		}
		if err != nil {
			return workspaceFile{}, false, err
		}
		if gitInfo.Mode&unix.S_IFMT == unix.S_IFLNK {
			return workspaceFile{}, false, errors.New("submodule .git is a symlink")
		}
		commit, err := workspaceGit(ctx, fmt.Sprintf("/proc/self/fd/%d", fd), false, "rev-parse", "--verify", "HEAD")
		if err != nil {
			return workspaceFile{}, false, fmt.Errorf("read submodule HEAD: %w", err)
		}
		f, err := snapshotData(strings.NewReader(strings.TrimSpace(commit)), "160000")
		return f, true, err
	}
	if !info.Mode().IsRegular() {
		return workspaceFile{}, false, fmt.Errorf("unsupported file type: %s", info.Mode())
	}
	mode := "100644"
	if info.Mode()&0111 != 0 {
		mode = "100755"
	}
	f, err := snapshotData(file, mode)
	return f, true, err
}

func diffWorkspaceBytes(ctx context.Context, old, now []byte, before, after bool) (string, error) {
	files := make([]*os.File, 0, 2)
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()
	for _, data := range [][]byte{old, now} {
		fd, err := unix.MemfdCreate("workspace-diff", unix.MFD_CLOEXEC)
		if err != nil {
			return "", err
		}
		f := os.NewFile(uintptr(fd), "workspace-diff")
		files = append(files, f)
		if _, err := f.Write(data); err != nil {
			return "", err
		}
		if _, err := f.Seek(0, 0); err != nil {
			return "", err
		}
	}
	a, b := "/proc/self/fd/3", "/proc/self/fd/4"
	if !before {
		a = os.DevNull
	}
	if !after {
		b = os.DevNull
	}
	command := exec.CommandContext(ctx, "diff", "-U", "0", "--label", "old", "--label", "new", "--", a, b)
	command.Env = workspaceGitEnvironment()
	command.ExtraFiles = files
	var output, stderr limitedGitOutput
	command.Stdout, command.Stderr = &output, &stderr
	err := command.Run()
	if output.exceeded || stderr.exceeded {
		return "", errors.New("Git output exceeds the input size limit")
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	var exit *exec.ExitError
	if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1 && stderr.buffer.Len() == 0) {
		return "", err
	}
	return output.buffer.String(), nil
}
