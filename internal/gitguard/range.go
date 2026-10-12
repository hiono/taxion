package gitguard

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// CheckRange scans every unpublished commit tree and message using a private index.
func CheckRange(ctx context.Context, directory, base, head, scanner string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	for _, sha := range []string{base, head} {
		if _, err := hex.DecodeString(sha); err != nil || len(sha) != 40 && len(sha) != 64 {
			return fmt.Errorf("publication scan requires full base and head SHAs")
		}
	}
	temporary, err := os.MkdirTemp("", "taxion-range-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	var env []string
	for _, variable := range os.Environ() {
		if !strings.HasPrefix(variable, "GIT_") {
			env = append(env, variable)
		}
	}
	env = append(env, "GIT_INDEX_FILE="+filepath.Join(temporary, "index"),
		"GIT_NO_REPLACE_OBJECTS=1", "GIT_NO_LAZY_FETCH=1")
	git := func(args ...string) (string, error) {
		command := exec.CommandContext(ctx, "git", append([]string{"-C", directory}, args...)...)
		command.Env = env
		data, err := command.Output()
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if err != nil {
			return "", fmt.Errorf("publication range Git inspection failed: %w", err)
		}
		return strings.TrimSuffix(string(data), "\n"), nil
	}
	if _, err := git("merge-base", "--is-ancestor", base, head); err != nil {
		return fmt.Errorf("publication base must be an ancestor of head: %w", err)
	}
	commits, err := git("rev-list", base+".."+head)
	if err != nil {
		return err
	}
	if commits == "" {
		return fmt.Errorf("publication range contains no unpublished commits")
	}
	for _, sha := range strings.Fields(commits) {
		if _, err := git("read-tree", sha); err != nil {
			return err
		}
		message, err := git("show", "-s", "--format=%B", sha)
		if err != nil {
			return err
		}
		if err := checkSecrets(ctx, directory, scanner, env, message); err != nil {
			return fmt.Errorf("unpublished commit %s: %w", sha, err)
		}
	}
	return nil
}
