package core

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"
)

const (
	// ApprovalBundleVersion identifies the bundle record layout.
	ApprovalBundleVersion = 1
	// ApprovalCanonicalizationVersion identifies the digest rules.
	ApprovalCanonicalizationVersion = 1
)

// Authorization impact classes reported by an approval bundle drift.
const (
	ImpactNone       = "none"
	ImpactReapproval = "reapproval-required"
	ImpactInvalid    = "invalid"
)

// ApprovalStage is an internal transition that reuses one approval bundle.

// ConfirmationBasis is the full approval basis a human confirms. The token
// binds the proposal to every dimension and to the versions that define how
// the digests were computed.
type ConfirmationBasis struct {
	BundleVersion           int    `json:"bundleVersion"`
	CanonicalizationVersion int    `json:"canonicalizationVersion"`
	ProposalHash            string `json:"proposalHash"`
	BaselineDigest          string `json:"baselineDigest"`
	ScopeDigest             string `json:"scopeDigest"`
	AcceptanceDigest        string `json:"acceptanceDigest"`
	BudgetDigest            string `json:"budgetDigest"`
	PermissionsDigest       string `json:"permissionsDigest"`
	SecurityDigest          string `json:"securityDigest"`
	ConfigurationDigest     string `json:"configurationDigest"`
}

// ConfirmationTokenFor returns the token a caller must pass back to authorize
// exactly the presented basis.
func ConfirmationTokenFor(proposalHash string, dimensions ApprovalDimensions) (string, error) {
	return CanonicalSHA256(ConfirmationBasis{
		BundleVersion:           ApprovalBundleVersion,
		CanonicalizationVersion: ApprovalCanonicalizationVersion,
		ProposalHash:            proposalHash,
		BaselineDigest:          dimensions.Baseline,
		ScopeDigest:             dimensions.Scope,
		AcceptanceDigest:        dimensions.Acceptance,
		BudgetDigest:            dimensions.Budget,
		PermissionsDigest:       dimensions.Permission,
		SecurityDigest:          dimensions.Security,
		ConfigurationDigest:     dimensions.Configuration,
	})
}

// ConfirmationToken returns the token for the bundle basis.
func (b ApprovalBundle) ConfirmationToken() (string, error) {
	return ConfirmationTokenFor(b.ProposalHash, b.Dimensions)
}

type ApprovalStage string

const (
	StageReadback    ApprovalStage = "readback"
	StageEligibility ApprovalStage = "eligibility"
	StagePreflight   ApprovalStage = "preflight"
	StageLane        ApprovalStage = "lane"
	StageRolePacket  ApprovalStage = "role-packet"
)

// ApprovalStages lists the transitions a single approval covers.
func ApprovalStages() []ApprovalStage {
	return []ApprovalStage{StageReadback, StageEligibility, StagePreflight, StageLane, StageRolePacket}
}

// ApprovalDimensions bind the executable-relevant dimensions of a contract.
type ApprovalDimensions struct {
	Baseline      string `json:"baselineDigest"`
	Scope         string `json:"scopeDigest"`
	Acceptance    string `json:"acceptanceDigest"`
	Budget        string `json:"budgetDigest"`
	Permission    string `json:"permissionsDigest"`
	Security      string `json:"securityDigest"`
	Configuration string `json:"configurationDigest"`
}

// ApprovalConfiguration records the exact effective configuration selection
// that a human approved. It carries references only, never resolved secrets.
type ApprovalConfiguration struct {
	Selection json.RawMessage   `json:"selection"`
	Sources   map[string]string `json:"sources,omitempty"`
	Digest    string            `json:"digest"`
}

// SelectApprovalConfiguration digests a configuration selection with the same
// canonicalization the task-bound configuration check compares.
func SelectApprovalConfiguration(selection json.RawMessage, sources map[string]string) (ApprovalConfiguration, error) {
	paths, err := SelectionFields(selection)
	if err != nil {
		return ApprovalConfiguration{}, err
	}
	for _, path := range paths {
		if len(sources) == 0 {
			continue
		}
		switch sources[path] {
		case "user", "project", "environment":
		default:
			return ApprovalConfiguration{}, graphError("approval configuration requires field origins")
		}
	}
	if len(sources) != 0 && len(paths) != len(sources) {
		return ApprovalConfiguration{}, graphError("approval configuration contains unrelated field origins")
	}
	if credentialLike(string(selection)) {
		return ApprovalConfiguration{}, graphError("approval configuration contains a credential-like value; store only credential references")
	}
	var normalized any
	if err := json.Unmarshal(selection, &normalized); err != nil {
		return ApprovalConfiguration{}, graphError("approval configuration requires a selected JSON object")
	}
	digest, err := CanonicalSHA256(normalized)
	if err != nil {
		return ApprovalConfiguration{}, err
	}
	return ApprovalConfiguration{Selection: selection, Sources: sources, Digest: digest}, nil
}

