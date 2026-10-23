package forge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/publictext"
	"github.com/hiono/taxion/internal/store"
)

func gitlabPublicationCLI(t *testing.T, responses map[string]string) string {
	t.Helper()
	publicationProfiles(t)
	directory := t.TempDir()
	request := filepath.Join(directory, "request.json")
	script := `#!/bin/sh
[ "$1" = api ] && [ "$2" = --hostname ] && [ "$3" = gitlab.example ] && [ "$4" = --method ] || exit 2
[ -z "$GITLAB_ACCESS_TOKEN" ] && [ -z "$GLAB_HOST" ] && [ "$GITLAB_TOKEN" = selected-fixture-token ] || exit 3
case "$5" in POST|PUT) printf '%s\n' "$@" > '` + request + `' ;; esac
case "$5:$6" in
`
	for endpoint, body := range responses {
		path := filepath.Join(directory, strings.ReplaceAll(endpoint, "/", "_")+".json")
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		script += "'" + endpoint + "') cat '" + path + "';;\n"
	}
	script += "*) echo private-provider-output >&2; exit 5;;\nesac\n"
	if err := os.WriteFile(filepath.Join(directory, "glab"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+":"+os.Getenv("PATH"))
	t.Setenv("TAXION_PUBLICATION_TEST_GITLAB_TOKEN", "selected-fixture-token")
	t.Setenv("GITLAB_ACCESS_TOKEN", "runtime-only-fixture")
	t.Setenv("GLAB_HOST", "unselected.example")
	return request
}

func gitlabPublication() store.ExternalUpdate {
	return store.ExternalUpdate{
		Statement: publictext.StatementContext{At: time.Now().UTC()},
		ID:        "accepted-gitlab-report", TaskID: "implementation", Provider: "gitlab",
		Host: "gitlab.example", Repository: "group/subgroup/repo", Account: "scm-user",
		CredentialRef: "env:TAXION_PUBLICATION_TEST_GITLAB_TOKEN", Actor: "human",
		ApprovalSource: "human:exact-report", Kind: "ISSUE", Number: 21,
		Action: "COMMENT", Body: "approved report", Hash: strings.Repeat("a", 64),
	}
}

func TestGitLabIssuePublicationAndMergeRequestComment(t *testing.T) {
	update := gitlabPublication()
	base := "projects/group%2Fsubgroup%2Frepo"
	project := `{"path_with_namespace":"group/subgroup/repo","permissions":{"project_access":{"access_level":50}}}`
	issue := update
	issue.Number = 21
	noteBody := issue.Body + "\n\n" + PublicationMarker(issue)
	note, _ := json.Marshal([]map[string]any{{"id": 410, "body": noteBody, "author": map[string]string{"username": "scm-user"}}})
	response, _ := json.Marshal(map[string]any{"id": 411, "body": noteBody, "author": map[string]string{"username": "SCM-USER"}})
	request := gitlabPublicationCLI(t, map[string]string{
		"GET:user": `{"username":"scm-user"}`, "GET:" + base: project,
		"GET:" + base + "/issues/21":                           `{"iid":21}`,
		"GET:" + base + "/issues/21/notes?per_page=100&page=1": string(note),
		"POST:" + base + "/issues/21/notes":                    string(response),
	})
	publisher := Publisher{}
	if err := publisher.Check(t.Context(), issue); err != nil {
		t.Fatal(err)
	}
	if id, found, err := publisher.Reconcile(t.Context(), issue); err != nil || !found || id != "410" {
		t.Fatal(id, found, err)
	}
	if id, err := publisher.Send(t.Context(), issue); err != nil || id != "411" {
		t.Fatal(id, err)
	}
	body, err := os.ReadFile(request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "body="+noteBody) {
		t.Fatal("unapproved GitLab note payload", string(body))
	}

	mergeRequest := issue
	mergeRequest.Kind = "PR"
	mergeRequest.Number = 7
	mrNote := mergeRequest.Body + "\n\n" + PublicationMarker(mergeRequest)
	mrResponse, _ := json.Marshal(map[string]any{"id": 512, "body": mrNote, "author": map[string]string{"username": "scm-user"}})
	gitlabPublicationCLI(t, map[string]string{
		"GET:user": `{"username":"scm-user"}`, "GET:" + base: project,
		"GET:" + base + "/merge_requests/7":                           `{"iid":7}`,
		"GET:" + base + "/merge_requests/7/notes?per_page=100&page=1": `[]`,
		"POST:" + base + "/merge_requests/7/notes":                    string(mrResponse),
	})
	if err := publisher.Check(t.Context(), mergeRequest); err != nil {
		t.Fatal(err)
	}
	if id, err := publisher.Send(t.Context(), mergeRequest); err != nil || id != "512" {
		t.Fatal(id, err)
	}
}

func TestGitLabIssueCreateCloseAndReconciliation(t *testing.T) {
	base := "projects/group%2Fsubgroup%2Frepo"
	project := `{"path_with_namespace":"group/subgroup/repo","permissions":{"project_access":{"access_level":50}}}`
	publisher := Publisher{}
	create := gitlabPublication()
	create.Action, create.Number, create.Kind = "CREATE", 0, "ISSUE"
	create.Title = "approved title"
	create.Body = "approved description"
	description := create.Body + "\n\n" + PublicationMarker(create)
	issues, _ := json.Marshal([]map[string]any{{"iid": 35, "title": create.Title,
		"description": description, "author": map[string]string{"username": "scm-user"}}})
	created, _ := json.Marshal(map[string]any{"iid": 36, "title": create.Title,
		"description": description, "author": map[string]string{"username": "scm-user"}})
	request := gitlabPublicationCLI(t, map[string]string{
		"GET:user": `{"username":"scm-user"}`, "GET:" + base: project,
		"GET:" + base + "/issues?state=all&per_page=100&page=1": string(issues),
		"POST:" + base + "/issues":                              string(created),
	})
	if err := publisher.Check(t.Context(), create); err != nil {
		t.Fatal(err)
	}
	if id, found, err := publisher.Reconcile(t.Context(), create); err != nil || !found || id != "35" {
		t.Fatal(id, found, err)
	}
	if id, err := publisher.Send(t.Context(), create); err != nil || id != "36" {
		t.Fatal(id, err)
	}
	body, err := os.ReadFile(request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "title="+create.Title) ||
		!strings.Contains(string(body), "description="+description) {
		t.Fatal("unapproved GitLab Issue payload", string(body))
	}

	closeUpdate := gitlabPublication()
	closeUpdate.Action, closeUpdate.Body, closeUpdate.Number = "CLOSE", "", 36
	closeUpdate.FlowID, closeUpdate.At = "completed-flow", time.Now().Add(-time.Minute)
	opened := `{"iid":36,"state":"opened"}`
	gitlabPublicationCLI(t, map[string]string{
		"GET:user": `{"username":"scm-user"}`, "GET:" + base: project,
		"GET:" + base + "/issues/36": opened,
	})
	if err := publisher.Check(t.Context(), closeUpdate); err != nil {
		t.Fatal(err)
	}
	if id, found, err := publisher.Reconcile(t.Context(), closeUpdate); err != nil || found || id != "36" {
		t.Fatal(id, found, err)
	}
	closedAt := time.Now().UTC().Format(time.RFC3339Nano)
	closed := `{"iid":36,"state":"closed","closed_at":"` + closedAt + `","closed_by":{"username":"scm-user"}}`
	request = gitlabPublicationCLI(t, map[string]string{
		"GET:" + base + "/issues/36": closed,
		"PUT:" + base + "/issues/36": closed,
	})
	if id, found, err := publisher.Reconcile(t.Context(), closeUpdate); err != nil || !found || id != "36" {
		t.Fatal(id, found, err)
	}
	if id, err := publisher.Send(t.Context(), closeUpdate); err != nil || id != "36" {
		t.Fatal(id, err)
	}
	body, err = os.ReadFile(request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "state_event=close") {
		t.Fatal("unexpected GitLab close payload", string(body))
	}
}

func TestGitLabPublicationRejectsConflictingEvidence(t *testing.T) {
	update := gitlabPublication()
	base := "projects/group%2Fsubgroup%2Frepo"
	project := `{"path_with_namespace":"group/subgroup/repo","permissions":{"project_access":{"access_level":50}}}`
	conflicting, _ := json.Marshal([]map[string]any{{"id": 410,
		"body":   update.Body + "\n\n" + PublicationMarker(update),
		"author": map[string]string{"username": "another-user"}}})
	gitlabPublicationCLI(t, map[string]string{
		"GET:user": `{"username":"scm-user"}`, "GET:" + base: project,
		"GET:" + base + "/issues/21":                           `{"iid":21}`,
		"GET:" + base + "/issues/21/notes?per_page=100&page=1": string(conflicting),
	})
	if _, _, err := (Publisher{}).Reconcile(t.Context(), update); err == nil {
		t.Fatal("conflicting author accepted")
	}
	update.Host = "--help"
	if err := (Publisher{}).Check(t.Context(), update); err == nil {
		t.Fatal("invalid host accepted")
	}
}

func TestGitLabClosedIssueReconciliationEvidence(t *testing.T) {
	update := gitlabPublication()
	update.Action, update.Body, update.FlowID, update.At = "CLOSE", "", "completed-flow", time.Now().Add(-time.Minute)
	base := "projects/group%2Fsubgroup%2Frepo"
	project := `{"path_with_namespace":"group/subgroup/repo","permissions":{"project_access":{"access_level":50}}}`
	for _, test := range []struct {
		name, closedAt, actor string
		found, denied         bool
	}{
		{"recent-selected-actor", time.Now().UTC().Format(time.RFC3339Nano), "scm-user", true, false},
		{"different-actor", time.Now().UTC().Format(time.RFC3339Nano), "another-user", false, true},
		{"stale-close", time.Now().Add(-7 * time.Minute).UTC().Format(time.RFC3339Nano), "scm-user", false, true},
		{"just-before-approval", update.At.Add(-time.Second).UTC().Format(time.RFC3339Nano), "scm-user", false, true},
		{"future-close", time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), "scm-user", false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			issue, _ := json.Marshal(map[string]any{"iid": update.Number, "state": "closed",
				"closed_at": test.closedAt, "closed_by": map[string]string{"username": test.actor}})
			gitlabPublicationCLI(t, map[string]string{
				"GET:user": `{"username":"scm-user"}`, "GET:" + base: project,
				"GET:" + base + "/issues/21": string(issue),
			})
			publisher := Publisher{}
			if err := publisher.Check(t.Context(), update); err != nil {
				t.Fatal("closed Issue could not be checked for recovery", err)
			}
			id, found, err := publisher.Reconcile(t.Context(), update)
			if (err != nil) != test.denied || found != test.found ||
				!test.denied && test.found && id != "21" {
				t.Fatalf("unexpected close reconciliation: id=%q found=%v err=%v", id, found, err)
			}
		})
	}
}

