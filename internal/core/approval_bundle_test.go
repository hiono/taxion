package core

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const legacySelection = `{"project":{"id":"p1"},"repository":{"provider":"gitlab","host":"gitlab.example.com","path":"group/repo","account":"acct","credentialRef":"git:main"},"runtime":{"provider":"copilot","account":"rt","credentialRef":"runtime:main","contractRef":"contract:v1"},"execution":{"requester":"human","worker":"coder","approvalRef":"approval-1"}}`

const explicitSelection = `{"project":{"id":"p1"},"repository":{"provider":"gitlab","host":"gitlab.example.com","path":"group/repo","account":"acct","credentialRef":"git:main"},"runtime":{"provider":"copilot","account":"rt","credentialRef":"runtime:main","contractRef":"contract:v1"},"execution":{"requester":"human","worker":"coder","approvalRef":"approval-1"},"endpoints":{"apiBase":"https://gitlab.example.com","webBase":"https://gitlab.example.com","httpsCloneBase":"https://gitlab.example.com","sshHost":"gitlab.example.com","sshPort":"22","projectId":"42"}}`

func selectionSources(t *testing.T, selection string) map[string]string {
	t.Helper()
	paths, err := SelectionFields(json.RawMessage(selection))
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]string{}
	for _, path := range paths {
		sources[path] = "user"
	}
	return sources
}

func approvalConfiguration(t *testing.T, selection string) ApprovalConfiguration {
	t.Helper()
	configuration, err := SelectApprovalConfiguration(json.RawMessage(selection), selectionSources(t, selection))
	if err != nil {
		t.Fatal(err)
	}
	return configuration
}

