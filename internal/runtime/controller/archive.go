package controller

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

type ArchiveRequest struct {
	TaskIDs      []string `json:"taskIds"`
	Actor        string   `json:"actor"`
	Source       string   `json:"source"`
	Reason       string   `json:"reason"`
	ExpectedHash string   `json:"expectedHash,omitempty"`
}

type ArchivePreview struct {
	TaskIDs     []string `json:"taskIds"`
	FlowIDs     []string `json:"flowIds"`
	DispatchIDs []string `json:"dispatchIds"`
	Hash        string   `json:"hash"`
}

func previewArchive(state store.Workspace, request ArchiveRequest) (ArchivePreview, error) {
	if !state.HasGraph || strings.TrimSpace(request.Actor) == "" ||
		strings.TrimSpace(request.Source) == "" || strings.TrimSpace(request.Reason) == "" {
		return ArchivePreview{}, fmt.Errorf("taxion: archive requires tracked Tasks, actor, source and reason")
	}
	ids, err := state.Graph.ArchiveScope(request.TaskIDs)
	if err != nil {
		return ArchivePreview{}, err
	}
	preview := ArchivePreview{TaskIDs: ids, FlowIDs: []string{}, DispatchIDs: []string{}}
	for _, dispatch := range state.Graph.Dispatches {
		if slices.Contains(ids, dispatch.TaskID) {
			preview.DispatchIDs = append(preview.DispatchIDs, dispatch.ID)
		}
	}
	for _, dispatch := range state.Ledger.Dispatches {
		if slices.Contains(ids, dispatch.TaskID) {
			if dispatch.Active {
				return ArchivePreview{}, fmt.Errorf("taxion: archive requires terminal legacy Dispatch %s", dispatch.ID)
			}
			if !slices.Contains(preview.DispatchIDs, dispatch.ID) {
				preview.DispatchIDs = append(preview.DispatchIDs, dispatch.ID)
			}
		}
	}
	for _, flow := range state.Graph.ReviewFlows {
		if slices.Contains(ids, flow.TaskID) {
			preview.FlowIDs = append(preview.FlowIDs, flow.ID)
		}
	}
	for _, update := range state.Records.Updates {
		if slices.Contains(ids, update.TaskID) && update.Status != "SENT" {
			return ArchivePreview{}, fmt.Errorf("taxion: archive requires settling external publication %s (%s)", update.ID, update.Status)
		}
	}
	request.ExpectedHash = ""
	request.TaskIDs = ids
	content, err := json.Marshal(struct {
		State   store.Workspace `json:"state"`
		Request ArchiveRequest  `json:"request"`
	}{state, request})
	if err != nil {
		return ArchivePreview{}, err
	}
	preview.Hash = fmt.Sprintf("sha256:%x", sha256.Sum256(content))
	return preview, nil
}

// ArchiveTasks previews without mutation, or commits the exact reviewed state
// within the same transaction that checks execution and publication ownership.
func ArchiveTasks(db *store.Store, request ArchiveRequest, dryRun bool) (ArchivePreview, error) {
	if db == nil {
		return ArchivePreview{}, fmt.Errorf("taxion: archive requires a workspace")
	}
	if dryRun {
		state, err := db.LoadWorkspace()
		if err != nil {
			return ArchivePreview{}, err
		}
		return previewArchive(state, request)
	}
	var preview ArchivePreview
	err := db.UpdateWorkspace(func(state *store.Workspace) error {
		var err error
		preview, err = previewArchive(*state, request)
		if err != nil {
			return err
		}
		if request.ExpectedHash != preview.Hash {
			return fmt.Errorf("taxion: archive preview changed or --expected-hash is missing; review --dry-run again")
		}
		state.Graph.Archives = append(state.Graph.Archives, core.TaskArchive{
			TaskIDs: slices.Clone(preview.TaskIDs), Actor: request.Actor, Source: request.Source,
			Reason: request.Reason, ExpectedHash: request.ExpectedHash, At: time.Now().UTC(),
		})
		return nil
	})
	return preview, err
}
