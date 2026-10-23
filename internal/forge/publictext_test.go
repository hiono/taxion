package forge

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/publictext"
)

func TestAdaptersCannotBypassPublicTextGate(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		t.Run(provider, func(t *testing.T) {
			update := forgePublication()
			var request string
			if provider == "gitlab" {
				update = gitlabPublication()
				request = gitlabPublicationCLI(t, nil)
			} else {
				request = publicationCLI(t, nil)
			}
			for _, body := range []string{
				"operation owner: controller",
				"The completed result is recorded in a later comment",
				"The unpublished release is available below",
				"CI has passed when the run is still pending",
				"private-example-47",
				"password=" + strings.Repeat("x", 25),
			} {
				update.Body = body
				if _, err := (Publisher{}).Send(t.Context(), update); err == nil {
					t.Fatal("direct adapter send bypassed public-text gate")
				}
				if _, err := os.Stat(request); !os.IsNotExist(err) {
					t.Fatal("rejected adapter invocation launched a write", err)
				}
			}
			update.Body = "Setup choices remain unchanged."
			t.Setenv("TAXION_COMMUNICATION_PROFILES", "")
			if _, err := (Publisher{}).Send(t.Context(), update); err == nil {
				t.Fatal("missing profile allowed a direct send")
			}
			for _, method := range []string{"POST", "PATCH", "PUT", "DELETE"} {
				if _, err := publicationAPI(t.Context(), nil, method, "repos/owner/repo/releases", map[string]string{"body": "text"}); err == nil {
					t.Fatal("raw GitHub API write bypassed gate")
				}
				if _, err := gitlabAPI(t.Context(), "gitlab.example", nil, method, "projects/1/wikis", map[string]string{"content": "text"}); err == nil {
					t.Fatal("raw GitLab API write bypassed gate")
				}
			}
		})
	}
}

func TestPublicWritePermitBindsExactTransport(t *testing.T) {
	update := forgePublication()
	publicationProfiles(t)
	payload := map[string]string{"body": update.Body + "\n\n" + PublicationMarker(update)}
	ctx, err := publicWriteContext(t.Context(), update, "github.com", "POST", "repos/owner/repo/issues/53/comments", payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkWritePermit(ctx, "github.com", "POST", "repos/owner/repo/issues/53/comments", payload); err != nil {
		t.Fatal(err)
	}
	if _, err := publicWriteContext(t.Context(), update, "github.com", "POST", "repos/owner/repo/issues/53/comments",
		map[string]string{"body": "unchecked content"}, nil); err == nil {
		t.Fatal("unchecked payload obtained a transport permit")
	}
	for _, test := range []struct {
		host, method, endpoint string
		payload                map[string]string
	}{
		{"different.example", "POST", "repos/owner/repo/issues/53/comments", payload},
		{"github.com", "PATCH", "repos/owner/repo/issues/53/comments", payload},
		{"github.com", "POST", "repos/other/repo/issues/53/comments", payload},
		{"github.com", "POST", "repos/owner/repo/issues/53/comments", map[string]string{"body": "changed"}},
	} {
		if err := checkWritePermit(ctx, test.host, test.method, test.endpoint, test.payload); err == nil {
			t.Fatal("permit reused for different transport")
		}
	}
}

func TestDirectAdapterRequiresObservedReferenceState(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		t.Run(provider, func(t *testing.T) {
			update := forgePublication()
			target := "https://github.com/owner/repo/actions/runs/42"
			endpoint := "GET:repos/owner/repo/actions/runs/42"
			object := map[string]any{"html_url": target, "created_at": time.Now().Add(-time.Hour),
				"updated_at": time.Now().Add(-time.Minute), "status": "in_progress"}
			if provider == "gitlab" {
				update = gitlabPublication()
				target = "https://gitlab.example/group/subgroup/repo/-/pipelines/42"
				endpoint = "GET:projects/group%2Fsubgroup%2Frepo/pipelines/42"
				object = map[string]any{"web_url": target, "created_at": time.Now().Add(-time.Hour),
					"updated_at": time.Now().Add(-time.Minute), "status": "running"}
			}
			data, err := json.Marshal(object)
			if err != nil {
				t.Fatal(err)
			}
			var request string
			if provider == "gitlab" {
				request = gitlabPublicationCLI(t, map[string]string{endpoint: string(data)})
			} else {
				request = publicationCLI(t, map[string]string{endpoint: string(data)})
			}
			update.Body = "CI has completed: " + target
			update.Statement.Claims = []publictext.ReferenceClaim{{URL: target, State: "COMPLETED"}}
			if _, err := (Publisher{}).Send(t.Context(), update); err == nil {
				t.Fatal("pending CI presented as passed")
			}
			if _, err := os.Stat(request); !os.IsNotExist(err) {
				t.Fatal("unverified reference caused remote write")
			}
		})
	}
}

func TestGitLabReferenceStatesAndIdentity(t *testing.T) {
	at := time.Now().UTC().Add(-time.Hour)
	for _, test := range []struct{ kind, state, want string }{
		{"merge_requests", "merged", "COMPLETED"},
		{"merge_requests", "opened", "IN_PROGRESS"},
		{"jobs", "success", "COMPLETED"},
		{"pipelines", "running", "IN_PROGRESS"},
		{"pipelines", "failed", "FAILED"},
		{"releases", "", "PUBLISHED"},
	} {
		target := "https://gitlab.example/group/subgroup/repo/-/" + test.kind + "/42"
		object := map[string]any{"web_url": target, "created_at": at, "updated_at": at,
			"merged_at": at, "finished_at": at, "released_at": at, "state": test.state, "status": test.state}
		data, _ := json.Marshal(object)
		got, err := gitlabReferenceMetadata(target, test.kind, data)
		if err != nil || got.State != test.want {
			t.Fatal(test, got, err)
		}
		if _, err := gitlabReferenceMetadata(target+"9", test.kind, data); err == nil {
			t.Fatal("different GitLab object accepted")
		}
	}
	if _, err := resolveGitLabReference(context.Background(), "gitlab.example", "group/subgroup/repo",
		"https://other.example/group/subgroup/repo/-/issues/1", nil); err == nil {
		t.Fatal("reference escaped configured host")
	}
}
