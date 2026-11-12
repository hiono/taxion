package controller

import (
	"fmt"
	"strings"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

// AppendJobMessage records a Role-to-Role exchange on the original Job.
// It does not grant authority, alter approval scope, or dispatch work.
func AppendJobMessage(db *store.Store, message core.JobMessage) error {
	if db == nil {
		return fmt.Errorf("taxion: Job collaboration requires a workspace")
	}
	return db.UpdateWorkspace(func(state *store.Workspace) error {
		if !state.HasGraph {
			return fmt.Errorf("taxion: Job collaboration requires a Task Graph")
		}
		if strings.TrimSpace(message.Source) == "" {
			return fmt.Errorf("taxion: Job collaboration requires source attribution")
		}
		return state.Graph.AppendJobMessage(message)
	})
}

// JobConversation reads the append-only collaboration history for one Job.
func JobConversation(db *store.Store, jobID string) ([]core.JobMessage, error) {
	if db == nil || strings.TrimSpace(jobID) == "" {
		return nil, fmt.Errorf("taxion: Job conversation requires a workspace and Job ID")
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		return nil, err
	}
	if !state.HasGraph {
		return nil, fmt.Errorf("taxion: Job conversation requires a Task Graph")
	}
	if _, ok := state.Graph.Job(jobID); !ok {
		// Legacy conversations are addressed by their former TaskID until
		// the workspace is explicitly associated with a first-class Job.
		if _, legacy := state.Graph.GraphTask(jobID); !legacy {
			return nil, fmt.Errorf("taxion: Job conversation does not exist")
		}
	}
	return state.Graph.JobConversation(jobID), nil
}
