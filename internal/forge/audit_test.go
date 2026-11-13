package forge

import (
	"os"
	"strings"
	"testing"
)

func TestGitLabInventoryReadsEverySupportedCollectionWithoutMutation(t *testing.T) {
	base := "projects/group%2Fsubgroup%2Frepo"
	request := gitlabPublicationCLI(t, map[string]string{
		"GET:" + base + "/issues?per_page=100&page=1":                 `[{"id":101,"iid":21,"title":"Preserve setup choices","description":"Invalid confirmation preserves choices."}]`,
		"GET:" + base + "/issues/21/notes?per_page=100&page=1":        `[{"id":102,"body":"Existing issue note."}]`,
		"GET:" + base + "/merge_requests?per_page=100&page=1":         `[{"id":103,"iid":7,"title":"Preserve setup choices","description":"Use existing configuration."}]`,
		"GET:" + base + "/merge_requests/7/notes?per_page=100&page=1": `[{"id":104,"body":"Existing review summary."}]`,
		"GET:" + base + "/milestones?per_page=100&page=1":             `[{"id":105,"title":"Stable configuration","description":"Existing scope."}]`,
		"GET:" + base + "/labels?per_page=100&page=1":                 `[{"id":106,"name":"configuration","description":"Configuration changes."}]`,
		"GET:" + base + "/releases?per_page=100&page=1":               `[{"tag_name":"v1.0.0","name":"Application 1.0.0","description":"Existing release notes."}]`,
	})
	records, err := InventoryGitLabText(t.Context(), "gitlab.example", "group/subgroup/repo", "env:TAXION_PUBLICATION_TEST_GITLAB_TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 7 || records[6].Surface != "release" || !strings.HasSuffix(records[6].ID, "/v1.0.0") {
		t.Fatal("incomplete or unstable inventory", records)
	}
	if _, err := os.Stat(request); !os.IsNotExist(err) {
		t.Fatal("read-only inventory attempted mutation", err)
	}
	t.Setenv("TAXION_COMMUNICATION_PROFILES", "")
	if _, err := InventoryGitLabText(t.Context(), "gitlab.example", "group/subgroup/repo", "env:TAXION_PUBLICATION_TEST_GITLAB_TOKEN"); err == nil {
		t.Fatal("unconfigured project read")
	}
}
