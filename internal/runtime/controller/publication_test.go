package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/publictext"
	"github.com/hiono/taxion/internal/store"
)

func publicationScanner(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scanner")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nif [ \"$1\" = version ]; then echo 1.9.0; exit 0; fi\ncat >/dev/null\nprintf '[]'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

type publicationFixture struct {
	mu           sync.Mutex
	sends        int
	found        bool
	failure      bool
	entered      chan struct{}
	release      chan struct{}
	check        func() error
	preSendError error
}

func (p *publicationFixture) Check(context.Context, store.ExternalUpdate) error {
	if p.check != nil {
		return p.check()
	}
	return nil
}
func (p *publicationFixture) Reconcile(context.Context, store.ExternalUpdate) (string, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return "99", p.found, nil
}
func (p *publicationFixture) Send(_ context.Context, _ store.ExternalUpdate) (string, error) {
	if p.preSendError != nil {
		return "", p.preSendError
	}
	p.mu.Lock()
	p.sends++
	p.found = true
	fail := p.failure
	p.mu.Unlock()
	if p.entered != nil {
		close(p.entered)
		<-p.release
	}
	if fail {
		return "", errors.New("simulated response lost after publication")
	}
	return "99", nil
}

func publicationProposal(t *testing.T) store.ExternalUpdate {
	t.Helper()
	protected := sha256.Sum256([]byte("reserved-private-test-identifier"))
	t.Setenv("TAXION_PUBLIC_PRIVATE_IDENTIFIER_SHA256", hex.EncodeToString(protected[:]))
	profiles := publictext.Profiles{Version: 1}
	for _, binding := range []struct{ provider, host, repository string }{
		{"github", "github.com", "owner/repo"},
		{"gitlab", "gitlab.example", "owner/repo"},
		{"gitlab", "gitlab.example:8443", "group/subgroup/repo"},
	} {
		profiles.Projects = append(profiles.Projects, publictext.Profile{Version: 1, Provider: binding.provider,
			ProjectType: "public-oss", Configuration: "production", Guidance: &publictext.GuidanceProfile{
				ID: "test-public-en", IntendedAudience: []string{"users", "contributors", "maintainers"}, Visibility: []string{"public"}, ProjectTypes: []string{"public-oss"},
				PrimaryLanguage: "en", SecondaryLanguages: []string{}, DocumentTypes: []string{"issue", "comment", "pull-request"},
				TerminologyDomain: "test documentation", PublicationSuitability: "suitable", ConfidentialityAssumptions: "Synthetic data only.", Content: "Describe user-visible behavior and verified facts."},
			Host: binding.host, Repository: binding.repository, PrimaryLanguage: "en", SecondaryLanguages: []string{"ja"},
			TitleLanguage: "en", BodyLanguage: "en", TechnicalTerms: []string{}, Glossary: map[string]string{},
			Visibility: "public", IntendedAudience: []string{"users", "contributors", "maintainers"}})
	}
	data, err := json.Marshal(profiles)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "profiles.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TAXION_COMMUNICATION_PROFILES", path)
	_, guidance, err := publictext.SelectGuidance(profiles.Projects[0], "comment")
	if err != nil {
		t.Fatal(err)
	}
	return store.ExternalUpdate{ID: "accepted-report", TaskID: "implementation", Provider: "github", Host: "github.com",
		GuidanceDigest: guidance,
		Statement:      publictext.StatementContext{At: time.Now().UTC()},
		Repository:     "owner/repo", Kind: "ISSUE", Number: 53, Action: "COMMENT", Body: "Approved execution evidence.",
		Actor: "human", ApprovalSource: "human:exact-report", Account: "owner", CredentialRef: "gh:active"}
}

func TestPublicationHashGitLabTargetAndCredential(t *testing.T) {
	proposal := publicationProposal(t)
	proposal.Provider, proposal.Host = "gitlab", "gitlab.example:8443"
	proposal.Repository, proposal.Account, proposal.CredentialRef = "group/subgroup/repo", "scm-user", "glab:active"
	if _, err := PublicationHash(proposal); err != nil {
		t.Fatal("valid GitLab publication rejected", err)
	}

	proposal.CredentialRef = "gh:active"
	if _, err := PublicationHash(proposal); err == nil {
		t.Fatal("GitHub credential accepted for GitLab")
	}
	proposal.CredentialRef, proposal.Host = "glab:active", "https://gitlab.example"
	if _, err := PublicationHash(proposal); err == nil {
		t.Fatal("URL accepted instead of exact GitLab host")
	}
	proposal.Host, proposal.Provider = "gitlab.example", "github"
	proposal.CredentialRef = "glab:active"
	if _, err := PublicationHash(proposal); err == nil {
		t.Fatal("GitLab credential accepted for GitHub")
	}
	proposal.CredentialRef, proposal.Repository = "gh:active", "owner/group/repo"
	if _, err := PublicationHash(proposal); err == nil {
		t.Fatal("nested project path accepted for GitHub")
	}
}

