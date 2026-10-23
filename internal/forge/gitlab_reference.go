package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/publictext"
)

func resolveGitLabReference(ctx context.Context, host, repository, target string, env []string) (publictext.Reference, error) {
	parsed, err := url.Parse(target)
	prefix := "/" + repository + "/-/"
	if err != nil || parsed.Scheme != "https" || parsed.Host != host || parsed.User != nil ||
		parsed.RawQuery != "" || !strings.HasPrefix(parsed.Path, prefix) {
		return publictext.Reference{}, fmt.Errorf("unsupported GitLab reference")
	}
	parts := strings.Split(strings.TrimPrefix(parsed.Path, prefix), "/")
	if len(parts) != 2 || !regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(parts[1]) {
		return publictext.Reference{}, fmt.Errorf("unsupported GitLab reference path")
	}
	base := gitlabProjectEndpoint(repository)
	endpoint, kind := "", parts[0]
	switch kind {
	case "issues", "merge_requests", "jobs", "pipelines":
		if !regexp.MustCompile(`^[1-9][0-9]*$`).MatchString(parts[1]) {
			return publictext.Reference{}, fmt.Errorf("GitLab reference requires an exact numeric ID")
		}
		endpoint = base + "/" + kind + "/" + parts[1]
	case "releases":
		endpoint = base + "/releases/" + url.PathEscape(parts[1])
	default:
		return publictext.Reference{}, fmt.Errorf("GitLab reference type is unsupported")
	}
	if parsed.Fragment != "" {
		if kind != "issues" && kind != "merge_requests" ||
			!regexp.MustCompile(`^note_[1-9][0-9]*$`).MatchString(parsed.Fragment) {
			return publictext.Reference{}, fmt.Errorf("GitLab reference anchor is unsupported")
		}
		// Check the parent too: a note ID alone does not prove its project/object.
		parent, err := gitlabAPI(ctx, host, env, "GET", endpoint, nil)
		if err != nil {
			return publictext.Reference{}, err
		}
		if _, err := gitlabReferenceMetadata(strings.Split(target, "#")[0], kind, parent); err != nil {
			return publictext.Reference{}, err
		}
		endpoint += "/notes/" + strings.TrimPrefix(parsed.Fragment, "note_")
		kind = "note"
	}
	data, err := gitlabAPI(ctx, host, env, "GET", endpoint, nil)
	if err != nil {
		return publictext.Reference{}, err
	}
	return gitlabReferenceMetadata(target, kind, data)
}

func gitlabReferenceMetadata(target, kind string, data []byte) (publictext.Reference, error) {
	var value struct {
		ID          int64     `json:"id"`
		WebURL      string    `json:"web_url"`
		CreatedAt   time.Time `json:"created_at"`
		UpdatedAt   time.Time `json:"updated_at"`
		ClosedAt    time.Time `json:"closed_at"`
		MergedAt    time.Time `json:"merged_at"`
		FinishedAt  time.Time `json:"finished_at"`
		ReleasedAt  time.Time `json:"released_at"`
		State       string    `json:"state"`
		Status      string    `json:"status"`
		NoteableIID int       `json:"noteable_iid"`
		Links       struct {
			Self string `json:"self"`
		} `json:"_links"`
	}
	if json.Unmarshal(data, &value) != nil {
		return publictext.Reference{}, fmt.Errorf("invalid GitLab reference metadata")
	}
	if kind == "note" {
		parsed, _ := url.Parse(target)
		if value.ID <= 0 || parsed.Fragment != fmt.Sprintf("note_%d", value.ID) ||
			!strings.HasSuffix(parsed.Path, fmt.Sprintf("/%d", value.NoteableIID)) {
			return publictext.Reference{}, fmt.Errorf("GitLab note identity differs")
		}
	} else if value.WebURL != target && (kind != "releases" || value.Links.Self != target) {
		return publictext.Reference{}, fmt.Errorf("GitLab reference resolves to a different object")
	}
	result := publictext.Reference{URL: target, CreatedAt: value.CreatedAt, StateAt: value.UpdatedAt, State: "EXISTS"}
	if result.StateAt.IsZero() {
		result.StateAt = value.CreatedAt
	}
	switch kind {
	case "issues":
		if value.State == "closed" {
			result.State, result.StateAt = "CLOSED", value.ClosedAt
		}
	case "merge_requests":
		result.State = "IN_PROGRESS"
		if value.State == "merged" {
			result.State, result.StateAt = "COMPLETED", value.MergedAt
		} else if value.State == "closed" {
			result.State, result.StateAt = "FAILED", value.ClosedAt
		}
	case "jobs", "pipelines":
		result.State = "IN_PROGRESS"
		if value.Status == "success" {
			result.State, result.StateAt = "COMPLETED", value.FinishedAt
		} else if value.Status == "failed" || value.Status == "canceled" || value.Status == "skipped" {
			result.State, result.StateAt = "FAILED", value.FinishedAt
		}
	case "releases":
		result.State, result.StateAt = "PUBLISHED", value.ReleasedAt
		if value.ReleasedAt.After(time.Now().UTC()) {
			result.State = "NOT_PUBLISHED"
		}
	case "note":
	default:
		return publictext.Reference{}, fmt.Errorf("unsupported GitLab metadata kind")
	}
	if result.CreatedAt.IsZero() || result.StateAt.IsZero() {
		return publictext.Reference{}, fmt.Errorf("GitLab reference lacks authoritative timestamps")
	}
	return result, nil
}
