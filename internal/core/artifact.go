package core

import (
	"encoding/json"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"time"
)

// ArtifactTarget identifies input, not a successful implementation or quality verdict.
type ArtifactTarget struct {
	Version      int      `json:"version"`
	Repository   string   `json:"repository"`
	BaselineSHA  string   `json:"baselineSha"`
	CommitSHA    string   `json:"commitSha"`
	TreeSHA      string   `json:"treeSha"`
	ChangedFiles []string `json:"changedFiles"`
	Source       string   `json:"source"`
	Hash         string   `json:"hash"`
}

func (a ArtifactTarget) Verify() error {
	hash := a.Hash
	a.Hash = ""
	digest, err := CanonicalSHA256(a)
	if err != nil || hash != "sha256:"+digest || a.Version != 1 || !nonempty(a.Source) ||
		!nonempty(a.Repository) || !validSHA(a.BaselineSHA) || a.CommitSHA != a.BaselineSHA ||
		!validSHA(a.TreeSHA) || !validList(a.ChangedFiles) {
		return graphError("invalid input artifact identity")
	}
	return nil
}

type ArtifactValidation struct {
	Status     string               `json:"status"`
	TargetHash string               `json:"targetHash"`
	StartedAt  time.Time            `json:"startedAt"`
	FinishedAt time.Time            `json:"finishedAt"`
	Commands   []DispatchValidation `json:"commands"`
	Error      string               `json:"error,omitempty"`
}

type ArtifactReview struct {
	ValidationSourceFlowID string                `json:"validationSourceFlowId,omitempty"`
	Target                 ArtifactTarget        `json:"target"`
	Configuration          ApprovalConfiguration `json:"configuration"`
	ObservedAt             time.Time             `json:"observedAt"`
	Validation             ArtifactValidation    `json:"validation"`
}

// StoppedArtifactReviewer identifies preparation failure, not unknown model work.
func (f ReviewFlow) StoppedArtifactReviewer(d GraphDispatch) bool {
	return f.Status == "STOPPED" && f.StopCode == "SAFETY_STOP" && f.HandoffHeld() &&
		f.RunnerID == "" && f.Artifact != nil && f.Artifact.Validation.Status == "PASSED" &&
		len(f.DispatchIDs) == 1 && f.DispatchIDs[0] == d.ID &&
		d.Recipient == "REVIEWER" && d.TaskID != f.TaskID && d.Status == DispatchIssued &&
		!d.Acknowledged && !d.ExecutionStarted && d.RuntimeSessionID == "" && d.Result == nil &&
		d.RuntimeConfigHash == "" && d.ExecutionStartSHA == "" && len(d.RuntimeUsage) == 0 &&
		d.ReviewWorktreeSource == ""
}

func cloneArtifactReview(a *ArtifactReview) *ArtifactReview {
	if a == nil {
		return nil
	}
	copy := *a
	copy.Target.ChangedFiles = slices.Clone(a.Target.ChangedFiles)
	copy.Configuration.Selection = slices.Clone(a.Configuration.Selection)
	copy.Configuration.Sources = maps.Clone(a.Configuration.Sources)
	copy.Validation.Commands = slices.Clone(a.Validation.Commands)
	return &copy
}

// LastDispatchID is empty during the authorized, non-model input validation stage.
func (f ReviewFlow) LastDispatchID() string {
	if len(f.DispatchIDs) == 0 {
		return ""
	}
	return f.DispatchIDs[len(f.DispatchIDs)-1]
}

func (f ReviewFlow) ReviewFirst() bool { return f.SourceDispatchID != "" || f.Artifact != nil }

func (f ReviewFlow) ArtifactDecisionID() string {
	id := "input-artifact:" + f.Artifact.Target.Hash
	if f.Artifact.ValidationSourceFlowID != "" {
		id += ":" + f.ID
	}
	return id
}

func (a ArtifactReview) BasisHash() (string, error) {
	return CanonicalSHA256(struct {
		ValidationSourceFlowID string                `json:"validationSourceFlowId,omitempty"`
		Target                 ArtifactTarget        `json:"target"`
		Configuration          ApprovalConfiguration `json:"configuration"`
	}{a.ValidationSourceFlowID, a.Target, a.Configuration})
}

