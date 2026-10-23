package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/store"
)

func checkGitLabPublication(ctx context.Context, update store.ExternalUpdate) error {
	if !validGitLabHost(update.Host) || update.Repository == "" || update.Account == "" {
		return fmt.Errorf("taxion: GitLab publication requires an exact host, project and account")
	}
	if update.Action == "CREATE" {
		if update.Kind != "ISSUE" {
			return fmt.Errorf("taxion: GitLab can create Issues only")
		}
	} else if update.Action == "COMMENT" {
		if update.Number <= 0 || update.Kind != "ISSUE" && update.Kind != "PR" {
			return fmt.Errorf("taxion: GitLab comment requires an Issue or merge request")
		}
	} else if update.Action == "CLOSE" {
		if update.Number <= 0 || update.Kind != "ISSUE" {
			return fmt.Errorf("taxion: GitLab close requires an Issue")
		}
	} else {
		return fmt.Errorf("taxion: unsupported GitLab publication action")
	}
	env, err := gitlabEnvironment(update.CredentialRef)
	if err != nil {
		return err
	}
	query := func(endpoint string) ([]byte, error) {
		return gitlabAPI(ctx, update.Host, env, "GET", endpoint, nil)
	}
	if err := checkGitLabTarget(update.Repository, update.Account, true, query); err != nil {
		return err
	}
	if update.Action == "CREATE" {
		return nil
	}
	endpoint := gitlabObjectEndpoint(update)
	data, err := gitlabAPI(ctx, update.Host, env, "GET", endpoint, nil)
	if err != nil {
		return err
	}
	var target struct {
		IID   int    `json:"iid"`
		State string `json:"state"`
	}
	if json.Unmarshal(data, &target) != nil || target.IID != update.Number {
		return fmt.Errorf("taxion: GitLab external object kind or ID differs")
	}
	return nil
}

func reconcileGitLabPublication(ctx context.Context, update store.ExternalUpdate) (string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	env, err := gitlabEnvironment(update.CredentialRef)
	if err != nil {
		return "", false, err
	}
	base := gitlabProjectEndpoint(update.Repository)
	switch update.Action {
	case "CREATE":
		matched := ""
		for page := 1; ; page++ {
			endpoint := base + "/issues?state=all&per_page=100&page=" + strconv.Itoa(page)
			data, err := gitlabAPI(ctx, update.Host, env, "GET", endpoint, nil)
			if err != nil {
				return "", false, err
			}
			var issues []struct {
				IID         int    `json:"iid"`
				Title       string `json:"title"`
				Description string `json:"description"`
				Author      struct {
					Username string `json:"username"`
				} `json:"author"`
			}
			if json.Unmarshal(data, &issues) != nil || issues == nil {
				return "", false, fmt.Errorf("taxion: GitLab Issue listing is unconfirmed")
			}
			for _, issue := range issues {
				if strings.Contains(issue.Description, PublicationMarker(update)) {
					if issue.Description != update.Body+"\n\n"+PublicationMarker(update) ||
						issue.Title != update.Title || issue.IID <= 0 ||
						!strings.EqualFold(issue.Author.Username, update.Account) || matched != "" {
						return "", false, fmt.Errorf("taxion: GitLab publication marker has conflicting identity, content or duplicates")
					}
					matched = strconv.Itoa(issue.IID)
				}
			}
			if len(issues) < 100 {
				return matched, matched != "", nil
			}
		}
	case "COMMENT":
		matched := ""
		for page := 1; ; page++ {
			endpoint := gitlabObjectEndpoint(update) + "/notes?per_page=100&page=" + strconv.Itoa(page)
			data, err := gitlabAPI(ctx, update.Host, env, "GET", endpoint, nil)
			if err != nil {
				return "", false, err
			}
			var notes []struct {
				ID     int64  `json:"id"`
				Body   string `json:"body"`
				Author struct {
					Username string `json:"username"`
				} `json:"author"`
			}
			if json.Unmarshal(data, &notes) != nil || notes == nil {
				return "", false, fmt.Errorf("taxion: GitLab note listing is unconfirmed")
			}
			for _, note := range notes {
				if strings.Contains(note.Body, PublicationMarker(update)) {
					if note.Body != update.Body+"\n\n"+PublicationMarker(update) || note.ID <= 0 ||
						!strings.EqualFold(note.Author.Username, update.Account) || matched != "" {
						return "", false, fmt.Errorf("taxion: GitLab publication marker has conflicting identity, content or duplicates")
					}
					matched = strconv.FormatInt(note.ID, 10)
				}
			}
			if len(notes) < 100 {
				return matched, matched != "", nil
			}
		}
	case "CLOSE":
		data, err := gitlabAPI(ctx, update.Host, env, "GET", gitlabObjectEndpoint(update), nil)
		if err != nil {
			return "", false, err
		}
		return reconcileGitLabClose(update, data)
	default:
		return "", false, fmt.Errorf("taxion: unsupported GitLab publication action")
	}
}

