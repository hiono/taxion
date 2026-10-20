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
		Owner         string
		Reviewer      string
		Authorization string
		Decisions     []TaskDecision
		Worktree      string
		Branch        string
	}{task.Owner, task.Reviewer, task.AuthorizationID, task.Decisions, task.Worktree, task.Branch}); err != nil {
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
	appendDrift("permissions", b.Dimensions.Permission, dimensions.Permission, ImpactReapproval, []string{"owner", "reviewer", "authorizationId", "decisions", "worktree", "branch"})
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

func selectionValues(selection json.RawMessage) (map[string]any, error) {
	var values map[string]any
	if err := json.Unmarshal(selection, &values); err != nil {
		return nil, graphError("approval configuration requires a selected JSON object")
	}
	flat := map[string]any{}
	var collect func(map[string]any, string) error
	collect = func(fields map[string]any, prefix string) error {
		for key, value := range fields {
			path := prefix + key
			if children, ok := value.(map[string]any); ok {
				if len(children) == 0 {
					flat[path] = "<empty>"
					continue
				}
				if err := collect(children, path+"."); err != nil {
					return err
				}
				continue
			}
			flat[path] = value
		}
		return nil
	}
	if err := collect(values, ""); err != nil {
		return nil, err
	}
	return flat, nil
}
