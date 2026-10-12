package gitproof

import (
	"encoding/json"
	"testing"

	"github.com/hiono/taxion/internal/core"
)

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
