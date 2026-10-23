package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/publictext"
)

// Only GitHub object metadata is read. No arbitrary URL is fetched and no
// caller-provided executable, API host or shell expression is evaluated.
func resolveGitHub(ctx context.Context, target string) (publictext.Reference, error) {
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "github.com" ||
		parsed.User != nil || parsed.RawQuery != "" {
		return publictext.Reference{}, fmt.Errorf("unsupported public reference URL")
	}
	path := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(path) < 4 || !regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(path[0]) ||
		!regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(path[1]) {
		return publictext.Reference{}, fmt.Errorf("unsupported public reference path")
	}
	base := "repos/" + path[0] + "/" + path[1]
	endpoint := ""
	kind := ""
	number := regexp.MustCompile(`^[1-9][0-9]*$`)
	switch {
	case len(path) == 4 && (path[2] == "issues" || path[2] == "pull") && number.MatchString(path[3]):
		kind = path[2]
		if parsed.Fragment != "" {
			switch {
			case regexp.MustCompile(`^issuecomment-[1-9][0-9]*$`).MatchString(parsed.Fragment):
				endpoint, kind = base+"/issues/comments/"+strings.TrimPrefix(parsed.Fragment, "issuecomment-"), "comment"
			case path[2] == "pull" && regexp.MustCompile(`^pullrequestreview-[1-9][0-9]*$`).MatchString(parsed.Fragment):
				endpoint, kind = base+"/pulls/"+path[3]+"/reviews/"+strings.TrimPrefix(parsed.Fragment, "pullrequestreview-"), "review"
			default:
				return publictext.Reference{}, fmt.Errorf("unsupported public reference anchor")
			}
		} else {
			apiKind := "issues"
			if kind == "pull" {
				apiKind = "pulls"
			}
			endpoint = base + "/" + apiKind + "/" + path[3]
		}
	case len(path) == 5 && path[2] == "actions" && path[3] == "runs" && number.MatchString(path[4]) && parsed.Fragment == "":
		endpoint, kind = base+"/actions/runs/"+path[4], "run"
	case len(path) == 5 && path[2] == "releases" && path[3] == "tag" && parsed.Fragment == "" && regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(path[4]):
		endpoint, kind = base+"/releases/tags/"+path[4], "release"
	case len(path) == 6 && path[2] == "releases" && path[3] == "download" && parsed.Fragment == "" &&
		regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(path[4]) && regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(path[5]):
		endpoint, kind = base+"/releases/tags/"+path[4], "artifact"
	default:
		return publictext.Reference{}, fmt.Errorf("reference type is not supported; verify it separately and do not claim coverage")
	}
	data, err := exec.CommandContext(ctx, "gh", "api", "--hostname", "github.com", "--method", "GET", endpoint).Output()
	if err != nil {
		return publictext.Reference{}, fmt.Errorf("GitHub reference metadata is unavailable: %w", err)
	}
	return referenceMetadata(target, kind, data)
}

func referenceMetadata(target, kind string, data []byte) (publictext.Reference, error) {
	var value struct {
		URL         string    `json:"html_url"`
		CreatedAt   time.Time `json:"created_at"`
		UpdatedAt   time.Time `json:"updated_at"`
		ClosedAt    time.Time `json:"closed_at"`
		MergedAt    time.Time `json:"merged_at"`
		SubmittedAt time.Time `json:"submitted_at"`
		PublishedAt time.Time `json:"published_at"`
		State       string    `json:"state"`
		StateReason string    `json:"state_reason"`
		Status      string    `json:"status"`
		Conclusion  string    `json:"conclusion"`
		Draft       bool      `json:"draft"`
		Body        string    `json:"body"`
		Assets      []struct {
			URL       string    `json:"browser_download_url"`
			CreatedAt time.Time `json:"created_at"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return publictext.Reference{}, fmt.Errorf("invalid reference metadata: %w", err)
	}
	if value.URL != target && kind != "artifact" {
		return publictext.Reference{}, fmt.Errorf("reference resolves to a different GitHub object")
	}
	result := publictext.Reference{URL: target, CreatedAt: value.CreatedAt, StateAt: value.CreatedAt, State: "EXISTS"}
	switch kind {
	case "comment":
		if !value.UpdatedAt.IsZero() {
			result.StateAt = value.UpdatedAt
		}
	case "issues":
		if value.State == "closed" {
			result.State, result.StateAt = "CLOSED", value.ClosedAt
			if value.StateReason == "completed" {
				result.State = "COMPLETED"
			}
		}
	case "pull":
		result.State = "IN_PROGRESS"
		if !value.MergedAt.IsZero() {
			result.State, result.StateAt = "COMPLETED", value.MergedAt
		} else if value.State == "closed" {
			result.State, result.StateAt = "FAILED", value.ClosedAt
		}
	case "review":
		result.CreatedAt, result.StateAt = value.SubmittedAt, value.SubmittedAt
		result.State = value.State
	case "run":
		result.State = "IN_PROGRESS"
		if value.Status == "completed" {
			result.State, result.StateAt = "FAILED", value.UpdatedAt
			if value.Conclusion == "success" {
				result.State = "COMPLETED"
			}
		}
	case "release", "artifact":
		result.State = "NOT_PUBLISHED"
		if !value.Draft && !value.PublishedAt.IsZero() {
			result.State, result.StateAt = "PUBLISHED", value.PublishedAt
		}
		if kind == "artifact" {
			found := false
			for _, asset := range value.Assets {
				if asset.URL == target {
					result.CreatedAt = asset.CreatedAt
					if result.StateAt.Before(asset.CreatedAt) {
						result.StateAt = asset.CreatedAt
					}
					found = true
				}
			}
			if !found {
				return publictext.Reference{}, fmt.Errorf("artifact has not been published at this URL")
			}
		}
	default:
		return publictext.Reference{}, fmt.Errorf("unsupported metadata kind")
	}
	if result.CreatedAt.IsZero() || result.StateAt.IsZero() {
		return publictext.Reference{}, fmt.Errorf("reference metadata has no authoritative state timestamp")
	}
	return result, nil
}
