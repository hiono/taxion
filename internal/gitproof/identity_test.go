package gitproof

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
)

func TestRepositoryIdentityPreservesFullNamespace(t *testing.T) {
	for _, repo := range []string{"owner/repo", "group/subgroup/project", strings.Repeat("group/", 21) + "project"} {
		selected, err := json.Marshal(map[string]any{"repository": map[string]string{
			"host": "gitlab.example.com", "path": repo,
		}})
		if err != nil {
			t.Fatal(err)
		}
		dispatch := core.GraphDispatch{Repository: repo, Configuration: &core.DispatchConfiguration{Selection: selected}}
		for _, remote := range []string{"https://gitlab.example.com/" + repo + ".git",
			"https://gitlab.example.com/" + repo,
			"git@gitlab.example.com:" + repo + ".git", "ssh://git@gitlab.example.com/" + repo + ".git"} {
			if RepositoryIdentity(remote) != repo {
				t.Fatal("truncated repository namespace", remote, RepositoryIdentity(remote))
			}
			if err := VerifyRepositoryRemote(dispatch, remote); err != nil {
				t.Fatal(err)
			}
			if strings.Count(repo, "/") > 1 {
				parts := strings.Split(repo, "/")
				shortened := dispatch
				shortened.Repository = strings.Join(parts[len(parts)-2:], "/")
				shortened.Configuration = nil
				if err := VerifyRepositoryRemote(shortened, remote); err == nil {
					t.Fatal("shortened repository accepted the full remote")
				}
			}
		}
		if err := VerifyRepositoryRemote(dispatch, "https://gitlab.example.com/another/"+repo+".git"); err == nil {
			t.Fatal("accepted different parent namespace")
		}
	}
}

func TestApprovedHostSeparatesSameRepositoryPaths(t *testing.T) {
	dispatch := core.GraphDispatch{Repository: "owner/repo", Configuration: &core.DispatchConfiguration{
		Selection: json.RawMessage(`{"repository":{"host":"github.com","path":"owner/repo"}}`),
	}}
	for _, remote := range []string{"https://github.com/owner/repo.git", "git@github.com:owner/repo.git"} {
		if err := VerifyRepositoryRemote(dispatch, remote); err != nil {
			t.Fatal(err)
		}
	}
	for _, remote := range []string{"https://gitlab.example.com/owner/repo.git", "git@other.example.com:owner/repo.git", "https://github.com/owner/other.git"} {
		if err := VerifyRepositoryRemote(dispatch, remote); err == nil {
			t.Fatal("different target accepted", remote)
		}
	}
}