func TestLiveGitLabIssuePublication(t *testing.T) {
	if os.Getenv("TAXION_GITLAB_WRITE_INTEGRATION") != "1" {
		t.Skip("set TAXION_GITLAB_WRITE_INTEGRATION=1 to create, comment on, and close one temporary Issue")
	}
	host := os.Getenv("TAXION_GITLAB_TEST_HOST")
	account := os.Getenv("TAXION_GITLAB_TEST_ACCOUNT")
	repository := os.Getenv("TAXION_GITLAB_TEST_PROJECT")
	credential := os.Getenv("TAXION_GITLAB_TEST_CREDENTIAL_REF")
	if host == "" || account == "" || repository == "" {
		t.Fatal("GitLab write test requires explicit host, account, and project")
	}
	if credential == "" {
		credential = "glab:active"
	}
	publisher := Publisher{}
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	makeUpdate := func(action, id, body string) store.ExternalUpdate {
		hash := sha256.Sum256([]byte(id + "\n" + body))
		return store.ExternalUpdate{
			Statement: publictext.StatementContext{At: time.Now().UTC()},
			ID:        id, TaskID: "gitlab-api-smoke-" + suffix, Provider: "gitlab",
			Host: host, Repository: repository, Account: account, CredentialRef: credential,
			Kind: "ISSUE", Action: action, Body: body, Hash: hex.EncodeToString(hash[:]),
		}
	}
	create := makeUpdate("CREATE", "gitlab-create-"+suffix, "Temporary Taxion GitLab adapter smoke test "+suffix)
	create.Title = "Taxion temporary adapter smoke " + suffix
	if err := publisher.Check(t.Context(), create); err != nil {
		t.Fatal(err)
	}
	if _, found, err := publisher.Reconcile(t.Context(), create); err != nil || found {
		t.Fatalf("unique temporary Issue marker already exists: found=%v err=%v", found, err)
	}
	remoteID, err := publisher.Send(t.Context(), create)
	if err != nil {
		t.Fatal(err)
	}
	number, err := strconv.Atoi(remoteID)
	if err != nil || number <= 0 {
		t.Fatalf("invalid created Issue IID %q: %v", remoteID, err)
	}

	comment := makeUpdate("COMMENT", "gitlab-comment-"+suffix, "Temporary adapter comment "+suffix)
	comment.Number = number
	if err := publisher.Check(t.Context(), comment); err != nil {
		t.Fatal(err)
	}
	if _, found, err := publisher.Reconcile(t.Context(), comment); err != nil || found {
		t.Fatalf("unique temporary note marker already exists: found=%v err=%v", found, err)
	}
	if _, err := publisher.Send(t.Context(), comment); err != nil {
		t.Fatal(err)
	}
	if id, found, err := publisher.Reconcile(t.Context(), comment); err != nil || !found || id == "" {
		t.Fatalf("temporary note was not reconciled: id=%q found=%v err=%v", id, found, err)
	}

	closeUpdate := makeUpdate("CLOSE", "gitlab-close-"+suffix, "")
	closeUpdate.Number, closeUpdate.FlowID, closeUpdate.At = number, "smoke-flow", time.Now().UTC()
	if err := publisher.Check(t.Context(), closeUpdate); err != nil {
		t.Fatal(err)
	}
	if id, found, err := publisher.Reconcile(t.Context(), closeUpdate); err != nil || found || id != remoteID {
		t.Fatalf("open temporary Issue was incorrectly reconciled as closed: id=%q found=%v err=%v", id, found, err)
	}
	if _, err := publisher.Send(t.Context(), closeUpdate); err != nil {
		t.Fatal(err)
	}
	if id, found, err := publisher.Reconcile(t.Context(), closeUpdate); err != nil || !found || id != remoteID {
		t.Fatalf("temporary Issue close was not reconciled: id=%q found=%v err=%v", id, found, err)
	}
}