// ApprovalBundle is one append-only human approval of a contract basis.
type ApprovalBundle struct {
	BundleVersion           int                   `json:"bundleVersion"`
	CanonicalizationVersion int                   `json:"canonicalizationVersion"`
	ID                      string                `json:"id"`
	TaskID                  string                `json:"taskId"`
	ProposalHash            string                `json:"proposalHash"`
	RecordHash              string                `json:"recordHash"`
	Dimensions              ApprovalDimensions    `json:"dimensions"`
	Configuration           ApprovalConfiguration `json:"configuration"`
	BundleHash              string                `json:"bundleHash"`
	Actor                   string                `json:"actor"`
	Source                  string                `json:"source"`
	Reason                  string                `json:"reason"`
	At                      time.Time             `json:"at"`
}

// ApprovalDrift is a machine-readable change between an approval and the
// current contract basis. It never carries field values.
type ApprovalDrift struct {
	Dimension           string   `json:"dimension"`
	PreviousDigest      string   `json:"previousDigest,omitempty"`
	CurrentDigest       string   `json:"currentDigest,omitempty"`
	AuthorizationImpact string   `json:"authorizationImpact"`
	Fields              []string `json:"redactedFieldPaths"`
}

// BundleDimensions digests the contract and configuration dimensions.
func BundleDimensions(task GraphTask, configuration ApprovalConfiguration) (ApprovalDimensions, error) {
	if !nonempty(configuration.Digest) {
		return ApprovalDimensions{}, graphError("approval bundle requires a configuration digest")
	}
	dimension := func(value any) (string, error) { return CanonicalSHA256(value) }
	dimensions := ApprovalDimensions{Configuration: configuration.Digest}
	var err error
	if dimensions.Baseline, err = dimension(struct{ BaselineSHA string }{task.BaselineSHA}); err != nil {
		return ApprovalDimensions{}, err
	}
	if dimensions.Scope, err = dimension(struct {
		Scope      []string
		OutOfScope []string
		FileAccess *FileAccessPolicy
	}{task.Scope, task.OutOfScope, task.FileAccess}); err != nil {
		return ApprovalDimensions{}, err
	}
	if dimensions.Acceptance, err = dimension(struct {
		Acceptance         []string
		RequiredEvidence   []string
		ValidationCommands []string
		ExitCondition      string
	}{task.Acceptance, task.RequiredEvidence, task.ValidationCommands, task.ExitCondition}); err != nil {
		return ApprovalDimensions{}, err
	}
	if dimensions.Budget, err = dimension(task.BudgetGuidance); err != nil {
		return ApprovalDimensions{}, err
	}
	if dimensions.Permission, err = dimension(struct {
		Owner              string
		Reviewer           string
		Authorization      string
		Decisions          []TaskDecision
		Worktree           string
		Branch             string
		OutputMode         string
		TargetTreeSHA      string
		TargetChangedFiles []string
	}{task.Owner, task.Reviewer, task.AuthorizationID, task.Decisions, task.Worktree, task.Branch, task.OutputMode, task.TargetTreeSHA, task.TargetChangedFiles}); err != nil {
		return ApprovalDimensions{}, err
	}
	if dimensions.Security, err = dimension(struct {
		Risk           string
		ExceptionIDs   []string
		Specifications []Specification
		MemoryRefs     []string
	}{task.Risk, task.ExceptionIDs, task.Specifications, task.MemoryRefs}); err != nil {
		return ApprovalDimensions{}, err
	}
	return dimensions, nil
}

// ComposeApprovalBundle binds a proposed contract, the stored record and the
// effective configuration selection under one hash.
func ComposeApprovalBundle(id string, proposal, stored GraphTask, configuration ApprovalConfiguration, actor, source, reason string, at time.Time) (ApprovalBundle, error) {
	if !nonempty(id) || !nonempty(stored.ID) || !nonempty(actor) || !nonempty(source) {
		return ApprovalBundle{}, graphError("approval bundle requires an ID, Task, actor and source")
	}
	dimensions, err := BundleDimensions(stored, configuration)
	if err != nil {
		return ApprovalBundle{}, err
	}
	proposalHash, err := CanonicalSHA256(proposal)
	if err != nil {
		return ApprovalBundle{}, err
	}
	recordHash, err := CanonicalSHA256(stored)
	if err != nil {
		return ApprovalBundle{}, err
	}
	bundle := ApprovalBundle{
		BundleVersion: ApprovalBundleVersion, CanonicalizationVersion: ApprovalCanonicalizationVersion,
		ID: id, TaskID: stored.ID, ProposalHash: proposalHash, RecordHash: recordHash,
		Dimensions: dimensions, Configuration: configuration,
		Actor: actor, Source: source, Reason: reason, At: at.UTC(),
	}
	bundle.BundleHash, err = bundle.computeHash()
	if err != nil {
		return ApprovalBundle{}, err
	}
	return bundle, nil
}