func sendGitLabPublication(ctx context.Context, update store.ExternalUpdate) (string, error) {
	env, err := gitlabEnvironment(update.CredentialRef)
	if err != nil {
		return "", err
	}
	base := gitlabProjectEndpoint(update.Repository)
	method, endpoint := "POST", base+"/issues"
	payload := map[string]string{"description": update.Body + "\n\n" + PublicationMarker(update)}
	switch update.Action {
	case "CREATE":
		payload["title"] = update.Title
	case "COMMENT":
		endpoint = gitlabObjectEndpoint(update) + "/notes"
		payload = map[string]string{"body": update.Body + "\n\n" + PublicationMarker(update)}
	case "CLOSE":
		method, endpoint = "PUT", gitlabObjectEndpoint(update)
		payload = map[string]string{"state_event": "close"}
	default:
		return "", fmt.Errorf("taxion: unsupported GitLab publication action")
	}
	ctx, err = publicWriteContext(ctx, update, update.Host, method, endpoint, payload, env)
	if err != nil {
		return "", err
	}
	data, err := gitlabAPI(ctx, update.Host, env, method, endpoint, payload)
	if err != nil {
		return "", err
	}
	if update.Action == "COMMENT" {
		var response struct {
			ID     int64  `json:"id"`
			Body   string `json:"body"`
			Author struct {
				Username string `json:"username"`
			} `json:"author"`
		}
		if json.Unmarshal(data, &response) != nil || response.ID <= 0 ||
			response.Body != payload["body"] || !strings.EqualFold(response.Author.Username, update.Account) {
			return "", fmt.Errorf("taxion: GitLab publication response identity is unconfirmed")
		}
		return strconv.FormatInt(response.ID, 10), nil
	}
	if update.Action == "CREATE" {
		var response struct {
			IID         int    `json:"iid"`
			Title       string `json:"title"`
			Description string `json:"description"`
			Author      struct {
				Username string `json:"username"`
			} `json:"author"`
		}
		if json.Unmarshal(data, &response) != nil || response.IID <= 0 ||
			response.Title != update.Title || response.Description != payload["description"] ||
			!strings.EqualFold(response.Author.Username, update.Account) {
			return "", fmt.Errorf("taxion: GitLab publication response identity is unconfirmed")
		}
		return strconv.Itoa(response.IID), nil
	}
	id, found, err := reconcileGitLabClose(update, data)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("taxion: GitLab Issue close is not confirmed")
	}
	return id, nil
}

func reconcileGitLabClose(update store.ExternalUpdate, data []byte) (string, bool, error) {
	var issue struct {
		IID      int    `json:"iid"`
		State    string `json:"state"`
		ClosedAt string `json:"closed_at"`
		ClosedBy *struct {
			Username string `json:"username"`
		} `json:"closed_by"`
	}
	if json.Unmarshal(data, &issue) != nil || issue.IID != update.Number ||
		issue.State != "opened" && issue.State != "closed" {
		return "", false, fmt.Errorf("taxion: GitLab Issue identity or state is unconfirmed")
	}
	if issue.State == "opened" {
		return strconv.Itoa(update.Number), false, nil
	}
	closedAt, err := time.Parse(time.RFC3339Nano, issue.ClosedAt)
	if err != nil || issue.ClosedBy == nil ||
		!strings.EqualFold(issue.ClosedBy.Username, update.Account) ||
		update.At.IsZero() || closedAt.Before(update.At.Add(-5*time.Minute)) || closedAt.After(time.Now().UTC()) {
		return "", false, fmt.Errorf("taxion: GitLab Issue close evidence does not match the approved update")
	}
	return strconv.Itoa(update.Number), true, nil
}

func gitlabProjectEndpoint(repository string) string {
	return "projects/" + url.PathEscape(repository)
}

func gitlabObjectEndpoint(update store.ExternalUpdate) string {
	kind := "issues"
	if update.Kind == "PR" {
		kind = "merge_requests"
	}
	return gitlabProjectEndpoint(update.Repository) + "/" + kind + "/" + strconv.Itoa(update.Number)
}