func approvedBundle(t *testing.T, task GraphTask) (ApprovalBundle, ApprovalConfiguration) {
	t.Helper()
	task.Status = Draft
	configuration := approvalConfiguration(t, explicitSelection)
	bundle, err := ComposeApprovalBundle("approval-1", task, task, configuration, "human", "human:1", "contract gate", time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return bundle, configuration
}

func TestApprovalBundleDigestsEveryDimension(t *testing.T) {
	task := validDraftTask("contract")
	bundle, _ := approvedBundle(t, task)
	if bundle.BundleVersion != ApprovalBundleVersion || bundle.CanonicalizationVersion != ApprovalCanonicalizationVersion {
		t.Fatalf("versions missing: %+v", bundle)
	}
	for name, digest := range map[string]string{
		"baseline": bundle.Dimensions.Baseline, "scope": bundle.Dimensions.Scope,
		"acceptance": bundle.Dimensions.Acceptance, "budget": bundle.Dimensions.Budget,
		"permissions": bundle.Dimensions.Permission, "security": bundle.Dimensions.Security,
		"configuration": bundle.Dimensions.Configuration,
	} {
		if len(digest) != 64 {
			t.Fatalf("%s digest is not a SHA-256: %q", name, digest)
		}
	}
	if len(bundle.BundleHash) != 64 || bundle.BundleHash != bundle.ProposalHash && bundle.ProposalHash != bundle.RecordHash {
		t.Fatalf("unexpected hashes: %+v", bundle)
	}
	if err := bundle.CheckApprovalAdvance(task, approvalConfiguration(t, explicitSelection)); err != nil {
		t.Fatalf("unchanged contract requires re-approval: %v", err)
	}
}

func TestApprovalBundleDriftIsMachineReadable(t *testing.T) {
	base := validDraftTask("contract")
	configuration := approvalConfiguration(t, explicitSelection)
	cases := []struct {
		name      string
		mutate    func(*GraphTask)
		dimension string
		fields    []string
	}{
		{"baseline", func(task *GraphTask) { task.BaselineSHA = strings.Repeat("b", 40) }, "baseline", []string{"baselineSha"}},
		{"scope", func(task *GraphTask) { task.Scope = append(task.Scope, "extra") }, "scope", []string{"scope"}},
		{"acceptance", func(task *GraphTask) { task.Acceptance = append(task.Acceptance, "extra") }, "acceptance", []string{"acceptance"}},
		{"budget", func(task *GraphTask) { task.BudgetGuidance = &BudgetGuidancePolicy{Mode: "flow"} }, "budget", []string{"budgetGuidance"}},
		{"permissions", func(task *GraphTask) { task.Owner = "REVIEWER" }, "permissions", []string{"owner"}},
		{"security", func(task *GraphTask) { task.Risk = "high" }, "security", []string{"risk"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			bundle, _ := approvedBundle(t, base)
			stored := base
			test.mutate(&stored)
			drifts, err := bundle.Drifts(stored, configuration)
			if err != nil {
				t.Fatal(err)
			}
			var found *ApprovalDrift
			for i := range drifts {
				if drifts[i].Dimension == test.dimension {
					found = &drifts[i]
				}
			}
			if found == nil {
				t.Fatalf("%s drift missing: %+v", test.dimension, drifts)
			}
			if found.AuthorizationImpact != ImpactReapproval || found.PreviousDigest == found.CurrentDigest {
				t.Fatalf("unexpected drift: %+v", *found)
			}
			for _, field := range test.fields {
				if !slicesContains(found.Fields, field) {
					t.Fatalf("%s field path missing: %+v", field, found.Fields)
				}
			}
			if err := bundle.Verify(stored, configuration); err == nil {
				t.Fatal("drifted contract was accepted")
			}
		})
	}
}

func TestApprovalBundleDriftReportsConfigurationPaths(t *testing.T) {
	bundle, _ := approvedBundle(t, validDraftTask("contract"))
	for name, change := range map[string]struct {
		selection string
		field     string
	}{
		"apiBase":    {strings.Replace(explicitSelection, "https://gitlab.example.com\",\"webBase", "https://api2.example.com\",\"webBase", 1), "endpoints.apiBase"},
		"projectId":  {strings.Replace(explicitSelection, "\"projectId\":\"42\"", "\"projectId\":\"43\"", 1), "endpoints.projectId"},
		"sshPort":    {strings.Replace(explicitSelection, "\"sshPort\":\"22\"", "\"sshPort\":\"2222\"", 1), "endpoints.sshPort"},
		"identity":   {strings.Replace(explicitSelection, "\"account\":\"acct\"", "\"account\":\"acct\",\"identity\":\"ssh\"", 1), "repository.identity"},
		"credential": {strings.Replace(explicitSelection, "git:main", "git:other", 1), "repository.credentialRef"},
		"runtime":    {strings.Replace(explicitSelection, "\"account\":\"rt\"", "\"account\":\"rt2\"", 1), "runtime.account"},
		"worker":     {strings.Replace(explicitSelection, "\"worker\":\"coder\"", "\"worker\":\"reviewer\"", 1), "execution.worker"},
	} {
		t.Run(name, func(t *testing.T) {
			current := approvalConfiguration(t, change.selection)
			drifts, err := bundle.Drifts(validDraftTask("contract"), current)
			if err != nil {
				t.Fatal(err)
			}
			if len(drifts) != 1 || drifts[0].Dimension != "configuration" {
				t.Fatalf("expected only configuration drift: %+v", drifts)
			}
			if drifts[0].AuthorizationImpact != ImpactReapproval || !slicesContains(drifts[0].Fields, change.field) {
				t.Fatalf("unexpected configuration drift: %+v", drifts[0])
			}
			if drifts[0].PreviousDigest != bundle.Dimensions.Configuration || drifts[0].CurrentDigest != current.Digest {
				t.Fatalf("digests were not reported: %+v", drifts[0])
			}
		})
	}
}

func TestApprovalBundleDriftReportsMultipleDimensions(t *testing.T) {
	bundle, _ := approvedBundle(t, validDraftTask("contract"))
	stored := validDraftTask("contract")
	stored.Acceptance = append(stored.Acceptance, "extra")
	stored.Owner = "REVIEWER"
	drifts, err := bundle.Drifts(stored, approvalConfiguration(t, legacySelection))
	if err != nil {
		t.Fatal(err)
	}
	dimensions := map[string]string{}
	for _, drift := range drifts {
		dimensions[drift.Dimension] = drift.AuthorizationImpact
	}
	for _, expected := range []string{"recordHash", "acceptance", "permissions", "configuration"} {
		if dimensions[expected] != ImpactReapproval {
			t.Fatalf("%s drift missing: %+v", expected, drifts)
		}
	}
	if _, ok := dimensions["scope"]; ok {
		t.Fatalf("unrelated dimension drifted: %+v", drifts)
	}
}

func TestApprovalBundleRejectsTamperedBundle(t *testing.T) {
	task := validDraftTask("contract")
	configuration := approvalConfiguration(t, explicitSelection)
	bundle, err := ComposeApprovalBundle("approval-1", task, task, configuration, "human", "human:1", "gate", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*ApprovalBundle){
		"bundleHash":  func(b *ApprovalBundle) { b.BundleHash = strings.Repeat("0", 64) },
		"emptyHash":   func(b *ApprovalBundle) { b.BundleHash = "" },
		"dimension":   func(b *ApprovalBundle) { b.Dimensions.Scope = strings.Repeat("1", 64) },
		"selection":   func(b *ApprovalBundle) { b.Configuration = approvalConfiguration(t, legacySelection) },
		"proposal":    func(b *ApprovalBundle) { b.ProposalHash = strings.Repeat("2", 64) },
		"recordHash":  func(b *ApprovalBundle) { b.RecordHash = strings.Repeat("3", 64) },
		"actorChange": func(b *ApprovalBundle) { b.Actor = "other" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			tampered := bundle
			mutate(&tampered)
			if err := tampered.Verify(task, configuration); err == nil {
				t.Fatal("tampered bundle was accepted")
			}
		})
	}
}