func (b ApprovalBundle) computeHash() (string, error) {
	copy := b
	copy.BundleHash = ""
	return CanonicalSHA256(copy)
}

// Drifts reports every changed dimension with its authorization impact.
func (b ApprovalBundle) Drifts(stored GraphTask, current ApprovalConfiguration) ([]ApprovalDrift, error) {
	var drifts []ApprovalDrift
	if b.BundleVersion != ApprovalBundleVersion {
		drifts = append(drifts, ApprovalDrift{Dimension: "bundleVersion", PreviousDigest: fmt.Sprint(b.BundleVersion), CurrentDigest: fmt.Sprint(ApprovalBundleVersion), AuthorizationImpact: ImpactInvalid, Fields: []string{"bundleVersion"}})
		return drifts, nil
	}
	if b.CanonicalizationVersion != ApprovalCanonicalizationVersion {
		drifts = append(drifts, ApprovalDrift{Dimension: "canonicalizationVersion", PreviousDigest: fmt.Sprint(b.CanonicalizationVersion), CurrentDigest: fmt.Sprint(ApprovalCanonicalizationVersion), AuthorizationImpact: ImpactInvalid, Fields: []string{"canonicalizationVersion"}})
		return drifts, nil
	}
	appendDrift := func(dimension, previous, current, impact string, fields []string) {
		if previous != current {
			drifts = append(drifts, ApprovalDrift{Dimension: dimension, PreviousDigest: previous, CurrentDigest: current, AuthorizationImpact: impact, Fields: fields})
		}
	}
	appendDrift("taskId", b.TaskID, stored.ID, ImpactInvalid, []string{"id"})
	recordHash, err := CanonicalSHA256(stored)
	if err != nil {
		return nil, err
	}
	appendDrift("recordHash", b.RecordHash, recordHash, ImpactReapproval, []string{"contract"})
	dimensions, err := BundleDimensions(stored, current)
	if err != nil {
		return nil, err
	}
	appendDrift("baseline", b.Dimensions.Baseline, dimensions.Baseline, ImpactReapproval, []string{"baselineSha"})
	appendDrift("scope", b.Dimensions.Scope, dimensions.Scope, ImpactReapproval, []string{"scope", "outOfScope", "fileAccess"})
	appendDrift("acceptance", b.Dimensions.Acceptance, dimensions.Acceptance, ImpactReapproval, []string{"acceptance", "requiredEvidence", "validationCommands", "exitCondition"})
	appendDrift("budget", b.Dimensions.Budget, dimensions.Budget, ImpactReapproval, []string{"budgetGuidance"})
	appendDrift("permissions", b.Dimensions.Permission, dimensions.Permission, ImpactReapproval, []string{"owner", "reviewer", "authorizationId", "decisions", "worktree", "branch", "outputMode", "targetTreeSha", "targetChangedFiles"})
	appendDrift("security", b.Dimensions.Security, dimensions.Security, ImpactReapproval, []string{"risk", "exceptionIds", "specifications", "memoryRefs"})
	fields, err := configurationDriftFields(b.Configuration.Selection, current.Selection)
	if err != nil {
		return nil, err
	}
	appendDrift("configuration", b.Configuration.Digest, current.Digest, ImpactReapproval, fields)
	return drifts, nil
}

func configurationDriftFields(previous, current json.RawMessage) ([]string, error) {
	if len(previous) == 0 || len(current) == 0 {
		return []string{"endpoints"}, nil
	}
	before, err := selectionValues(previous)
	if err != nil {
		return nil, err
	}
	after, err := selectionValues(current)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for path := range before {
		seen[path] = true
	}
	for path := range after {
		seen[path] = true
	}
	var changed []string
	for path := range seen {
		if !reflect.DeepEqual(before[path], after[path]) {
			changed = append(changed, path)
		}
	}
	slices.Sort(changed)
	return changed, nil
}

// Verify rejects any drift that no longer matches the approved basis.
func (b ApprovalBundle) Verify(stored GraphTask, current ApprovalConfiguration) error {
	drifts, err := b.Drifts(stored, current)
	if err != nil {
		return err
	}
	if len(drifts) == 0 {
		hash, err := b.computeHash()
		if err != nil {
			return err
		}
		if b.BundleHash == "" || b.BundleHash != hash {
			return graphError("approval bundle %s hash mismatch; re-approval required", b.ID)
		}
		return nil
	}
	for _, drift := range drifts {
		if drift.AuthorizationImpact == ImpactInvalid {
			return graphError("approval bundle %s is invalid (%s); re-approval required", b.ID, drift.Dimension)
		}
	}
	hash, err := b.computeHash()
	if err != nil {
		return err
	}
	if b.BundleHash == "" || b.BundleHash != hash {
		return graphError("approval bundle %s hash mismatch; re-approval required", b.ID)
	}
	return graphError("approval bundle %s basis changed (%s); re-approval required", b.ID, driftNames(drifts))
}

