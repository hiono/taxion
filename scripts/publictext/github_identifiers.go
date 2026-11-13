package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/publictext"
)

type githubText struct {
	Title   string `json:"title"`
	Body    string `json:"body"`
	Name    string `json:"name"`
	TagName string `json:"tag_name"`
	Number  int    `json:"number"`
	Commits int    `json:"commits"`
	Head    struct {
		SHA string `json:"sha"`
	} `json:"head"`
}

type githubTextEvent struct {
	PullRequest githubText `json:"pull_request"`
	Issue       githubText `json:"issue"`
	Comment     githubText `json:"comment"`
	Review      githubText `json:"review"`
	Release     githubText `json:"release"`
	Before      string     `json:"before"`
	After       string     `json:"after"`
}

type githubCommitText struct {
	SHA    string `json:"sha"`
	Commit struct {
		Message string `json:"message"`
	} `json:"commit"`
}

func githubEventTexts(name string, event githubTextEvent, commits []githubCommitText) ([]string, error) {
	text := func(value githubText) string {
		return value.Title + "\n" + value.Body + "\n" + value.Name + "\n" + value.TagName
	}
	switch name {
	case "pull_request_target":
		pr := event.PullRequest
		// The REST endpoint returns at most 250 commits. Never claim coverage
		// of a truncated response or a different head.
		if pr.Number <= 0 || pr.Commits < 1 || pr.Commits > 250 || len(commits) != pr.Commits ||
			commits[len(commits)-1].SHA != pr.Head.SHA {
			return nil, fmt.Errorf("pull request commit inventory is incomplete or changed")
		}
		texts := []string{text(pr)}
		for _, commit := range commits {
			texts = append(texts, commit.Commit.Message)
		}
		return texts, nil
	case "issues":
		return []string{text(event.Issue)}, nil
	case "issue_comment", "pull_request_review_comment":
		return []string{text(event.Comment)}, nil
	case "pull_request_review":
		return []string{text(event.Review)}, nil
	case "release":
		return []string{text(event.Release)}, nil
	default:
		return nil, fmt.Errorf("unsupported GitHub identifier event")
	}
}

func checkIdentifierTexts(texts, digests []string) error {
	if len(digests) == 0 {
		return fmt.Errorf("identifier checks require protected identifier policy")
	}
	for _, text := range texts {
		if err := publictext.CheckIdentifiers(text, digests); err != nil {
			return err
		}
	}
	return nil
}

func checkGitHubIdentifiers() error {
	digests := strings.Fields(os.Getenv("TAXION_PUBLIC_PRIVATE_IDENTIFIER_SHA256"))
	if err := checkIdentifierTexts([]string{""}, digests); err != nil {
		return err
	}
	data, err := os.ReadFile(os.Getenv("GITHUB_EVENT_PATH"))
	if err != nil {
		return fmt.Errorf("cannot read GitHub event")
	}
	var event githubTextEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("cannot decode GitHub event")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	name := os.Getenv("GITHUB_EVENT_NAME")
	if name == "push" {
		sha := regexp.MustCompile(`^[0-9a-f]{40}$`)
		if !sha.MatchString(event.Before) || !sha.MatchString(event.After) ||
			event.Before == strings.Repeat("0", 40) || event.After == strings.Repeat("0", 40) {
			return fmt.Errorf("push identifier checks require two existing commit IDs")
		}
		messages, err := exec.CommandContext(ctx, "git", "log", "--format=%B",
			event.Before+".."+event.After).Output()
		if err != nil {
			return fmt.Errorf("cannot inventory pushed commit messages")
		}
		return checkIdentifierTexts([]string{string(messages)}, digests)
	}
	var commits []githubCommitText
	if name == "pull_request_target" {
		repository := os.Getenv("GITHUB_REPOSITORY")
		if !regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`).MatchString(repository) ||
			event.PullRequest.Number <= 0 {
			return fmt.Errorf("invalid GitHub pull request binding")
		}
		endpoint := fmt.Sprintf("repos/%s/pulls/%d/commits?per_page=100", repository, event.PullRequest.Number)
		data, err := exec.CommandContext(ctx, "gh", "api", "--paginate", "--slurp", endpoint).Output()
		if err != nil {
			return fmt.Errorf("cannot inventory pull request commits")
		}
		var pages [][]githubCommitText
		if err := json.Unmarshal(data, &pages); err != nil {
			return fmt.Errorf("cannot decode pull request commit inventory")
		}
		for _, page := range pages {
			commits = append(commits, page...)
		}
	}
	texts, err := githubEventTexts(name, event, commits)
	if err != nil {
		return err
	}
	return checkIdentifierTexts(texts, digests)
}
