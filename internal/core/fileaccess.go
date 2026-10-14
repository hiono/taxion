package core

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

type FileAccessPolicy struct {
	Level string   `json:"level"`
	Paths []string `json:"paths,omitempty"`
}

const (
	FileAccessExact     = "exact"
	FileAccessCommitted = "committed"
	FileAccessAll       = "all"
)

func DefaultFileAccessPolicy() *FileAccessPolicy {
	return &FileAccessPolicy{Level: FileAccessCommitted}
}

func (p FileAccessPolicy) Validate() error {
	switch p.Level {
	case FileAccessExact:
		if len(p.Paths) == 0 || !validList(p.Paths) {
			return fmt.Errorf("exact file access requires unique relative paths")
		}
		for _, path := range p.Paths {
			if filepath.IsAbs(filepath.FromSlash(path)) || filepath.VolumeName(filepath.FromSlash(path)) != "" ||
				strings.ContainsAny(path, "\\*?[") || strings.ContainsRune(path, '\x00') ||
				filepath.ToSlash(filepath.Clean(filepath.FromSlash(path))) != path ||
				path == "." || path == ".." || strings.HasPrefix(path, "../") || path == ".git" ||
				strings.HasPrefix(path, ".git/") {
				return fmt.Errorf("exact file access path %q must be a normalized repository-relative file path", path)
			}
		}
	case FileAccessCommitted, FileAccessAll:
		if len(p.Paths) != 0 {
			return fmt.Errorf("%s file access does not accept explicit paths", p.Level)
		}
	default:
		return fmt.Errorf("unknown file access level %q", p.Level)
	}
	return nil
}

func CloneFileAccessPolicy(policy *FileAccessPolicy) *FileAccessPolicy {
	if policy == nil {
		return nil
	}
	copy := *policy
	copy.Paths = slices.Clone(policy.Paths)
	return &copy
}
