package forge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/publictext"
	"github.com/hiono/taxion/internal/store"
)

func publicationCLI(t *testing.T, responses map[string]string) string {
	t.Helper()
	publicationProfiles(t)
	directory := t.TempDir()
	request := filepath.Join(directory, "request.json")
	script := "#!/bin/sh\n[ \"$1\" = api ] && [ \"$2\" = --hostname ] && [ \"$3\" = github.com ] && [ \"$4\" = --method ] || exit 2\n" +
		"[ -z \"$COPILOT_GITHUB_TOKEN\" ] && [ -z \"$GH_DEBUG\" ] && [ \"$GH_TOKEN\" = selected-fixture-token ] || exit 3\n" +
		"case \"$5\" in POST|PATCH) [ \"$7\" = --input ] && [ \"$8\" = - ] || exit 4; cat > '" + request + "';; esac\ncase \"$5:$6\" in\n"
	for endpoint, body := range responses {
		path := filepath.Join(directory, strings.ReplaceAll(endpoint, "/", "_")+".json")
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		script += "'" + endpoint + "') cat '" + path + "';;\n"
	}
	script += "*) echo private-provider-output >&2; exit 5;;\nesac\n"
	if err := os.WriteFile(filepath.Join(directory, "gh"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+":"+os.Getenv("PATH"))
	t.Setenv("TAXION_PUBLICATION_TEST_TOKEN", "selected-fixture-token")
	t.Setenv("COPILOT_GITHUB_TOKEN", "runtime-only-fixture")
	t.Setenv("GH_DEBUG", "unselected-fixture")
	return request
}

func forgePublication() store.ExternalUpdate {
	return store.ExternalUpdate{Provider: "github", Host: "github.com", Repository: "owner/repo",
		Statement: publictext.StatementContext{At: time.Now().UTC()},
		Account:   "owner", CredentialRef: "env:TAXION_PUBLICATION_TEST_TOKEN",
		Kind: "ISSUE", Number: 53, Action: "COMMENT", Body: "approved report", Hash: strings.Repeat("a", 64)}
}

func publicationProfiles(t *testing.T) {
	t.Helper()
	profiles := publictext.Profiles{Version: 1}
	for _, binding := range []struct{ provider, host, repository string }{
		{"github", "github.com", "owner/repo"},
		{"gitlab", "gitlab.example", "group/subgroup/repo"},
	} {
		profiles.Projects = append(profiles.Projects, publictext.Profile{Version: 1, Provider: binding.provider,
			Host: binding.host, Repository: binding.repository, PrimaryLanguage: "en", SecondaryLanguages: []string{},
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
	sum := sha256.Sum256([]byte("private-example-47"))
	t.Setenv("TAXION_PUBLIC_PRIVATE_IDENTIFIER_SHA256", hex.EncodeToString(sum[:]))
}

func TestPublicationCLIIdentityPaginationAndApprovedPayload(t *testing.T) {
	update := forgePublication()
	first := make([]map[string]any, 100)
	for i := range first {
		first[i] = map[string]any{"id": i + 1, "body": "other", "user": map[string]string{"login": "owner"}}
	}
	page, _ := json.Marshal(first)
	last, _ := json.Marshal([]map[string]any{{"id": 101, "body": update.Body + "\n\n" + PublicationMarker(update),
		"user": map[string]string{"login": "OWNER"}}})
	response, _ := json.Marshal(map[string]any{"id": 102, "body": update.Body + "\n\n" + PublicationMarker(update), "user": map[string]string{"login": "owner"}})
	request := publicationCLI(t, map[string]string{
		"GET:user":                       `{"login":"owner"}`,
		"GET:repos/owner/repo":           `{"full_name":"owner/repo","permissions":{"push":true}}`,
		"GET:repos/owner/repo/issues/53": `{"number":53}`,
		"GET:repos/owner/repo/issues/53/comments?per_page=100&page=1": string(page),
		"GET:repos/owner/repo/issues/53/comments?per_page=100&page=2": string(last),
		"POST:repos/owner/repo/issues/53/comments":                    string(response),
	})
	publisher := Publisher{}
	if err := publisher.Check(t.Context(), update); err != nil {
		t.Fatal(err)
	}
	if id, found, err := publisher.Reconcile(t.Context(), update); err != nil || !found || id != "101" {
		t.Fatal(id, found, err)
	}
	if id, err := publisher.Send(t.Context(), update); err != nil || id != "102" {
		t.Fatal(id, err)
	}
	body, err := os.ReadFile(request)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]string
	if json.Unmarshal(body, &payload) != nil || len(payload) != 1 || payload["body"] != update.Body+"\n\n"+PublicationMarker(update) {
		t.Fatal("unapproved payload", string(body))
	}
	update.Kind = "PR"
	if err := publisher.Check(t.Context(), update); err == nil {
		t.Fatal("Issue mistaken for PR")
	}
}

func TestPublicationCLIConflictingMarkersAndUnknownReplies(t *testing.T) {
	for _, failure := range []string{"author", "duplicate", "content", "missing-id", "malformed", "permission", "account"} {
		t.Run(failure, func(t *testing.T) {
			update := forgePublication()
			comment := map[string]any{"id": 99, "body": update.Body + "\n\n" + PublicationMarker(update), "user": map[string]string{"login": "owner"}}
			comments := []map[string]any{comment}
			user, repository := `{"login":"owner"}`, `{"full_name":"owner/repo","permissions":{"push":true}}`
			switch failure {
			case "author":
				comment["user"] = map[string]string{"login": "other"}
			case "duplicate":
				comments = append(comments, comment)
			case "content":
				comment["body"] = "modified " + PublicationMarker(update)
			case "missing-id":
				comment["id"] = 0
			case "permission":
				repository = `{"full_name":"owner/repo","permissions":{"push":false}}`
			case "account":
				user = `{"login":"other"}`
			}

			data, _ := json.Marshal(comments)
			if failure == "malformed" {
				data = []byte("private-provider-output")
			}
			publicationCLI(t, map[string]string{
				"GET:user": user, "GET:repos/owner/repo": repository,
				"GET:repos/owner/repo/issues/53":                              `{"number":53}`,
				"GET:repos/owner/repo/issues/53/comments?per_page=100&page=1": string(data),
				"POST:repos/owner/repo/issues/53/comments":                    `{}`,
			})
			var err error
			if failure == "permission" || failure == "account" {
				err = (Publisher{}).Check(t.Context(), update)
			} else {
				_, _, err = (Publisher{}).Reconcile(t.Context(), update)
			}
			if err == nil || strings.Contains(err.Error(), "private-provider-output") {
				t.Fatal("unconfirmed provider evidence", err)
			}
			if _, err := (Publisher{}).Send(t.Context(), update); err == nil {
				t.Fatal("missing delivery ID accepted")
			}
		})
	}
}

func TestGitHubClosedIssueReconciliationEvidence(t *testing.T) {
	update := forgePublication()
	update.Action, update.Body, update.FlowID, update.At = "CLOSE", "", "completed-flow", time.Now().UTC().Add(-time.Minute)
	for _, test := range []struct {
		name, state, reason, actor string
		closedAt                   time.Time
		found, denied              bool
	}{
		{"open", "open", "", "", time.Time{}, false, false},
		{"approved-actor", "closed", "completed", "OWNER", update.At.Add(time.Second), true, false},
		{"different-actor", "closed", "completed", "other", update.At.Add(time.Second), false, true},
		{"before-approval", "closed", "completed", "owner", update.At.Add(-time.Second), false, true},
		{"missing-time", "closed", "completed", "owner", time.Time{}, false, true},
		{"future-time", "closed", "completed", "owner", time.Now().Add(time.Hour), false, true},
		{"different-disposition", "closed", "not_planned", "owner", update.At.Add(time.Second), false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{
				"number": update.Number, "state": test.state, "state_reason": test.reason,
				"closed_at": test.closedAt, "closed_by": map[string]string{"login": test.actor},
			})
			if err != nil {
				t.Fatal(err)
			}
			request := publicationCLI(t, map[string]string{
				"GET:repos/owner/repo/issues/53":   string(body),
				"PATCH:repos/owner/repo/issues/53": string(body),
			})
			id, found, err := (Publisher{}).Reconcile(t.Context(), update)
			if (err != nil) != test.denied || found != test.found || !test.denied && id != "53" {
				t.Fatal(id, found, err)
			}
			if _, err := os.Stat(request); !os.IsNotExist(err) {
				t.Fatal("reconciliation wrote to the remote", err)
			}
			id, err = (Publisher{}).Send(t.Context(), update)
			if (err == nil) != test.found || test.found && id != "53" {
				t.Fatal("unconfirmed close response", id, err)
			}
		})
	}
}
