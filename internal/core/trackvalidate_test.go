package core

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func ledgerFor(task GraphTask) Ledger {
	return Ledger{Tasks: []Task{{ID: task.ID, Title: task.Title, Repo: task.Repo}}}
}

func validDraftTask(id string) GraphTask {
	return GraphTask{
		ID: id, Title: "contract validation", Repo: "owner/repo", Type: "IMPLEMENTATION", Status: Draft,
		Issue: "Issue #108", CreatedFrom: "human:1", Owner: "CODER", Objective: "validate contracts",
		Scope: []string{"internal/core"}, OutOfScope: []string{"web"}, Acceptance: []string{"report"},
		RequiredEvidence: []string{"report"}, ExitCondition: "report", NextAction: "implement",
		ValidationCommands: []string{"go test ./..."}, Branch: "work/contract", Worktree: "/tmp/contract",
		BaselineSHA: strings.Repeat("a", 40),
	}
}

func TestValidateTrackingReportsAllContractErrors(t *testing.T) {
	graph := TaskGraph{Version: 1}
	bad := GraphTask{ID: "bad", Repo: "owner/repo", Status: Draft}
	report := graph.ValidateTracking(bad, "human", "draft", nil, Ledger{}, false)
	if report.Valid {
		t.Fatal("invalid task was accepted")
	}
	if len(report.ValidatorErrors) < 3 {
		t.Fatalf("validator errors were not aggregated: %v", report.ValidatorErrors)
	}
	if report.StateChanges != "none" || report.CanonicalJSON != "" || report.CanonicalSHA256 != "" {
		t.Fatalf("invalid contract produced a digest: %+v", report)
	}
}

func TestValidateTrackingMatchesTrackAndKeepsState(t *testing.T) {
	graph := TaskGraph{Version: 1}
	task := validDraftTask("contract")
	report := graph.ValidateTracking(task, "human", "draft", nil, ledgerFor(task), false)
	if !report.Valid {
		t.Fatalf("valid draft was rejected: %v", report.ValidatorErrors)
	}
	if report.ExistingTask || report.StateChanges != "none" {
		t.Fatalf("unexpected report state: %+v", report)
	}
	if len(graph.Tasks) != 0 {
		t.Fatal("ValidateTracking mutated the receiver graph")
	}
	copied := graph
	if err := copied.Track(task, "human", "draft"); err != nil {
		t.Fatalf("track rejected the validated contract: %v", err)
	}
	if len(graph.Tasks) != 0 {
		t.Fatal("Track ran on the validate graph")
	}

	for _, mutate := range []func(*GraphTask){
		func(t *GraphTask) { t.Status = Ready },
		func(t *GraphTask) { t.Type = "UNKNOWN" },
		func(t *GraphTask) { t.MemoryRefs = []string{"not-a-reference"} },
		func(t *GraphTask) { t.Supersedes = "missing" },
		func(t *GraphTask) { t.DependsOn = []TaskDependency{{TaskID: "missing", State: Completed}} },
	} {
		changed := task
		mutate(&changed)
		rejected := graph.ValidateTracking(changed, "human", "draft", nil, ledgerFor(changed), false)
		candidate := graph
		trackErr := candidate.Track(changed, "human", "draft")
		if rejected.Valid != (trackErr == nil) {
			t.Fatalf("verdicts disagree for %+v: validate=%+v track=%v", changed, rejected.ValidatorErrors, trackErr)
		}
		if rejected.Valid {
			t.Fatalf("invalid contract was accepted: %+v", changed)
		}
	}
}

func TestValidateTrackingKeepsExistingTaskRules(t *testing.T) {
	graph := TaskGraph{Version: 1}
	task := validDraftTask("contract")
	if err := graph.Track(task, "human", "draft"); err != nil {
		t.Fatal(err)
	}
	report := graph.ValidateTracking(task, "human", "draft", nil, ledgerFor(task), false)
	if !report.Valid || !report.ExistingTask {
		t.Fatalf("existing contract rejected: %+v", report)
	}
	edited := task
	edited.Repo = "other/repo"
	rejected := graph.ValidateTracking(edited, "human", "draft", nil, ledgerFor(edited), false)
	candidate := graph
	if rejected.Valid != (candidate.Track(edited, "human", "draft") == nil) {
		t.Fatalf("verdicts disagree on identity edit: %+v", rejected.ValidatorErrors)
	}
	if rejected.Valid {
		t.Fatal("identity edit was accepted")
	}
}