func TestPublicationProfileBindingAndMissingProfilePreserveStore(t *testing.T) {
	db, _, _ := reviewFlowFixture(t)
	proposal := publicationProposal(t)
	scanner := publicationScanner(t)
	hash, err := PublicationHash(proposal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApprovePublication(t.Context(), db, PublicationRequest{proposal, hash}, scanner); err != nil {
		t.Fatal(err)
	}
	before, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	path := os.Getenv("TAXION_COMMUNICATION_PROFILES")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var profiles publictext.Profiles
	if err := json.Unmarshal(data, &profiles); err != nil {
		t.Fatal(err)
	}
	profiles.Projects[0].Glossary["unusedword"] = "preferredword"
	data, err = json.Marshal(profiles)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := PublicationHash(proposal)
	if err != nil || changed == hash {
		t.Fatal("approval did not bind the explicit profile", err)
	}
	publisher := &publicationFixture{}
	if _, err := DeliverPublication(t.Context(), db, proposal.ID, scanner, publisher); err == nil || publisher.sends != 0 {
		t.Fatal("profile drift bypassed saved approval", err)
	}
	t.Setenv("TAXION_COMMUNICATION_PROFILES", "")
	if _, err := PublicationHash(proposal); err == nil {
		t.Fatal("missing profile obtained approval hash")
	}
	if _, err := DeliverPublication(t.Context(), db, proposal.ID, scanner, publisher); err == nil || publisher.sends != 0 {
		t.Fatal("missing profile bypassed saved approval", err)
	}
	after, err := db.LoadWorkspace()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("profile rejection changed store", err)
	}
}

func TestPublicationPolicyRejectsBeforeApprovalOrDelivery(t *testing.T) {
	db, _, _ := reviewFlowFixture(t)
	before, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"github", "gitlab"} {
		proposal := publicationProposal(t)
		proposal.Provider = provider
		if provider == "gitlab" {
			proposal.Host, proposal.CredentialRef = "gitlab.example", "glab:active"
		}
		for _, body := range []string{
			"blockingItem: internal execution state",
			"完了した結果は後続コメントに記載している",
			"The unpublished release is available below",
			"CI has passed when the run is still pending",
		} {
			proposal.Body = body
			if _, err := PublicationHash(proposal); err == nil {
				t.Fatal("unsafe publication obtained an approval hash")
			}
			if _, err := ApprovePublication(t.Context(), db, PublicationRequest{Update: proposal}, publicationScanner(t)); err == nil {
				t.Fatal("unsafe publication approved")
			}
		}
	}

	after, err := db.LoadWorkspace()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("rejected publication changed saved state", err)
	}
	proposal := publicationProposal(t)
	original := proposal.Body
	if _, err := PublicationHash(proposal); err != nil || proposal.Body != original {
		t.Fatal("publication check rewrote approved content", err)
	}
}

