package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/store"
)

// Publisher performs approved host-side operations through gh authentication.
type Publisher struct{}

func (Publisher) Check(ctx context.Context, update store.ExternalUpdate) error {
	if update.Provider == "gitlab" {
		return checkGitLabPublication(ctx, update)
	}
	if update.Provider != "github" || update.Host != "github.com" {
		return fmt.Errorf("taxion: publication adapter is unavailable for the selected provider and host")
	}
	// Publication uses the repository credential independently from runtime settings.
	env, err := githubEnvironment(update.CredentialRef)
	if err != nil {
		return err
	}
	user, err := publicationAPI(ctx, env, "GET", "user", nil)
	if err != nil {
		return err
	}
	var identity struct {
		Login string `json:"login"`
	}
	if json.Unmarshal(user, &identity) != nil || !strings.EqualFold(identity.Login, update.Account) {
		return fmt.Errorf("taxion: publication account differs from approval")
	}
	body, err := publicationAPI(ctx, env, "GET", "repos/"+update.Repository, nil)
	if err != nil {
		return err
	}
	var repo struct {
		FullName    string `json:"full_name"`
		Permissions struct {
			Push bool `json:"push"`
		} `json:"permissions"`
	}
	if json.Unmarshal(body, &repo) != nil || repo.FullName != update.Repository || !repo.Permissions.Push {
		return fmt.Errorf("taxion: publication requires exact repository and write access")
	}
	if update.Action != "CREATE" {
		object, err := publicationAPI(ctx, env, "GET", "repos/"+update.Repository+"/issues/"+strconv.Itoa(update.Number), nil)
		if err != nil {
			return err
		}
		var target struct {
			Number      int              `json:"number"`
			PullRequest *json.RawMessage `json:"pull_request"`
		}
		if json.Unmarshal(object, &target) != nil || target.Number != update.Number ||
			(update.Kind == "PR") != (target.PullRequest != nil) {
			return fmt.Errorf("taxion: external object kind or ID differs")
		}
	}
	return nil
}

func PublicationMarker(update store.ExternalUpdate) string {
	return "<!-- taxion:" + update.Hash + " -->"
}