func TestApprovalBundleRejectsUnknownVersions(t *testing.T) {
	task := validDraftTask("contract")
	configuration := approvalConfiguration(t, explicitSelection)
	bundle, err := ComposeApprovalBundle("approval-1", task, task, configuration, "human", "human:1", "gate", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ApprovalBundle){
		"bundle":           func(b *ApprovalBundle) { b.BundleVersion = 2 },
		"canonicalization": func(b *ApprovalBundle) { b.CanonicalizationVersion = 2 },
	} {
		t.Run(name, func(t *testing.T) {
			changed := bundle
			mutate(&changed)
			drifts, err := changed.Drifts(task, configuration)
			if err != nil {
				t.Fatal(err)
			}
			if len(drifts) != 1 || drifts[0].AuthorizationImpact != ImpactInvalid {
				t.Fatalf("unknown version was not invalid: %+v", drifts)
			}
			if err := changed.Verify(task, configuration); err == nil || !strings.Contains(err.Error(), "invalid") {
				t.Fatalf("unknown version was accepted: %v", err)
			}
		})
	}
}

func TestApprovalBundleKeepsProposalAndRecordHashDistinct(t *testing.T) {
	proposal := validDraftTask("contract")
	stored := validDraftTask("contract")
	stored.FileAccess = DefaultFileAccessPolicy()
	stored.BudgetGuidance = &BudgetGuidancePolicy{Mode: "flow", Source: "default", AllowedModes: []string{"off", "flow"}}
	configuration := approvalConfiguration(t, explicitSelection)
	bundle, err := ComposeApprovalBundle("approval-1", proposal, stored, configuration, "human", "human:1", "gate", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if bundle.ProposalHash == bundle.RecordHash {
		t.Fatal("proposal and record hash were conflated")
	}
	if err := bundle.Verify(stored, configuration); err != nil {
		t.Fatalf("defaults alone required re-approval: %v", err)
	}
}

func TestApprovalBundleDetectsStoredTamper(t *testing.T) {
	task := validDraftTask("contract")
	bundle, configuration := approvedBundle(t, task)
	stored := task
	stored.Objective = "rewritten after approval"
	drifts, err := bundle.Drifts(stored, configuration)
	if err != nil {
		t.Fatal(err)
	}
	if len(drifts) != 1 || drifts[0].Dimension != "recordHash" || drifts[0].AuthorizationImpact != ImpactReapproval {
		t.Fatalf("stored tamper was not detected as record drift: %+v", drifts)
	}
}

func TestApprovalConfigurationDigestCoversSelection(t *testing.T) {
	legacy := approvalConfiguration(t, legacySelection)
	explicit := approvalConfiguration(t, explicitSelection)
	if legacy.Digest == explicit.Digest {
		t.Fatal("legacy selection matched explicit endpoints")
	}
	changedAPIBase := approvalConfiguration(t, strings.Replace(explicitSelection, "https://gitlab.example.com\",\"webBase", "https://api2.example.com\",\"webBase", 1))
	changedProject := approvalConfiguration(t, strings.Replace(explicitSelection, "\"projectId\":\"42\"", "\"projectId\":\"43\"", 1))
	if changedAPIBase.Digest == explicit.Digest || changedProject.Digest == explicit.Digest {
		t.Fatal("endpoint change did not change the configuration digest")
	}
	if legacy.Selection == nil || string(explicit.Selection) != explicitSelection {
		t.Fatal("exact selection was not retained")
	}
}

func TestApprovalConfigurationIgnoresFieldOrigins(t *testing.T) {
	paths, err := SelectionFields(json.RawMessage(explicitSelection))
	if err != nil {
		t.Fatal(err)
	}
	fromProject := map[string]string{}
	fromDefault := map[string]string{}
	for _, path := range paths {
		fromProject[path] = "project"
		fromDefault[path] = "environment"
	}
	first, err := SelectApprovalConfiguration(json.RawMessage(explicitSelection), fromProject)
	if err != nil {
		t.Fatal(err)
	}
	second, err := SelectApprovalConfiguration(json.RawMessage(explicitSelection), fromDefault)
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest != second.Digest {
		t.Fatal("field origins changed the configuration digest")
	}
}

func TestApprovalConfigurationRejectsCredentials(t *testing.T) {
	selection := strings.Replace(explicitSelection, "git:main", "ghp_"+strings.Repeat("z", 36), 1)
	if _, err := SelectApprovalConfiguration(json.RawMessage(selection), nil); err == nil {
		t.Fatal("credential-like selection was accepted")
	}
	if _, err := SelectApprovalConfiguration(json.RawMessage(`{}`), nil); err == nil {
		t.Fatal("empty selection was accepted")
	}
}

func TestApprovalBundleRejectsLegacyBundleWithoutConfiguration(t *testing.T) {
	task := validDraftTask("contract")
	bundle, _ := approvedBundle(t, task)
	legacy := bundle
	legacy.Configuration = ApprovalConfiguration{}
	legacy.BundleHash = ""
	if err := legacy.Verify(task, approvalConfiguration(t, explicitSelection)); err == nil {
		t.Fatal("bundle without a configuration selection was accepted")
	}
}

func slicesContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestAppendApprovalKeepsRecordsImmutable(t *testing.T) {
	graph := TaskGraph{Version: 1}
	task := validDraftTask("contract")
	if err := graph.Track(task, "human", "draft"); err != nil {
		t.Fatal(err)
	}
	stored, _ := graph.GraphTask(task.ID)
	bundle, configuration := approvedBundle(t, stored)
	bundle.ID = "approval-one"
	hash, err := bundle.computeHash()
	if err != nil {
		t.Fatal(err)
	}
	bundle.BundleHash = hash
	if err := graph.AppendApproval(bundle); err != nil {
		t.Fatal(err)
	}
	if err := graph.AppendApproval(bundle); err == nil {
		t.Fatal("duplicate approval ID was accepted")
	}
	other := bundle
	other.ID = "approval-two"
	other.TaskID = "missing"
	hash, err = other.computeHash()
	if err != nil {
		t.Fatal(err)
	}
	other.BundleHash = hash
	if err := graph.AppendApproval(other); err == nil {
		t.Fatal("approval for an unknown task was accepted")
	}
	if len(graph.Approvals) != 1 || graph.Approvals[0].ProposalHash != bundle.ProposalHash {
		t.Fatal("append changed an existing approval record")
	}
	if _, ok := graph.MatchingApproval(task.ID, stored, configuration); !ok {
		t.Fatal("stored approval does not match the current dimensions")
	}
	if _, ok := graph.MatchingApproval("other", stored, configuration); ok {
		t.Fatal("approval matched a different task")
	}
}
