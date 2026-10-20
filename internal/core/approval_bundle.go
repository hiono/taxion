package core

import (
	"strings"
	"time"
)

// ApprovalBundleVersion is the schema version of an approval bundle record.
const ApprovalBundleVersion = 1

// ApprovalStage names an internal transition that must not ask for approval
// again while the approved basis is unchanged.
type ApprovalStage string

const (
	StageReadback    ApprovalStage = "readback"
	StageEligibility ApprovalStage = "eligibility"
	StagePreflight   ApprovalStage = "preflight"
	StageLane        ApprovalStage = "lane"
	StageRolePacket  ApprovalStage = "rolePacket"
)

var approvalStages = []ApprovalStage{StageReadback, StageEligibility, StagePreflight, StageLane, StageRolePacket}

// ApprovalDimensions are the contract dimensions that a single human approval
// covers. Any change to one of them requires a new approval.
type ApprovalDimensions struct {
	Baseline   string `json:"baselineDigest"`
	Scope      string `json:"scopeDigest"`
	Acceptance string `json:"acceptanceDigest"`
	Budget     string `json:"budgetDigest"`
	Permission string `json:"permissionDigest"`
	Security   string `json:"securityDigest"`
}

// ApprovalBundle binds one human approval to a proposed contract and to the
// contract that was actually stored. ProposalHash covers what the human
// approved; RecordHash covers the stored contract after defaults were applied.
// They differ whenever the store applies defaults, which is not drift.
type ApprovalBundle struct {
	Version      int                `json:"version"`
	ID           string             `json:"id"`
	TaskID       string             `json:"taskId"`
	ProposalHash string             `json:"proposalHash"`
	RecordHash   string             `json:"recordHash"`
	Dimensions   ApprovalDimensions `json:"dimensions"`
	BundleHash   string             `json:"bundleHash"`
	Actor        string             `json:"actor"`
	Source       string             `json:"source"`
	Reason       string             `json:"reason"`
	At           time.Time          `json:"at"`
}

// ApprovalStages returns every stage a single approval covers.
func ApprovalStages() []ApprovalStage {
	return append([]ApprovalStage(nil), approvalStages...)
}

// BundleDimensions derives the approved dimensions from a contract.
func BundleDimensions(task GraphTask) (ApprovalDimensions, error) {
	baseline, err := CanonicalSHA256(struct {
		BaselineSHA string `json:"baselineSha"`
	}{task.BaselineSHA})
	if err != nil {
		return ApprovalDimensions{}, err
	}
	scope, err := CanonicalSHA256(struct {
		Scope      []string          `json:"scope"`
		OutOfScope []string          `json:"outOfScope"`
		FileAccess *FileAccessPolicy `json:"fileAccess,omitempty"`
	}{task.Scope, task.OutOfScope, task.FileAccess})
	if err != nil {
		return ApprovalDimensions{}, err
	}
	acceptance, err := CanonicalSHA256(struct {
		Acceptance         []string `json:"acceptance"`
		RequiredEvidence   []string `json:"requiredEvidence"`
		ValidationCommands []string `json:"validationCommands,omitempty"`
		ExitCondition      string   `json:"exitCondition"`
	}{task.Acceptance, task.RequiredEvidence, task.ValidationCommands, task.ExitCondition})
	if err != nil {
		return ApprovalDimensions{}, err
	}
	budget, err := CanonicalSHA256(struct {
		BudgetGuidance *BudgetGuidancePolicy `json:"budgetGuidance,omitempty"`
	}{task.BudgetGuidance})
	if err != nil {
		return ApprovalDimensions{}, err
	}
	permission, err := CanonicalSHA256(struct {
		Owner           string         `json:"owner"`
		Reviewer        string         `json:"reviewer"`
		AuthorizationID string         `json:"authorizationId"`
		Decisions       []TaskDecision `json:"decisions"`
		Worktree        string         `json:"worktree"`
		Branch          string         `json:"branch"`
	}{task.Owner, task.Reviewer, task.AuthorizationID, task.Decisions, task.Worktree, task.Branch})
	if err != nil {
		return ApprovalDimensions{}, err
	}
	security, err := CanonicalSHA256(struct {
		Risk           string          `json:"risk"`
		ExceptionIDs   []string        `json:"exceptionIds"`
		Specifications []Specification `json:"specifications,omitempty"`
		MemoryRefs     []string        `json:"memoryRefs"`
	}{task.Risk, task.ExceptionIDs, task.Specifications, task.MemoryRefs})
	if err != nil {
		return ApprovalDimensions{}, err
	}
	return ApprovalDimensions{
		Baseline:   baseline,
		Scope:      scope,
		Acceptance: acceptance,
		Budget:     budget,
		Permission: permission,
		Security:   security,
	}, nil
}

