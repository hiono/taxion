package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
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
	Base    struct {
		SHA string `json:"sha"`
	} `json:"base"`
	Head struct {
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

func githubEventTexts(name string, event githubTextEvent, commitMessages []string) ([]string, error) {
	text := func(value githubText) string {
		return value.Title + "\n" + value.Body + "\n" + value.Name + "\n" + value.TagName
	}
	switch name {
	case "pull_request_target":
		pr := event.PullRequest
		if pr.Number <= 0 || pr.Commits < 1 || len(commitMessages) != pr.Commits ||
			!validGitSHA(pr.Base.SHA) || !validGitSHA(pr.Head.SHA) {
			return nil, fmt.Errorf("pull request commit inventory is incomplete or changed")
		}
		texts := []string{text(pr)}
		return append(texts, commitMessages...), nil
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

func validGitSHA(value string) bool {
	return regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(value)
}

func gitOutputAt(ctx context.Context, directory string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "git", append([]string{"-C", directory}, args...)...).Output()
}

func gitOutput(ctx context.Context, args ...string) ([]byte, error) {
	return gitOutputAt(ctx, ".", args...)
}

// pullRequestCommitMessages fetches the PR head as Git data only. It never
// checks out or executes code from the pull request.
func pullRequestCommitMessages(ctx context.Context, repository string, number int, baseSHA, headSHA string, expectedCount int) ([]string, error) {
	return pullRequestCommitMessagesAt(ctx, ".", repository, number, baseSHA, headSHA, expectedCount)
}

func pullRequestCommitMessagesAt(ctx context.Context, directory, repository string, number int, baseSHA, headSHA string, expectedCount int) ([]string, error) {
	if !regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`).MatchString(repository) ||
		number <= 0 || !validGitSHA(baseSHA) || !validGitSHA(headSHA) || expectedCount < 1 {
		return nil, fmt.Errorf("invalid GitHub pull request binding")
	}
	ref := fmt.Sprintf("refs/taxion/pull-request/%d", number)
	fetchRef := fmt.Sprintf("+refs/pull/%d/head:%s", number, ref)
	if _, err := gitOutputAt(ctx, directory, "fetch", "--no-tags", "origin", fetchRef); err != nil {
		return nil, fmt.Errorf("cannot fetch pull request head")
	}
	resolvedHead, err := gitOutputAt(ctx, directory, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil || strings.TrimSpace(string(resolvedHead)) != headSHA {
		return nil, fmt.Errorf("pull request head changed during identifier check")
	}
	if _, err := gitOutputAt(ctx, directory, "cat-file", "-e", baseSHA+"^{commit}"); err != nil {
		return nil, fmt.Errorf("pull request base commit is unavailable")
	}
	countOutput, err := gitOutputAt(ctx, directory, "rev-list", "--count", baseSHA+".."+headSHA)
	if err != nil {
		return nil, fmt.Errorf("cannot inventory pull request commit range")
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(countOutput)))
	if err != nil || count != expectedCount {
		return nil, fmt.Errorf("pull request commit inventory is incomplete or changed")
	}
	output, err := gitOutputAt(ctx, directory, "log", "--format=%B%x00", baseSHA+".."+headSHA)
	if err != nil {
		return nil, fmt.Errorf("cannot read pull request commit messages")
	}
	rawMessages := strings.Split(string(output), "\x00\n")
	if len(rawMessages) > 0 && rawMessages[len(rawMessages)-1] == "" {
		rawMessages = rawMessages[:len(rawMessages)-1]
	}
	if len(rawMessages) != expectedCount {
		return nil, fmt.Errorf("pull request commit inventory is incomplete or changed")
	}
	return rawMessages, nil
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
		if !validGitSHA(event.Before) || !validGitSHA(event.After) ||
			event.Before == strings.Repeat("0", 40) || event.After == strings.Repeat("0", 40) {
			return fmt.Errorf("push identifier checks require two existing commit IDs")
		}
		messages, err := gitOutput(ctx, "log", "--format=%B", event.Before+".."+event.After)
		if err != nil {
			return fmt.Errorf("cannot inventory pushed commit messages")
		}
		return checkIdentifierTexts([]string{string(messages)}, digests)
	}
	var commitMessages []string
	if name == "pull_request_target" {
		repository := os.Getenv("GITHUB_REPOSITORY")
		commitMessages, err = pullRequestCommitMessages(ctx, repository,
			event.PullRequest.Number, event.PullRequest.Base.SHA,
			event.PullRequest.Head.SHA, event.PullRequest.Commits)
		if err != nil {
			return err
		}
	}
	texts, err := githubEventTexts(name, event, commitMessages)
	if err != nil {
		return err
	}
	return checkIdentifierTexts(texts, digests)
}
