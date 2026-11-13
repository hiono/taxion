package gitproof

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
)

func VerifyRepositoryRemote(dispatch core.GraphDispatch, remote string) error {
	if dispatch.Configuration != nil {
		var selected struct {
			Repository struct {
				Host string `json:"host"`
				Path string `json:"path"`
			} `json:"repository"`
			Endpoints *config.Endpoints `json:"endpoints"`
		}
		if json.Unmarshal(dispatch.Configuration.Selection, &selected) == nil &&
			selected.Endpoints != nil && !selected.Endpoints.Empty() {
			if selected.Repository.Path != "" && selected.Repository.Path != dispatch.Repository {
				return fmt.Errorf("REP-004 Git repository configuration differs from the approved path")
			}
			if err := VerifyEndpointsRemote(*selected.Endpoints, dispatch.Repository, remote); err != nil {
				return err
			}
			return nil
		}
	}
	if !strings.EqualFold(repositoryIdentity(remote), dispatch.Repository) {
		return fmt.Errorf("Git repository path differs")
	}
	if dispatch.Configuration == nil {
		return nil
	}
	var selected struct {
		Repository struct {
			Host string `json:"host"`
			Path string `json:"path"`
		} `json:"repository"`
	}
	if json.Unmarshal(dispatch.Configuration.Selection, &selected) != nil || selected.Repository.Host == "" ||
		!strings.EqualFold(selected.Repository.Path, dispatch.Repository) {
		return fmt.Errorf("Git repository configuration is invalid")
	}
	host := ""
	if parsed, err := url.Parse(remote); err == nil && parsed.Host != "" {
		host = parsed.Host
	} else if colon := strings.IndexByte(remote, ':'); colon >= 0 {
		host = remote[:colon]
		if at := strings.LastIndexByte(host, '@'); at >= 0 {
			host = host[at+1:]
		}
	}
	if !strings.EqualFold(host, selected.Repository.Host) {
		return fmt.Errorf("Git origin host differs from approved host")
	}
	return nil
}