func (Publisher) Reconcile(ctx context.Context, update store.ExternalUpdate) (string, bool, error) {
	if update.Provider == "gitlab" {
		return reconcileGitLabPublication(ctx, update)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	env, err := githubEnvironment(update.CredentialRef)
	if err != nil {
		return "", false, err
	}
	base := "repos/" + update.Repository + "/issues"
	matched := ""
	switch update.Action {
	case "CREATE":
		for page := 1; ; page++ {
			data, err := publicationAPI(ctx, env, "GET", base+"?state=all&per_page=100&page="+strconv.Itoa(page), nil)
			if err != nil {
				return "", false, err
			}
			var issues []struct {
				Number      int              `json:"number"`
				Body        string           `json:"body"`
				Title       string           `json:"title"`
				PullRequest *json.RawMessage `json:"pull_request"`
				User        struct {
					Login string `json:"login"`
				} `json:"user"`
			}
			if json.Unmarshal(data, &issues) != nil || issues == nil {
				return "", false, fmt.Errorf("invalid issue listing")
			}
			for _, issue := range issues {
				if strings.Contains(issue.Body, PublicationMarker(update)) {
					if issue.Body != update.Body+"\n\n"+PublicationMarker(update) || issue.Title != update.Title ||
						issue.Number <= 0 || issue.PullRequest != nil || !strings.EqualFold(issue.User.Login, update.Account) || matched != "" {
						return "", false, fmt.Errorf("taxion: publication marker has conflicting identity, content or duplicates")
					}
					matched = strconv.Itoa(issue.Number)
				}
			}
			if len(issues) < 100 {
				return matched, matched != "", nil
			}
		}
	case "COMMENT":
		for page := 1; ; page++ {
			data, err := publicationAPI(ctx, env, "GET", base+"/"+strconv.Itoa(update.Number)+"/comments?per_page=100&page="+strconv.Itoa(page), nil)
			if err != nil {
				return "", false, err
			}
			var comments []struct {
				ID   int64  `json:"id"`
				Body string `json:"body"`
				User struct {
					Login string `json:"login"`
				} `json:"user"`
			}
			if json.Unmarshal(data, &comments) != nil || comments == nil {
				return "", false, fmt.Errorf("invalid comment listing")
			}
			for _, comment := range comments {
				if strings.Contains(comment.Body, PublicationMarker(update)) {
					if comment.Body != update.Body+"\n\n"+PublicationMarker(update) || comment.ID <= 0 ||
						!strings.EqualFold(comment.User.Login, update.Account) || matched != "" {
						return "", false, fmt.Errorf("taxion: publication marker has conflicting identity, content or duplicates")
					}
					matched = strconv.FormatInt(comment.ID, 10)
				}
			}
			if len(comments) < 100 {
				return matched, matched != "", nil
			}
		}
	case "CLOSE":
		data, err := publicationAPI(ctx, env, "GET", base+"/"+strconv.Itoa(update.Number), nil)
		if err != nil {
			return "", false, err
		}
		var issue struct {
			Number      int    `json:"number"`
			State       string `json:"state"`
			StateReason string `json:"state_reason"`
		}
		if json.Unmarshal(data, &issue) != nil {
			return "", false, fmt.Errorf("invalid issue state")
		}
		if issue.Number != update.Number || issue.State != "open" && issue.State != "closed" {
			return "", false, fmt.Errorf("invalid issue identity or state")
		}
		if issue.State == "closed" && issue.StateReason != "completed" {
			return "", false, fmt.Errorf("taxion: Issue closed with a different disposition")
		}
		return strconv.Itoa(update.Number), issue.State == "closed", nil
	}
	return "", false, fmt.Errorf("unsupported publication action")
}

func (Publisher) Send(ctx context.Context, update store.ExternalUpdate) (string, error) {
	if update.Provider == "gitlab" {
		return sendGitLabPublication(ctx, update)
	}
	env, err := githubEnvironment(update.CredentialRef)
	if err != nil {
		return "", err
	}
	endpoint := "repos/" + update.Repository + "/issues"
	method := "POST"
	payload := map[string]string{"body": update.Body + "\n\n" + PublicationMarker(update)}
	switch update.Action {
	case "CREATE":
		payload["title"] = update.Title
	case "COMMENT":
		endpoint += "/" + strconv.Itoa(update.Number) + "/comments"
	case "CLOSE":
		method = "PATCH"
		endpoint += "/" + strconv.Itoa(update.Number)
		payload = map[string]string{"state": "closed", "state_reason": "completed"}
	default:
		return "", fmt.Errorf("unsupported publication action")
	}
	data, err := publicationAPI(ctx, env, method, endpoint, payload)
	if err != nil {
		return "", err
	}
	var response struct {
		ID          int64  `json:"id"`
		Number      int    `json:"number"`
		State       string `json:"state"`
		StateReason string `json:"state_reason"`
		Body        string `json:"body"`
		Title       string `json:"title"`
		User        struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if json.Unmarshal(data, &response) != nil {
		return "", fmt.Errorf("publication response is unconfirmed")
	}
	matched := response.Body == update.Body+"\n\n"+PublicationMarker(update) && strings.EqualFold(response.User.Login, update.Account)
	if update.Action == "CREATE" && response.Number > 0 && matched && response.Title == update.Title {
		return strconv.Itoa(response.Number), nil
	}
	if update.Action == "COMMENT" && response.ID > 0 && matched {
		return strconv.FormatInt(response.ID, 10), nil
	}
	if update.Action == "CLOSE" && response.Number == update.Number && response.State == "closed" && response.StateReason == "completed" {
		return strconv.Itoa(response.Number), nil
	}
	return "", fmt.Errorf("publication response identity is unconfirmed")
}

func publicationAPI(ctx context.Context, env []string, method, endpoint string, payload any) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	args := []string{"api", "--hostname", "github.com", "--method", method, endpoint}
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		args = append(args, "--input", "-")
	}
	command := exec.CommandContext(ctx, "gh", args...)
	command.Env = env
	if payload != nil {
		command.Stdin = strings.NewReader(string(body))
	}
	output, err := command.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("taxion: publication API failed; delivery may be uncertain, provider details withheld")
	}
	return output, nil
}