func TestLiveGitLabClosedIssueReconciliation(t *testing.T) {
	iid, err := strconv.Atoi(os.Getenv("TAXION_GITLAB_CLOSED_ISSUE_IID"))
	if err != nil || iid <= 0 {
		t.Skip("set TAXION_GITLAB_CLOSED_ISSUE_IID to reconcile a previously closed test Issue")
	}
	host, account, repository := os.Getenv("TAXION_GITLAB_TEST_HOST"),
		os.Getenv("TAXION_GITLAB_TEST_ACCOUNT"), os.Getenv("TAXION_GITLAB_TEST_PROJECT")
	if host == "" || account == "" || repository == "" {
		t.Fatal("GitLab reconciliation test requires explicit host, account, and project")
	}
	credential := os.Getenv("TAXION_GITLAB_TEST_CREDENTIAL_REF")
	if credential == "" {
		credential = "glab:active"
	}
	update := gitlabPublication()
	update.Host, update.Repository, update.Account, update.CredentialRef = host, repository, account, credential
	update.Action, update.Body, update.Number = "CLOSE", "", iid
	update.At = time.Now().Add(-30 * time.Minute)
	publisher := Publisher{}
	if err := publisher.Check(t.Context(), update); err != nil {
		t.Fatal(err)
	}
	if id, found, err := publisher.Reconcile(t.Context(), update); err != nil || !found || id != strconv.Itoa(iid) {
		t.Fatalf("closed test Issue was not reconciled: id=%q found=%v err=%v", id, found, err)
	}
}