func TestValidateTrackingWithholdsCredentialLikeValues(t *testing.T) {
	graph := TaskGraph{Version: 1}
	task := validDraftTask("contract")
	task.Objective = "use ghp_" + strings.Repeat("x", 36)
	report := graph.ValidateTracking(task, "human", "draft", nil, ledgerFor(task), false)
	if !report.Valid {
		t.Fatalf("credential scan changed the track verdict: %v", report.ValidatorErrors)
	}
	if !report.DigestWithheld || report.CanonicalJSON != "" || report.CanonicalSHA256 != "" {
		t.Fatalf("credential-like value was not withheld: %+v", report)
	}

	graph = TaskGraph{Version: 1}
	google := validDraftTask("contract")
	google.Objective = "key AIza" + strings.Repeat("Q", 35)
	if report := graph.ValidateTracking(google, "human", "draft", nil, ledgerFor(google), false); !report.Valid || !report.DigestWithheld {
		t.Fatalf("Google API key was not withheld: %+v", report)
	}

	graph = TaskGraph{Version: 1}
	hyphen := validDraftTask("contract")
	hyphen.Objective = "key AIza" + strings.Repeat("Q", 34) + "-"
	if report := graph.ValidateTracking(hyphen, "human", "draft", nil, ledgerFor(hyphen), false); !report.DigestWithheld {
		t.Fatalf("AIza key ending in a hyphen was not withheld: %+v", report)
	}

	graph = TaskGraph{Version: 1}
	task = validDraftTask("glpat-" + strings.Repeat("y", 30))
	task.Title = ""
	task.MemoryRefs = []string{"ghp_" + strings.Repeat("z", 36)}
	rejected := graph.ValidateTracking(task, "human", "draft", nil, ledgerFor(task), false)
	if rejected.Valid {
		t.Fatal("invalid contract with credential-like values was accepted")
	}
	if strings.Contains(rejected.TaskID, "glpat-") {
		t.Fatalf("Task ID leaked a credential-like value: %q", rejected.TaskID)
	}
	for _, message := range rejected.ValidatorErrors {
		if strings.Contains(message, "ghp_") || strings.Contains(message, "glpat-") {
			t.Fatalf("diagnostic leaked a credential-like value: %q", message)
		}
	}
	found := false
	for _, message := range rejected.ValidatorErrors {
		if strings.Contains(message, "[REDACTED]") {
			found = true
		}
	}
	if !found {
		t.Fatalf("redaction marker is missing: %v", rejected.ValidatorErrors)
	}
}

func TestValidateTrackingReportsDuplicateDecisions(t *testing.T) {
	graph := TaskGraph{Version: 1}
	task := validDraftTask("contract")
	task.Title = ""
	task.Decisions = []TaskDecision{
		{ID: "d1", Source: "human:1", Text: "first"},
		{ID: "d1", Source: "human:1", Text: "second"},
	}
	report := graph.ValidateTracking(task, "human", "draft", nil, ledgerFor(task), false)
	if report.Valid {
		t.Fatal("contract with conflicting decisions was accepted")
	}
	foundConflict, foundTitle := false, false
	for _, message := range report.ValidatorErrors {
		if strings.Contains(message, "conflicting decision d1") {
			foundConflict = true
		}
		if strings.Contains(message, "title") {
			foundTitle = true
		}
	}
	if !foundConflict || !foundTitle {
		t.Fatalf("violations were not aggregated: %v", report.ValidatorErrors)
	}
}

func TestValidateTrackingRedactsPrivateKeyBodies(t *testing.T) {
	graph := TaskGraph{Version: 1}
	task := validDraftTask("contract")
	begin, end := "-----BEGIN RSA "+"PRIVATE KEY-----", "-----END RSA "+"PRIVATE KEY-----"
	task.MemoryRefs = []string{begin + "\nMIIEowIBAAKCAQEAsupersecretbody\n" + end}
	report := graph.ValidateTracking(task, "human", "draft", nil, ledgerFor(task), false)
	if report.Valid {
		t.Fatal("invalid contract was accepted")
	}
	for _, message := range report.ValidatorErrors {
		if strings.Contains(message, "supersecretbody") {
			t.Fatalf("private key body leaked: %q", message)
		}
	}
}

func TestValidateTrackingKeepsProseWithoutCredentials(t *testing.T) {
	graph := TaskGraph{Version: 1}
	task := validDraftTask("contract")
	task.Objective = "document AKIA handling and ghp_ rotation for operators"
	report := graph.ValidateTracking(task, "human", "draft", nil, ledgerFor(task), false)
	if !report.Valid || report.DigestWithheld || report.CanonicalSHA256 == "" {
		t.Fatalf("prose was mistaken for a credential: %+v", report)
	}
}

func TestValidateTrackingReportsLedgerProjection(t *testing.T) {
	graph := TaskGraph{Version: 1}
	task := validDraftTask("contract")
	missing := graph.ValidateTracking(task, "human", "draft", nil, Ledger{}, false)
	if missing.Valid {
		t.Fatal("contract without a ledger task was accepted")
	}
	stale := ledgerFor(task)
	stale.Tasks[0].Title = "renamed elsewhere"
	renamed := graph.ValidateTracking(task, "human", "draft", nil, stale, false)
	if renamed.Valid {
		t.Fatal("contract with a stale ledger identity was accepted")
	}
}