// ComposeApprovalBundle binds one approval to a proposed contract and to the
// stored contract that a readback produced.
func ComposeApprovalBundle(id string, proposal, stored GraphTask, actor, source, reason string, at time.Time) (ApprovalBundle, error) {
	proposalHash, err := CanonicalSHA256(proposal)
	if err != nil {
		return ApprovalBundle{}, err
	}
	recordHash, err := CanonicalSHA256(stored)
	if err != nil {
		return ApprovalBundle{}, err
	}
	dimensions, err := BundleDimensions(stored)
	if err != nil {
		return ApprovalBundle{}, err
	}
	bundle := ApprovalBundle{
		Version:      ApprovalBundleVersion,
		ID:           id,
		TaskID:       stored.ID,
		ProposalHash: proposalHash,
		RecordHash:   recordHash,
		Dimensions:   dimensions,
		Actor:        actor,
		Source:       source,
		Reason:       reason,
		At:           at.UTC(),
	}
	hash, err := bundle.computeHash()
	if err != nil {
		return ApprovalBundle{}, err
	}
	bundle.BundleHash = hash
	return bundle, nil
}

// Verify reports whether the bundle still authorizes the stored contract.
// A nil error means every covered stage may proceed without a new approval.
func (b ApprovalBundle) Verify(stored GraphTask) error {
	hash, err := b.computeHash()
	if err != nil {
		return err
	}
	if b.BundleHash == "" || hash != b.BundleHash {
		return graphError("approval bundle %s hash mismatch; re-approval required", b.ID)
	}
	if changed := b.Drift(stored); len(changed) > 0 {
		return graphError("approval bundle %s basis changed (%s); re-approval required", b.ID, strings.Join(changed, ", "))
	}
	return nil
}

// Drift lists the approved dimensions that no longer match the stored
// contract. An empty result means the approval is still valid.
func (b ApprovalBundle) Drift(stored GraphTask) []string {
	var changed []string
	recordHash, err := CanonicalSHA256(stored)
	if err != nil || recordHash != b.RecordHash {
		changed = append(changed, "recordHash")
	}
	if b.TaskID != stored.ID {
		changed = append(changed, "taskId")
	}
	current, err := BundleDimensions(stored)
	if err != nil {
		return append(changed, "dimensions")
	}
	for _, dimension := range []struct {
		name     string
		approved string
		current  string
	}{
		{"baseline", b.Dimensions.Baseline, current.Baseline},
		{"scope", b.Dimensions.Scope, current.Scope},
		{"acceptance", b.Dimensions.Acceptance, current.Acceptance},
		{"budget", b.Dimensions.Budget, current.Budget},
		{"permissions", b.Dimensions.Permission, current.Permission},
		{"security", b.Dimensions.Security, current.Security},
	} {
		if dimension.approved != dimension.current {
			changed = append(changed, dimension.name)
		}
	}
	return changed
}

// CheckApprovalFor reports whether stage may proceed on the stored contract
// without asking for approval again.
func (b ApprovalBundle) CheckApprovalFor(stored GraphTask, stage ApprovalStage) error {
	if !validApprovalStage(stage) {
		return graphError("unknown approval stage %q", stage)
	}
	return b.Verify(stored)
}

// CheckApprovalAdvance verifies that one approval covers every internal stage.
func (b ApprovalBundle) CheckApprovalAdvance(stored GraphTask) error {
	if err := b.Verify(stored); err != nil {
		return err
	}
	for _, stage := range approvalStages {
		if err := b.CheckApprovalFor(stored, stage); err != nil {
			return err
		}
	}
	return nil
}

func (b ApprovalBundle) computeHash() (string, error) {
	b.BundleHash = ""
	return CanonicalSHA256(b)
}

func validApprovalStage(stage ApprovalStage) bool {
	for _, known := range approvalStages {
		if known == stage {
			return true
		}
	}
	return false
}
