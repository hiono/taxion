package gitproof

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
)

func binding() config.Endpoints {
	return config.Endpoints{
		APIBase:        "https://gitlab.example.com/git/api/v4",
		WebBase:        "https://gitlab.example.com/git",
		HTTPSCloneBase: "https://gitlab.example.com/git",
		SSHHost:        "gitlab.example.com",
		SSHPort:        "2222",
		ProjectID:      "191",
	}
}

func TestParseRemoteSeparatesIdentities(t *testing.T) {
	for name, want := range map[string]Remote{
		"https://gitlab.example.com/git/group/subgroup/application.git": {
			Scheme: "https", Host: "gitlab.example.com", Path: "git/group/subgroup/application"},
		"https://gitlab.example.com:8443/group/application": {
			Scheme: "https", Host: "gitlab.example.com", Port: "8443", Path: "group/application"},
		"ssh://git@gitlab.example.com:2222/group/subgroup/application.git": {
			Scheme: "ssh", Host: "gitlab.example.com", Port: "2222", Path: "group/subgroup/application"},
		"git@gitlab.example.com:group/application.git": {
			Scheme: "scp", Host: "gitlab.example.com", Path: "group/application"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ParseRemote(name)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatal("remote identities were not separated", got)
			}
		})
	}
}

func TestParseRemoteRefusesAmbiguousAndUnsafeAddresses(t *testing.T) {
	for name, remote := range map[string]string{
		"scp-like port":      "git@gitlab.example.com:2222/group/application.git",
		"encoded separator":  "https://gitlab.example.com/git%2Fother/group/application.git",
		"dot segment":        "https://gitlab.example.com/group/../application.git",
		"backslash":          "https://gitlab.example.com/group\\application.git",
		"credentials":        "https://user:secret@gitlab.example.com/group/application.git",
		"query":              "https://gitlab.example.com/group/application.git?ref=main",
		"fragment":           "https://gitlab.example.com/group/application.git#top",
		"unsupported scheme": "http://gitlab.example.com/group/application.git",
		"local path":         "file:///private/application.git",
		"namespace only":     "https://gitlab.example.com/application.git",
		"empty":              "",
		"whitespace":         "https://gitlab.example.com/group/application .git",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseRemote(remote); err == nil {
				t.Fatal("unsafe remote accepted")
			} else if !strings.Contains(err.Error(), "REP-003") {
				t.Fatal("unsafe remote lacks its diagnostic code: " + err.Error())
			}
		})
	}
}

func TestVerifyEndpointsRemoteBindsPrefixedCloneAndSSHPort(t *testing.T) {
	endpoints := binding()
	for _, remote := range []string{
		"https://gitlab.example.com/git/group/subgroup/application.git",
		"ssh://git@gitlab.example.com:2222/group/subgroup/application.git",
	} {
		if err := VerifyEndpointsRemote(endpoints, "group/subgroup/application", remote); err != nil {
			t.Fatal(err)
		}
	}
}

func TestVerifyEndpointsRemoteSeparatesFailingStages(t *testing.T) {
	endpoints := binding()
	repository := "group/subgroup/application"
	for name, want := range map[string]struct {
		remote string
		code   string
	}{
		"other project":        {"https://gitlab.example.com/git/group/subgroup/other.git", "REP-004"},
		"case only path":       {"https://gitlab.example.com/git/group/SubGroup/application.git", "REP-004"},
		"other parent path":    {"https://gitlab.example.com/git/group/application.git", "REP-004"},
		"wrong clone prefix":   {"https://gitlab.example.com/github/group/subgroup/application.git", "REP-005"},
		"doubled clone prefix": {"https://gitlab.example.com/git/git/group/subgroup/application.git", "REP-004"},
		"other host":           {"https://other.example.com/git/group/subgroup/application.git", "REP-005"},
		"other HTTPS port":     {"https://gitlab.example.com:8443/git/group/subgroup/application.git", "REP-005"},
		"missing SSH port":     {"ssh://git@gitlab.example.com/group/subgroup/application.git", "REP-005"},
		"other SSH port":       {"ssh://git@gitlab.example.com:2223/group/subgroup/application.git", "REP-005"},
		"scp-like port":        {"git@gitlab.example.com:group/subgroup/application.git", "REP-005"},
	} {
		t.Run(name, func(t *testing.T) {
			err := VerifyEndpointsRemote(endpoints, repository, want.remote)
			if err == nil || !strings.Contains(err.Error(), want.code) {
				t.Fatal("expected", want.code, "got", err)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("diagnostics exposed credentials")
			}
		})
	}
}

func TestVerifyEndpointsRemoteAcceptsSCPLikeOnTheDefaultPort(t *testing.T) {
	endpoints := binding()
	endpoints.SSHPort = "22"
	for _, remote := range []string{
		"git@gitlab.example.com:group/application.git",
		"ssh://git@gitlab.example.com/group/application.git",
	} {
		if err := VerifyEndpointsRemote(endpoints, "group/application", remote); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDispatchSelectionCarriesEndpoints(t *testing.T) {
	selection, err := json.Marshal(map[string]any{
		"repository": map[string]string{"host": "gitlab.example.com", "path": "group/subgroup/application"},
		"endpoints": map[string]string{
			"apiBase": "https://gitlab.example.com/git/api/v4", "webBase": "https://gitlab.example.com/git",
			"httpsCloneBase": "https://gitlab.example.com/git", "sshHost": "gitlab.example.com",
			"sshPort": "2222", "projectId": "191",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatch := core.GraphDispatch{Repository: "group/subgroup/application",
		Configuration: &core.DispatchConfiguration{Selection: selection}}
	if err := VerifyRepositoryRemote(dispatch, "https://gitlab.example.com/git/group/subgroup/application.git"); err != nil {
		t.Fatal(err)
	}
	if declared := EndpointsFromSelection(dispatch.Configuration); declared == nil || declared.ProjectID != "191" {
		t.Fatal("endpoints were not read from the selection")
	}
	dispatch.Repository = "group/subgroup/other"
	if err := VerifyRepositoryRemote(dispatch, "https://gitlab.example.com/git/group/subgroup/application.git"); err == nil ||
		!strings.Contains(err.Error(), "REP-004") {
		t.Fatal("dispatch repository mismatch was not reported as REP-004", err)
	}
}

func TestLegacyVerificationStaysUnchangedWithoutEndpoints(t *testing.T) {
	dispatch := core.GraphDispatch{Repository: "group/subgroup/application", Configuration: &core.DispatchConfiguration{
		Selection: json.RawMessage(`{"repository":{"host":"gitlab.example.com","path":"group/subgroup/application"}}`),
	}}
	if err := VerifyRepositoryRemote(dispatch, "https://gitlab.example.com/group/subgroup/application.git"); err != nil {
		t.Fatal("legacy binding regressed", err)
	}
	if err := VerifyRepositoryRemote(dispatch, "ssh://git@gitlab.example.com:2222/group/subgroup/application.git"); err == nil {
		t.Fatal("legacy binding accepted an explicit port it cannot verify")
	}
	if declared := EndpointsFromSelection(dispatch.Configuration); declared != nil {
		t.Fatal("legacy selection produced endpoints")
	}
}