func driftNames(drifts []ApprovalDrift) string {
	names := make([]string, 0, len(drifts))
	for _, drift := range drifts {
		names = append(names, drift.Dimension)
	}
	return strings.Join(names, ",")
}

// CheckApprovalFor reuses one approval for an internal stage.
func (b ApprovalBundle) CheckApprovalFor(stored GraphTask, current ApprovalConfiguration, stage ApprovalStage) error {
	known := false
	for _, candidate := range ApprovalStages() {
		known = known || candidate == stage
	}
	if !known {
		return graphError("unknown approval stage %q", string(stage))
	}
	return b.Verify(stored, current)
}

// CheckApprovalAdvance proves one approval covers every stage.
func (b ApprovalBundle) CheckApprovalAdvance(stored GraphTask, current ApprovalConfiguration) error {
	for _, stage := range ApprovalStages() {
		if err := b.CheckApprovalFor(stored, current, stage); err != nil {
			return err
		}
	}
	return nil
}

func selectionValues(selection json.RawMessage) (map[string]string, error) {
	leaves, err := SelectionLeaves(selection)
	if err != nil {
		return nil, graphError("approval configuration requires a selected JSON object")
	}
	return leaves, nil
}

// ApprovalsForTask returns every approval of a Task in append order.
func (g TaskGraph) ApprovalsForTask(taskID string) []ApprovalBundle {
	var bundles []ApprovalBundle
	for _, bundle := range g.Approvals {
		if bundle.TaskID == taskID {
			bundles = append(bundles, bundle)
		}
	}
	return bundles
}

// NewestApproval returns the most recently appended approval of a Task.
func (g TaskGraph) NewestApproval(taskID string) (ApprovalBundle, bool) {
	bundles := g.ApprovalsForTask(taskID)
	if len(bundles) == 0 {
		return ApprovalBundle{}, false
	}
	return bundles[len(bundles)-1], true
}

// Approval returns the approval record with the given ID.
func (g TaskGraph) Approval(id string) (ApprovalBundle, bool) {
	for _, bundle := range g.Approvals {
		if bundle.ID == id {
			return bundle, true
		}
	}
	return ApprovalBundle{}, false
}

func (g TaskGraph) validateApprovals() error {
	seen := map[string]bool{}
	for _, bundle := range g.Approvals {
		if !nonempty(bundle.ID) {
			return graphError("approval requires an ID")
		}
		if seen[bundle.ID] {
			return graphError("duplicate approval ID %s", bundle.ID)
		}
		seen[bundle.ID] = true
		if _, exists := g.task(bundle.TaskID); !exists {
			return graphError("approval %s references unknown task %s", bundle.ID, bundle.TaskID)
		}
		if err := bundle.hashValid(); err != nil {
			return graphError("approval %s %v", bundle.ID, err)
		}
	}
	return nil
}

// hashValid re-derives the bundle hash so a stored record cannot be edited
// without being detected.
func (b ApprovalBundle) hashValid() error {
	if b.BundleHash == "" {
		return fmt.Errorf("requires a bundle hash")
	}
	expected, err := b.computeHash()
	if err != nil {
		return err
	}
	if b.BundleHash != expected {
		return fmt.Errorf("bundle hash mismatch")
	}
	return nil
}

// AppendApproval stores an approval record without touching earlier records.
func (g *TaskGraph) AppendApproval(bundle ApprovalBundle) error {
	next := cloneGraph(*g)
	if _, exists := next.Approval(bundle.ID); exists {
		return graphError("duplicate approval ID %s", bundle.ID)
	}
	if _, exists := next.task(bundle.TaskID); !exists {
		return graphError("approval %s references unknown task %s", bundle.ID, bundle.TaskID)
	}
	if err := bundle.hashValid(); err != nil {
		return graphError("approval %s %v", bundle.ID, err)
	}
	next.Approvals = append(next.Approvals, bundle)
	if err := next.Validate(); err != nil {
		return err
	}
	*g = next
	return nil
}

// MatchingApproval returns the newest record that still covers every current
// dimension. A partial match is never reported as approved.
func (g TaskGraph) MatchingApproval(taskID string, stored GraphTask, current ApprovalConfiguration) (ApprovalBundle, bool) {
	for i := len(g.Approvals) - 1; i >= 0; i-- {
		bundle := g.Approvals[i]
		if bundle.TaskID != taskID {
			continue
		}
		if err := bundle.Verify(stored, current); err != nil {
			continue
		}
		return bundle, true
	}
	return ApprovalBundle{}, false
}