func TestPublicationPolicyDriftAfterClaimRestoresPendingRecord(t *testing.T) {
	for _, policy := range []string{"profile", "protected-identifier", "adapter-gate"} {
		t.Run(policy, func(t *testing.T) {
			db, _, _ := reviewFlowFixture(t)
			proposal := publicationProposal(t)
			scanner := publicationScanner(t)
			hash, err := PublicationHash(proposal)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ApprovePublication(t.Context(), db, PublicationRequest{proposal, hash}, scanner); err != nil {
				t.Fatal(err)
			}
			if err := db.UpdateWorkspace(func(state *store.Workspace) error {
				state.Records.Updates[0].Error = "previous diagnostic"
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			before, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			publisher := &publicationFixture{check: func() error {
				if policy == "profile" {
					t.Setenv("TAXION_COMMUNICATION_PROFILES", "")
				} else if policy == "protected-identifier" {
					t.Setenv("TAXION_PUBLIC_PRIVATE_IDENTIFIER_SHA256", "")
				}
				return nil
			}}
			if policy == "adapter-gate" {
				publisher.preSendError = publictext.ErrMutationRejected
			}
			if _, err := DeliverPublication(t.Context(), db, proposal.ID, scanner, publisher); err == nil || publisher.sends != 0 {
				t.Fatal("policy drift after claim allowed a remote write", err)
			}
			after, err := db.LoadWorkspace()
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("pre-send rejection did not restore the original pending record", err)
			}
		})
	}
}

func TestLegacyTemporalViolationRejectedBeforeDelivery(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		t.Run(provider, func(t *testing.T) {
			db, _, _ := reviewFlowFixture(t)
			proposal := publicationProposal(t)
			if provider == "gitlab" {
				proposal.Provider, proposal.Host, proposal.CredentialRef = provider, "gitlab.example", "glab:active"
			}
			proposal.Body = "The completed result is recorded in a later comment"
			data, err := json.Marshal(proposal)
			if err != nil {
				t.Fatal(err)
			}
			proposal.Hash, proposal.Status, proposal.At = receiptHash(string(data)), "PENDING", time.Now().UTC()
			if err := db.UpdateWorkspace(func(state *store.Workspace) error {
				state.Records.Updates = append(state.Records.Updates, proposal)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			before, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			publisher := &publicationFixture{}
			for range 2 {
				if _, err := DeliverPublication(t.Context(), db, proposal.ID, publicationScanner(t), publisher); err == nil || publisher.sends != 0 {
					t.Fatal("legacy temporal violation delivered")
				}
			}
			after, err := db.LoadWorkspace()
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("temporal refusal changed store or publication state", err)
			}
		})
	}
}

func TestPublicationIdentifierPolicyRejectsApprovalAndDelivery(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		for _, configuration := range []string{"protected", "missing", "malformed"} {
			t.Run(provider+"/"+configuration, func(t *testing.T) {
				db, _, _ := reviewFlowFixture(t)
				proposal := publicationProposal(t)
				if provider == "gitlab" {
					proposal.Provider, proposal.Host, proposal.CredentialRef = provider, "gitlab.example", "glab:active"
				}
				proposal.Body = "Private-Example-61"
				scanner := publicationScanner(t)
				hash, err := PublicationHash(proposal)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := ApprovePublication(t.Context(), db, PublicationRequest{proposal, hash}, scanner); err != nil {
					t.Fatal(err)
				}
				before, err := db.LoadWorkspace()
				if err != nil {
					t.Fatal(err)
				}
				digest := sha256.Sum256([]byte("private-example-61"))
				configured := hex.EncodeToString(digest[:])
				if configuration == "missing" {
					configured = ""
				} else if configuration == "malformed" {
					configured = "invalid"
				}
				t.Setenv("TAXION_PUBLIC_PRIVATE_IDENTIFIER_SHA256", configured)
				if _, err := PublicationHash(proposal); err == nil {
					t.Fatal("protected or unscanned proposal obtained an approval hash")
				}
				if _, err := ApprovePublication(t.Context(), db, PublicationRequest{proposal, hash}, scanner); err == nil {
					t.Fatal("protected or unscanned proposal approved")
				}
				publisher := &publicationFixture{}
				_, err = DeliverPublication(t.Context(), db, proposal.ID, scanner, publisher)
				if err == nil || publisher.sends != 0 {
					t.Fatal("protected or unscanned saved publication delivered", err)
				}
				if strings.Contains(err.Error(), proposal.Body) {
					t.Fatal("privacy diagnostic echoed the protected value")
				}
				if configuration == "missing" && !strings.Contains(err.Error(), "TAXION_PUBLIC_PRIVATE_IDENTIFIER_SHA256") {
					t.Fatal("missing configuration was hidden behind a generic content error", err)
				}
				after, err := db.LoadWorkspace()
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatal("privacy rejection changed saved publication or Task state", err)
				}
			})
		}
	}
}