func (g TaskGraph) validateArtifactFlow(f ReviewFlow) error {
	a := f.Artifact
	if a == nil || a.Target.Verify() != nil || a.ObservedAt.IsZero() ||
		a.Target.Repository != f.Contract.Repo || a.Target.BaselineSHA != f.Contract.BaselineSHA ||
		f.Contract.OutputMode != OutputModeWorktreeTreeSnapshot ||
		f.SourceDispatchID != "" || f.MaxRepairs != 0 || f.Repairs != 0 ||
		f.Budget.MaxCoderSessions != 0 || f.Budget.MaxDispatches != 1 || f.Budget.MaxReviewerSessions != 1 ||
		len(f.DispatchIDs) > 1 || f.StagePlan == nil {
		return graphError("invalid artifact-first review contract")
	}
	v := a.Validation
	if a.ValidationSourceFlowID != "" {
		prior, ok := g.ReviewFlow(a.ValidationSourceFlowID)
		if !ok || prior.ID == f.ID || prior.Status != "STOPPED" || prior.RunnerID != "" ||
			prior.Artifact == nil || prior.Artifact.Validation.Status != "PASSED" ||
			f.Contract.Supersedes != prior.TaskID || f.Contract.CreatedFrom != "flow:"+prior.ID ||
			!f.StartedAt.After(prior.StartedAt) {
			return graphError("invalid inherited artifact validation source")
		}
		expected := *prior.Artifact
		expected.ValidationSourceFlowID = prior.ID
		if !reflect.DeepEqual(*a, expected) ||
			!slices.Equal(f.Contract.ValidationCommands, prior.Contract.ValidationCommands) ||
			f.Contract.Worktree != prior.Contract.Worktree {
			return graphError("inherited artifact validation differs from its original receipt")
		}
	}
	if v.TargetHash != a.Target.Hash ||
		!slices.Contains([]string{"PENDING", "RUNNING", "PASSED", "FAILED"}, v.Status) ||
		v.Status == "PENDING" && !v.StartedAt.IsZero() ||
		(v.Status == "PENDING" || v.Status == "RUNNING") && (len(v.Commands) != 0 || !v.FinishedAt.IsZero() || v.Error != "") ||
		v.Status != "PENDING" && v.StartedAt.IsZero() ||
		v.Status != "PENDING" && v.StartedAt.Before(a.ObservedAt) ||
		(v.Status == "PASSED" || v.Status == "FAILED") && (v.FinishedAt.IsZero() || v.FinishedAt.Before(v.StartedAt)) ||
		v.Status == "PASSED" && (v.Error != "" || len(v.Commands) != len(f.Contract.ValidationCommands)) ||
		v.Status == "FAILED" && !nonempty(v.Error) ||
		v.Status != "PASSED" && len(f.DispatchIDs) != 0 {
		return graphError("invalid artifact validation receipt")
	}
	for i, check := range v.Commands {
		if i >= len(f.Contract.ValidationCommands) || check.Command != f.Contract.ValidationCommands[i] ||
			check.Worktree != f.Contract.Worktree || check.SHA != a.Target.TreeSHA ||
			check.StartedAt.Before(v.StartedAt) || check.FinishedAt.Before(check.StartedAt) ||
			!v.FinishedAt.IsZero() && check.FinishedAt.After(v.FinishedAt) ||
			v.Status == "PASSED" && check.ExitCode != 0 {
			return graphError("artifact validation differs from its approved commands or target")
		}
	}
	if len(f.DispatchIDs) != 0 {
		d, ok := g.dispatch(f.LastDispatchID())
		review, reviewExists := g.task(d.TaskID)
		if !ok || d.Recipient != "REVIEWER" || d.Relationship != RelationshipNone ||
			!reviewExists || d.TaskID == f.TaskID || review.CreatedFrom != "artifact:"+a.Target.Hash ||
			d.Instruction.BaselineSHA != a.Target.CommitSHA ||
			filepath.Dir(d.Instruction.Worktree) != f.ReviewRoot ||
			d.RelatedID != "" || d.Instruction.TargetTreeSHA != a.Target.TreeSHA ||
			d.Instruction.TargetSHA != a.Target.CommitSHA ||
			!slices.Equal(d.Instruction.TargetChangedFiles, a.Target.ChangedFiles) {
			return graphError("artifact review Dispatch differs from its validated target")
		}
		found := false
		for _, decision := range d.Instruction.Decisions {
			if decision.ID != f.ArtifactDecisionID() {
				continue
			}
			var receipt ArtifactReview
			if json.Unmarshal([]byte(decision.Text), &receipt) != nil || !equalJSON(receipt, *a) || decision.Source != "flow:"+f.ID {
				return graphError("artifact Reviewer requires its exact input validation receipt")
			}
			found = true
		}
		if !found {
			return graphError("artifact Reviewer input provenance is missing")
		}
	}
	return nil
}