func TestValidateTrackingReportsIndependentConcernErrors(t *testing.T) {
	graph := TaskGraph{Version: 1}
	broken := validDraftTask("contract")
	broken.Title = ""
	report := graph.ValidateTracking(broken, "human", "draft", &Concern{ID: "c1", TaskID: broken.ID, Source: "human:1", Text: "naming"}, ledgerFor(broken), false)
	if report.Valid {
		t.Fatal("invalid contract was accepted")
	}
	for _, message := range report.ValidatorErrors {
		if strings.Contains(message, "concern") {
			t.Fatalf("spurious concern error for an invalid contract: %q", message)
		}
	}
	invalid := graph.ValidateTracking(broken, "human", "draft", &Concern{ID: "c2", TaskID: broken.ID, Source: "human:1"}, ledgerFor(broken), false)
	found := false
	for _, message := range invalid.ValidatorErrors {
		if strings.Contains(message, "concern") {
			found = true
		}
	}
	if !found {
		t.Fatalf("independent concern error was dropped: %v", invalid.ValidatorErrors)
	}
}

func TestValidateTrackingReportsConcernAndDigest(t *testing.T) {
	graph := TaskGraph{Version: 1}
	task := validDraftTask("contract")
	report := graph.ValidateTracking(task, "human", "draft", &Concern{ID: "c1", TaskID: task.ID, Source: "human:1", Text: "naming"}, ledgerFor(task), false)
	if !report.Valid {
		t.Fatalf("valid contract with concern rejected: %v", report.ValidatorErrors)
	}
	if report.CanonicalSHA256 == "" || len(report.CanonicalJSON) == 0 {
		t.Fatal("contract digest is missing")
	}
	again := graph.ValidateTracking(task, "human", "draft", &Concern{ID: "c1", TaskID: task.ID, Source: "human:1", Text: "naming"}, ledgerFor(task), false)
	if again.CanonicalSHA256 != report.CanonicalSHA256 || again.CanonicalJSON != report.CanonicalJSON {
		t.Fatal("contract digest is not stable")
	}
	stored := graph
	if err := stored.Track(task, "human", "draft"); err != nil {
		t.Fatal(err)
	}
	registered, ok := stored.GraphTask(task.ID)
	if !ok {
		t.Fatal("tracked contract is missing")
	}
	expected, err := CanonicalSHA256(registered)
	if err != nil {
		t.Fatal(err)
	}
	if expected != report.CanonicalSHA256 {
		t.Fatalf("digest does not describe the stored contract: %s != %s", expected, report.CanonicalSHA256)
	}
	if registered.FileAccess == nil {
		t.Fatal("default file access policy was not applied to the stored contract")
	}
	lost := graph.ValidateTracking(task, "human", "draft", &Concern{ID: "c2", TaskID: "missing", Source: "human:1", Text: "naming"}, ledgerFor(task), false)
	if lost.Valid {
		t.Fatal("concern for an unregistered task was accepted")
	}
}

func TestCanonicalJSONIsOrderIndependent(t *testing.T) {
	first, err := CanonicalJSON(map[string]any{"b": 1, "a": []any{1, 2}, "c": "日本語 <tag>"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(map[string]any{"c": "日本語 <tag>", "a": []any{1, 2}, "b": 1})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := CanonicalJSON(json.RawMessage(second))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, decoded) {
		t.Fatalf("canonical bytes differ by key order:\n%s\n%s", first, decoded)
	}
	if bytes.Contains(first, []byte("\\u")) {
		t.Fatalf("canonical encoding escaped unicode: %s", first)
	}
	if bytes.Contains(first, []byte("\\u003c")) {
		t.Fatalf("canonical encoding escaped HTML: %s", first)
	}
	digest, err := CanonicalSHA256(map[string]any{"a": 1})
	if err != nil || len(digest) != 64 {
		t.Fatalf("unexpected digest %q: %v", digest, err)
	}
}

func TestCanonicalJSONKeepsLineSeparators(t *testing.T) {
	data, err := CanonicalJSON(map[string]string{"text": "a\u2028b\u2029c"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "a\u2028b\u2029c") || strings.Contains(string(data), `\u2028`) {
		t.Fatalf("line separators were escaped: %q", string(data))
	}
}

func TestCanonicalJSONKeepsLiteralEscapes(t *testing.T) {
	task := validDraftTask("contract")
	task.Objective = "literal \\u2028 and \\u0041 stay as text"
	data, err := CanonicalJSON(task)
	if err != nil {
		t.Fatal(err)
	}
	var decoded GraphTask
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("canonical output is not valid JSON: %v", err)
	}
	if decoded.Objective != task.Objective {
		t.Fatalf("canonicalization corrupted a literal escape: %q", decoded.Objective)
	}
	separator := validDraftTask("contract")
	separator.Objective = "a\u2028b"
	data, err = CanonicalJSON(separator)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &separator); err != nil {
		t.Fatalf("canonical output with a line separator is not valid JSON: %v", err)
	}
	if separator.Objective != "a\u2028b" {
		t.Fatalf("line separator was not preserved: %q", separator.Objective)
	}
	if strings.Contains(string(data), `\\u2028`) {
		t.Fatalf("line separator was still escaped: %s", data)
	}
}
