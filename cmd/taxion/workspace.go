package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func workspaceDBPath() string {
	return filepath.Join(filepath.Dir(storePath), "workspace.db")
}

func openWorkspace() (*store.Store, error) {
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
