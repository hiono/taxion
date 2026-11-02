package store

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
)

// InputObservation is a newly acquired fact, never a replacement DispatchResult.
type InputObservation struct {
	Version                    int                 `json:"version"`
	Hash                       string              `json:"hash"`
	Contract                   core.GraphTask      `json:"contract"`
	Configuration              json.RawMessage     `json:"configuration"`
	SourceTaskID               string              `json:"sourceTaskId"`
	SourceFlowID               string              `json:"sourceFlowId,omitempty"`
	SourceDispatchID           string              `json:"sourceDispatchId,omitempty"`
	SourceResultHash           string              `json:"sourceResultHash,omitempty"`
	SourceArtifactHash         string              `json:"sourceArtifactHash,omitempty"`
	Target                     core.ArtifactTarget `json:"target"`
	PriorPolicy                core.InputDecision  `json:"priorPolicy"`
	Actor                      string              `json:"actor"`
	Source                     string              `json:"source"`
	Reason                     string              `json:"reason"`
	Evidence                   []string            `json:"evidence"`
	NoActiveExecutionConfirmed bool                `json:"noActiveExecutionConfirmed"`
	AcceptDeliveryUncertainty  bool                `json:"acceptDeliveryUncertainty"`
	ObservedAt                 time.Time           `json:"observedAt"`
}

func (o InputObservation) ContentHash() (string, error) {
	o.Hash, o.ObservedAt = "", time.Time{}
	return core.CanonicalSHA256(o)
}

func (r Records) InputObservation(hash string) (InputObservation, bool) {
	for _, observation := range r.InputObservations {
		if hash != "" && observation.Hash == hash {
			return observation, true
		}
	}
	return InputObservation{}, false
}

func (r Records) VerifyInputObservations(graph core.TaskGraph) error {
	seen := map[string]bool{}
	for _, o := range r.InputObservations {
		hash, err := o.ContentHash()
		if o.Version != 1 || err != nil || hash != o.Hash || seen[hash] || o.ObservedAt.IsZero() ||
			o.Target.Verify() != nil || o.Contract.Repo != o.Target.Repository ||
			o.Contract.BaselineSHA != o.Target.BaselineSHA || o.Contract.TargetSHA != o.Target.CommitSHA ||
			o.Contract.TargetTreeSHA != o.Target.TreeSHA || !slices.Equal(o.Contract.TargetChangedFiles, o.Target.ChangedFiles) ||
			o.Contract.OutputMode != core.OutputModeWorktreeTreeSnapshot || o.Contract.Worktree == "" ||
			len(o.Contract.Scope) == 0 || !o.NoActiveExecutionConfirmed || !o.AcceptDeliveryUncertainty ||
			len(o.Evidence) == 0 || o.Actor == "" || o.Source == "" || o.Reason == "" {
			return fmt.Errorf("invalid input observation or content hash")
		}
		for _, text := range append([]string{o.Actor, o.Source, o.Reason}, o.Evidence...) {
			if strings.TrimSpace(text) != text || text == "" || core.Redact(text) != text {
				return fmt.Errorf("input observation requires sourced non-secret evidence")
			}
		}
		if _, ok := graph.GraphTask(o.SourceTaskID); !ok {
			return fmt.Errorf("input observation source Task is missing")
		}
		if o.SourceFlowID != "" {
			flow, ok := graph.ReviewFlow(o.SourceFlowID)
			if !ok || flow.TaskID != o.SourceTaskID ||
				o.SourceDispatchID != "" && !slices.Contains(flow.DispatchIDs, o.SourceDispatchID) {
				return fmt.Errorf("input observation source Flow differs")
			}
			if flow.Artifact != nil {
				hash, err := core.CanonicalSHA256(flow.Artifact)
				if err != nil || hash != o.SourceArtifactHash {
					return fmt.Errorf("input observation source validation receipt differs")
				}
			} else if o.SourceArtifactHash != "" {
				return fmt.Errorf("input observation source validation receipt is missing")
			}
		}
		if o.SourceDispatchID != "" {
			if o.SourceFlowID == "" {
				return fmt.Errorf("input observation cannot omit its source Flow metadata")
			}
			dispatch, ok := graph.GraphDispatch(o.SourceDispatchID)
			resultHash, err := core.CanonicalSHA256(dispatch.Result)
			if !ok || err != nil || resultHash != o.SourceResultHash || dispatch.Result == nil {
				return fmt.Errorf("input observation source result differs")
			}
		}
		seen[hash] = true
	}
	return nil
}
