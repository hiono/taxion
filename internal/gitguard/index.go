package gitguard

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"
)

// CheckIndex inspects the complete prospective tree, not the working files or
// just the staged diff. Git's environment (including GIT_INDEX_FILE) is retained.
func CheckIndex(ctx context.Context, directory string) error {
	entries, err := readIndex(ctx, directory)
	if err != nil {
		return err
	}
	return checkPaths(entries)
}

type indexEntry struct {
	mode, object, name string
}

func gitEnvironment() []string {
	var environment []string
	for _, variable := range os.Environ() {
		name, _, _ := strings.Cut(variable, "=")
		switch name {
		case "GIT_LITERAL_PATHSPECS", "GIT_GLOB_PATHSPECS", "GIT_NOGLOB_PATHSPECS", "GIT_ICASE_PATHSPECS":
			continue
		}
		environment = append(environment, variable)
	}
	return environment
}

func readIndex(ctx context.Context, directory string) ([]indexEntry, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", "-C", directory, "ls-files", "--cached", "--stage", "--full-name", "-z", "--", ":/")
	// Pathspec environment settings must not turn the root selector into a
	// literal path. Retain the hook's actual repository and index selection.
	command.Env = gitEnvironment()
	output, err := command.Output()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("commit index inspection interrupted: %w", ctx.Err())
	}
	if err != nil {
		return nil, fmt.Errorf("cannot inspect commit index: %w", err)
	}
	var entries []indexEntry
	for _, entry := range bytes.Split(output, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		header, name, ok := bytes.Cut(entry, []byte{'\t'})
		fields := bytes.Fields(header)
		if !ok || len(fields) != 3 {
			return nil, fmt.Errorf("cannot inspect commit index: malformed Git entry")
		}
		if string(fields[2]) != "0" {
			return nil, fmt.Errorf("commit index contains unresolved merge entries")
		}
		object := string(fields[1])
		if _, err := hex.DecodeString(object); err != nil || (len(object) != 40 && len(object) != 64) {
			return nil, fmt.Errorf("cannot inspect commit index: malformed object ID")
		}
		mode := string(fields[0])
		switch mode {
		case "100644", "100755", "120000", "160000":
		default:
			return nil, fmt.Errorf("cannot inspect commit index: unsupported entry mode")
		}
		entries = append(entries, indexEntry{mode: mode, object: object, name: string(name)})
	}
	return entries, nil
}

func checkPaths(entries []indexEntry) error {
	local, dotenv := 0, 0
	for _, entry := range entries {
		filename := entry.name
		for _, component := range strings.Split(filename, "/") {
			if component == ".taxion" {
				local++
				break
			}
		}
		base := path.Base(filename)
		if base == ".env" || strings.HasPrefix(base, ".env.") {
			dotenv++
		}
	}
	if local != 0 || dotenv != 0 {
		return fmt.Errorf("commit index contains prohibited local paths: .taxion entries=%d, dotenv entries=%d; review and unstage local files before committing", local, dotenv)
	}
	return nil
}
