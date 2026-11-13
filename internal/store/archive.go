package store

import (
	"encoding/json"

	"github.com/hiono/taxion/internal/core"
)

func archivedGraphContent(graph core.TaskGraph, ids map[string]bool) (string, error) {
	if len(ids) == 0 {
		return "", nil
	}
	saved := core.TaskGraph{Version: graph.Version}
	dispatchIDs := map[string]bool{}
	for _, task := range graph.Tasks {
		if ids[task.ID] {
			saved.Tasks = append(saved.Tasks, task)
		}
	}
	for _, dispatch := range graph.Dispatches {
		if ids[dispatch.TaskID] {
			saved.Dispatches = append(saved.Dispatches, dispatch)
			dispatchIDs[dispatch.ID] = true
		}
	}
	for _, flow := range graph.ReviewFlows {
		if ids[flow.TaskID] {
			saved.ReviewFlows = append(saved.ReviewFlows, flow)
		}
	}
	for _, event := range graph.History {
		if ids[event.TaskID] {
			saved.History = append(saved.History, event)
		}
	}
	for _, event := range graph.DispatchHistory {
		if dispatchIDs[event.DispatchID] {
			saved.DispatchHistory = append(saved.DispatchHistory, event)
		}
	}
	data, err := json.Marshal(saved)
	return string(data), err
}
