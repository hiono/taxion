package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func defaultWorkspaceStorePath() (string, error) {
	directory, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for ancestor := directory; ; ancestor = filepath.Dir(ancestor) {
		_, err := os.Lstat(filepath.Join(ancestor, ".git"))
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
			command.Dir = directory
			for _, variable := range os.Environ() {
				if !strings.HasPrefix(variable, "GIT_") {
					command.Env = append(command.Env, variable)
				}
			}
			output, err := command.Output()
			if err != nil {
				return "", fmt.Errorf("resolve default workspace from Git: %w; use --store only for an intentional override", err)
			}
			root := strings.TrimSuffix(string(output), "\n")
			if !filepath.IsAbs(root) {
				return "", fmt.Errorf("Git did not return an absolute worktree root")
			}
			return filepath.Join(root, ".taxion", "ledger.json"), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if filepath.Dir(ancestor) == ancestor {
			// Preserve standalone workspaces outside Git repositories.
			return filepath.Join(directory, ".taxion", "ledger.json"), nil
		}
	}
}

func workspaceDBPath() string {
	return filepath.Join(filepath.Dir(storePath), "workspace.db")
}

func validateWorkspaceStorePath() error {
	ledgerPath, err := filepath.Abs(storePath)
	if err != nil {
		return err
	}
	dbPath, err := filepath.Abs(workspaceDBPath())
	if err != nil {
		return err
	}
	collision := ledgerPath == dbPath ||
		(runtime.GOOS == "windows" && strings.EqualFold(ledgerPath, dbPath))
	var files [2]os.FileInfo
	for index, path := range []string{ledgerPath, dbPath} {
		files[index], err = os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			// A dangling link may become a collision when SQLite creates its target.
			info, linkErr := os.Lstat(path)
			if linkErr == nil && info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("--store or workspace database has an unresolved symlink; select a separate ledger.json path")
			}
			if linkErr != nil && !errors.Is(linkErr, os.ErrNotExist) {
				return linkErr
			}
			continue
		}
		if err != nil {
			return err
		}
	}
	if collision || (files[0] != nil && files[1] != nil && os.SameFile(files[0], files[1])) {
		return fmt.Errorf("--store selects the legacy ledger path, not the SQLite database; use a separate ledger.json path in the workspace directory, not workspace.db")
	}
	return nil
}

func openWorkspace() (*store.Store, error) {
	if err := validateWorkspaceStorePath(); err != nil {
		return nil, err
	}
	db, err := store.Open(workspaceDBPath())
	if err != nil {
		return nil, err
	}
	initialized, err := db.WorkspaceInitialized()
	if err != nil {
		db.Close()
		return nil, err
	}
	if initialized {
		return db, nil
	}
	ledger, err := core.LoadLedger(storePath)
	if errors.Is(err, os.ErrNotExist) {
		db.Close()
		return nil, fmt.Errorf("workspace not initialized (run 'taxion init' first)")
	}
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("cannot migrate legacy ledger %s: %w", storePath, err)
	}
	var graph core.TaskGraph
	hasGraph := false
	graphPath := core.GraphPath(storePath)
	graph, err = core.LoadTaskGraph(graphPath)
	if err == nil {
		hasGraph = true
		if err := graph.CheckLedger(ledger); err != nil {
			db.Close()
			return nil, fmt.Errorf("cannot migrate legacy task graph: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		db.Close()
		return nil, fmt.Errorf("cannot migrate legacy task graph: %w", err)
	}
	_, err = db.InitializeWorkspace(store.Workspace{Ledger: ledger, Graph: graph, HasGraph: hasGraph})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate workspace to SQLite: %w", err)
	}
	return db, nil
}

func loadWorkspace() (*store.Store, store.Workspace, error) {
	db, err := openWorkspace()
	if err != nil {
		return nil, store.Workspace{}, err
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		db.Close()
		return nil, store.Workspace{}, err
	}
	return db, state, nil
}

func updateWorkspace(update func(*store.Workspace) error) error {
	db, err := openWorkspace()
	if err != nil {
		return err
	}
	defer db.Close()
	return db.UpdateWorkspace(update)
}
