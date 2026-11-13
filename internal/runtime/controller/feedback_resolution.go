package controller

import (
	"fmt"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

type FeedbackResolutionRequest struct {
	FlowID     string `json:"flowId"`
	FeedbackID string `json:"feedbackId"`
	core.FeedbackResolution
}

func ResolveFeedback(db *store.Store, request FeedbackResolutionRequest) (core.ReviewFlow, error) {
	if db == nil {
		return core.ReviewFlow{}, fmt.Errorf("taxion: feedback resolution requires a workspace")
	}
	var flow core.ReviewFlow
	err := db.UpdateWorkspace(func(state *store.Workspace) error {
		if !state.HasGraph {
			return fmt.Errorf("taxion: feedback resolution requires a Task Graph")
		}
		var err error
		flow, err = state.Graph.ResolveFlowFeedback(request.FlowID, request.FeedbackID, request.FeedbackResolution)
		return err
	})
	return flow, err
}