func TestApprovedPublicationClaimRestartAndUncertainDelivery(t *testing.T) {
	db, ledger, _ := reviewFlowFixture(t)
	scanner := publicationScanner(t)
	proposal := publicationProposal(t)
	hash, err := PublicationHash(proposal)
	if err != nil {
		t.Fatal(err)
	}
	request := PublicationRequest{Update: proposal, ExpectedHash: hash}
	invalid := request
	invalid.Update.Body = "different report"
	if _, err := ApprovePublication(t.Context(), db, invalid, scanner); err == nil {
		t.Fatal("modified approval accepted")
	}
	if _, err := ApprovePublication(t.Context(), db, request, "missing-scanner"); err == nil {
		t.Fatal("missing scan accepted")
	}
	for range 2 {
		if _, err := ApprovePublication(t.Context(), db, request, scanner); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(filepath.Join(filepath.Dir(ledger), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	publisher := &publicationFixture{failure: true}
	update, err := DeliverPublication(t.Context(), db, proposal.ID, scanner, publisher)
	if err == nil || update.Status != "UNKNOWN" {
		t.Fatalf("%+v %v", update, err)
	}
	if _, err := DeliverPublication(t.Context(), db, proposal.ID, scanner, publisher); err == nil {
		t.Fatal("uncertain delivery resent")
	}
	if publisher.sends != 1 {
		t.Fatal("duplicate remote write")
	}
	if _, err := ReconcilePublication(t.Context(), db, proposal.ID, "wrong", true, publisher); err == nil {
		t.Fatal("wrong sender recovered")
	}
	update, err = ReconcilePublication(t.Context(), db, proposal.ID, update.Claim, true, publisher)
	if err != nil || update.Status != "SENT" || update.RemoteID != "99" || update.Error != "" {
		t.Fatalf("%+v %v", update, err)
	}
	if _, err := DeliverPublication(t.Context(), db, proposal.ID, scanner, publisher); err != nil || publisher.sends != 1 {
		t.Fatal("delivered report resent", err)
	}
	state, err := db.LoadWorkspace()
	if err != nil || len(state.Records.Updates) != 1 {
		t.Fatal("replay duplicated local approval", err)
	}
}

func TestConfirmedCloseReconciliationClearsDeliveryError(t *testing.T) {
	db, _, flow := completedFixture(t)
	proposal := publicationProposal(t)
	proposal.Action, proposal.Body, proposal.FlowID = "CLOSE", "", flow.ID
	scanner := publicationScanner(t)
	hash, err := PublicationHash(proposal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApprovePublication(t.Context(), db, PublicationRequest{proposal, hash}, scanner); err != nil {
		t.Fatal(err)
	}
	publisher := &publicationFixture{failure: true}
	update, err := DeliverPublication(t.Context(), db, proposal.ID, scanner, publisher)
	if err == nil || update.Status != "UNKNOWN" || update.Error == "" {
		t.Fatal("uncertain close did not retain its diagnostic", update, err)
	}
	update, err = ReconcilePublication(t.Context(), db, proposal.ID, update.Claim, true, publisher)
	if err != nil || update.Status != "SENT" || update.Error != "" || publisher.sends != 1 {
		t.Fatal("confirmed close retained stale error or repeated write", update, err)
	}
	state, err := db.LoadWorkspace()
	if err != nil || state.Records.Updates[0].Error != "" {
		t.Fatal("confirmed close error not cleared durably", err)
	}
}

func TestPublicationConcurrentConsumersAndCloseEvidence(t *testing.T) {
	db, ledger, flow := completedFixture(t)
	proposal := publicationProposal(t)
	scanner := publicationScanner(t)
	hash, _ := PublicationHash(proposal)
	if _, err := ApprovePublication(t.Context(), db, PublicationRequest{proposal, hash}, scanner); err != nil {
		t.Fatal(err)
	}
	other, err := store.Open(filepath.Join(filepath.Dir(ledger), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	publisher := &publicationFixture{entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() { _, err := DeliverPublication(t.Context(), db, proposal.ID, scanner, publisher); done <- err }()
	<-publisher.entered
	if _, err := DeliverPublication(t.Context(), other, proposal.ID, scanner, publisher); err == nil {
		t.Fatal("second consumer acquired claim")
	}
	close(publisher.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	closeProposal := proposal
	closeProposal.ID, closeProposal.Action, closeProposal.Body, closeProposal.FlowID = "close", "CLOSE", "", flow.ID
	hash, _ = PublicationHash(closeProposal)
	if _, err := ApprovePublication(t.Context(), db, PublicationRequest{closeProposal, hash}, scanner); err != nil {
		t.Fatal(err)
	}
	gitTest(t, flow.Contract.Worktree, "commit", "--allow-empty", "-m", "chore: external update")
	before, _ := db.LoadWorkspace()
	if _, err := DeliverPublication(t.Context(), db, closeProposal.ID, scanner, publisher); err == nil {
		t.Fatal("changed HEAD allowed Issue close")
	}
	after, _ := db.LoadWorkspace()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("failed close claim changed records")
	}
}

func TestFailureAnalysisKeepsProposedLessonAndDecision(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	dispatch := executeFlowDispatch(t, db, flow, "FAILED", true)
	body, _ := json.Marshal(dispatch.Result)
	record := store.FailureRecord{ID: "failure-1", TaskID: dispatch.TaskID, DispatchID: dispatch.ID,
		ResultHash: receiptHash(string(body)), Cause: "missing tool", Correction: "restore approved tool",
		LessonCandidate: "check tool availability before dispatch", ValidationEvidence: []string{"dispatch:" + dispatch.ID + ":result"},
		Actor: "human", DecisionSource: "human:failure-disposition"}
	for range 2 {
		if _, err := RecordFailureAnalysis(db, record); err != nil {
			t.Fatal(err)
		}
	}
	record.Cause = "new decision"
	if _, err := RecordFailureAnalysis(db, record); err == nil {
		t.Fatal("decision overwritten")
	}
	record.ID = "invented-validation"
	record.ValidationEvidence = []string{"invented:test-success"}
	if _, err := RecordFailureAnalysis(db, record); err == nil {
		t.Fatal("unsaved validation evidence accepted")
	}
	state, _ := db.LoadWorkspace()
	if len(state.Records.Findings) != 1 || state.Graph.Tasks[0].Status != core.Blocked {
		t.Fatal("finding changed execution decision")
	}
}

func TestCreateIssueRecoveryLinksOnlyApprovedDraft(t *testing.T) {
	db, ledger, _ := reviewFlowFixture(t)
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		task, _ := state.Graph.GraphTask("implementation")
		task.Status, task.Issue = core.Draft, "PENDING:registration"
		task.DependsOn = []core.TaskDependency{{TaskID: "registration", State: core.Completed}}
		graph := core.TaskGraph{Version: 1}
		registration := task
		registration.ID, registration.Type, registration.Issue = "registration", "EVIDENCE_COLLECTION", "Issue #56"
		registration.DependsOn = nil
		if err := graph.Track(registration, "human", "track Issue registration evidence"); err != nil {
			return err
		}
		if err := graph.Track(task, "human", "prepare independent Issue registration"); err != nil {
			return err
		}
		state.Graph = graph
		state.Ledger.Tasks = append(state.Ledger.Tasks, core.Task{ID: registration.ID, Title: registration.Title, Repo: registration.Repo})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	proposal := publicationProposal(t)
	proposal.Action, proposal.Number, proposal.Title = "CREATE", 0, "Independent implementation"
	hash, _ := PublicationHash(proposal)
	scanner := publicationScanner(t)
	if _, err := ApprovePublication(t.Context(), db, PublicationRequest{proposal, hash}, scanner); err != nil {
		t.Fatal(err)
	}
	duplicate := proposal
	duplicate.ID = "different-registration"
	duplicateHash, _ := PublicationHash(duplicate)
	if _, err := ApprovePublication(t.Context(), db, PublicationRequest{duplicate, duplicateHash}, scanner); err == nil {
		t.Fatal("duplicate Task registration accepted")
	}
	publisher := &publicationFixture{failure: true}
	update, err := DeliverPublication(t.Context(), db, proposal.ID, scanner, publisher)
	if err == nil || update.Status != "UNKNOWN" {
		t.Fatal(update, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(filepath.Join(filepath.Dir(ledger), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	update, err = ReconcilePublication(t.Context(), db, proposal.ID, update.Claim, true, publisher)
	if err != nil || update.Status != "SENT" || publisher.sends != 1 {
		t.Fatal(update, err)
	}
	state, err := db.LoadWorkspace()
	task, _ := state.Graph.GraphTask(proposal.TaskID)
	if err != nil || task.Issue != "Issue #99" || task.Status != core.Draft {
		t.Fatal("Issue registration lost or authorized execution", task, err)
	}
	if _, err := ApprovePublication(t.Context(), db, PublicationRequest{proposal, hash}, scanner); err != nil {
		t.Fatal("registration replay rejected", err)
	}
}
