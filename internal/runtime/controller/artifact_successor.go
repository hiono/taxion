package controller

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/hiono/taxion/internal/core"
)

func artifactPredecessorDecision(graph core.TaskGraph, task core.GraphTask) (*core.TaskDecision, error) {
	for _, flow := range graph.ReviewFlows {
		if task.CreatedFrom != "flow:"+flow.ID || flow.Artifact == nil || len(flow.DispatchIDs) != 0 {
			continue
		}
		if task.Supersedes != flow.TaskID || flow.Status != "STOPPED" || flow.StopCode != "SAFETY_STOP" ||
			flow.RunnerID != "" || !flow.HandoffHeld() || flow.Artifact.Validation.Status != "FAILED" {
			return nil, fmt.Errorf("taxion: artifact successor requires a held terminal FAILED input validation with no runner or Dispatch")
		}
		previous, err := os.Stat(flow.Contract.Worktree)
		if err != nil {
			return nil, fmt.Errorf("taxion: inspect failed input worktree: %w", err)
		}
		next, err := os.Stat(task.Worktree)
		if err != nil {
			return nil, fmt.Errorf("taxion: inspect successor worktree: %w", err)
		}
		if os.SameFile(previous, next) {
			return nil, fmt.Errorf("taxion: artifact successor requires a separate worktree preserving the failed input")
		}
		hash, err := core.CanonicalSHA256(flow.Artifact)
		if err != nil {
			return nil, err
		}
		text, err := json.Marshal(struct {
			FlowID       string `json:"flowId"`
			ArtifactHash string `json:"artifactHash"`
			ReceiptHash  string `json:"receiptHash"`
		}{flow.ID, flow.Artifact.Target.Hash, "sha256:" + hash})
		if err != nil {
			return nil, err
		}
		return &core.TaskDecision{ID: "artifact-predecessor:" + task.ID, Source: "flow:" + flow.ID, Text: string(text)}, nil
	}
	return nil, nil
}
