package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"github.com/hiono/taxion/internal/publictext"
)

type gitlabAuditEntry struct {
	ID, IID                        int
	Title, Name, Description, Body string
	TagName                        string `json:"tag_name"`
}

// InventoryGitLabText has no write path. Unknown project settings stop all reads.
func InventoryGitLabText(ctx context.Context, host, repository, credential string) ([]publictext.AuditRecord, error) {
	if _, err := publictext.LoadProfile("gitlab", host, repository); err != nil {
		return nil, err
	}
	env, err := gitlabEnvironment(credential)
	if err != nil {
		return nil, err
	}
	base := gitlabProjectEndpoint(repository)
	var records []publictext.AuditRecord
	read := func(endpoint, surface string) ([]gitlabAuditEntry, error) {
		var all []gitlabAuditEntry
		for page := 1; page <= 1000; page++ {
			data, err := gitlabAPI(ctx, host, env, "GET", endpoint+"?per_page=100&page="+strconv.Itoa(page), nil)
			if err != nil {
				return nil, err
			}
			var values []gitlabAuditEntry
			if err := json.Unmarshal(data, &values); err != nil || values == nil {
				return nil, fmt.Errorf("read-only audit received invalid listing")
			}
			all = append(all, values...)
			for _, value := range values {
				id := strconv.Itoa(value.ID)
				if surface == "release" {
					id = url.PathEscape(value.TagName)
				}
				if id == "" || id == "0" {
					return nil, fmt.Errorf("read-only audit requires a stable object identifier")
				}
				title := value.Title
				if title == "" {
					title = value.Name
				}
				body := value.Description
				if body == "" {
					body = value.Body
				}
				records = append(records, publictext.AuditRecord{ID: endpoint + "/" + id, Surface: surface, Title: title, Body: body})
			}
			if len(values) < 100 {
				return all, nil
			}
		}
		return nil, fmt.Errorf("read-only audit pagination limit reached; inventory incomplete")
	}
	for _, kind := range []string{"issues", "merge_requests", "milestones", "labels"} {
		surface := map[string]string{"issues": "issue", "merge_requests": "pull-request", "milestones": "milestone", "labels": "label"}[kind]
		values, err := read(base+"/"+kind, surface)
		if err != nil {
			return nil, err
		}
		if kind == "issues" || kind == "merge_requests" {
			for _, value := range values {
				if value.IID <= 0 {
					return nil, fmt.Errorf("read-only audit requires exact object IID")
				}
				if _, err := read(base+"/"+kind+"/"+strconv.Itoa(value.IID)+"/notes", "comment"); err != nil {
					return nil, err
				}
			}
		}
	}
	if _, err := read(base+"/releases", "release"); err != nil {
		return nil, err
	}
	return records, nil
}
