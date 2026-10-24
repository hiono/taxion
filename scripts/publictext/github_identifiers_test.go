package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitHubIdentifierTexts(t *testing.T) {
	sum := sha256.Sum256([]byte("private-example-47"))
	digests := []string{hex.EncodeToString(sum[:])}
	pr := githubText{Title: "Public title", Body: "Public description", Number: 42, Commits: 1}
	pr.Head.SHA = strings.Repeat("a", 40)
	commit := githubCommitText{SHA: pr.Head.SHA}
	commit.Commit.Message = "Public commit"
	event := githubTextEvent{PullRequest: pr}
	event.Issue = pr
	event.Comment.Body, event.Review.Body = "Public comment", "Public review"
	event.Release.Name, event.Release.Body = "Public release", "Public release notes"
	for _, name := range []string{"pull_request_target", "issues", "issue_comment",
		"pull_request_review", "pull_request_review_comment", "release"} {
		t.Run(name, func(t *testing.T) {
			texts, err := githubEventTexts(name, event, []githubCommitText{commit})
			if err != nil || checkIdentifierTexts(texts, digests) != nil {
				t.Fatal("valid public text rejected", err)
			}
			private := event
			private.PullRequest.Title = "PRIVATE-EXAMPLE-47"
			private.Issue.Body = "PRIVATE-EXAMPLE-47"
			private.Comment.Body = "PRIVATE-EXAMPLE-47"
			private.Review.Body = "PRIVATE-EXAMPLE-47"
			private.Release.Body = "PRIVATE-EXAMPLE-47"
			texts, err = githubEventTexts(name, private, []githubCommitText{commit})
			if err != nil {
				t.Fatal(err)
			}
			if err := checkIdentifierTexts(texts, digests); err == nil || strings.Contains(strings.ToLower(err.Error()), "private-example-47") {
				t.Fatal("protected name accepted or echoed")
			}
		})
	}
	privateCommit := commit
	privateCommit.Commit.Message = "Private-Example-47"
	texts, err := githubEventTexts("pull_request_target", event, []githubCommitText{privateCommit})
	if err != nil || checkIdentifierTexts(texts, digests) == nil {
		t.Fatal("protected commit message accepted", err)
	}
	for _, inventory := range [][]githubCommitText{nil, {commit, commit}, {{SHA: "wrong"}}} {
		if _, err := githubEventTexts("pull_request_target", event, inventory); err == nil {
			t.Fatal("incomplete or changed commit inventory accepted")
		}
	}
	event.PullRequest.Commits = 251
	if _, err := githubEventTexts("pull_request_target", event, nil); err == nil {
		t.Fatal("oversized pull request accepted")
	}
	if _, err := githubEventTexts("unknown", event, nil); err == nil {
		t.Fatal("unknown event accepted")
	}
	for _, policy := range [][]string{nil, {"invalid"}} {
		if checkIdentifierTexts([]string{"Public text"}, policy) == nil {
			t.Fatal("unavailable or invalid policy accepted")
		}
	}
}

func TestGitHubIdentifiersCLI(t *testing.T) {
	sum := sha256.Sum256([]byte("private-example-47"))
	t.Setenv("TAXION_PUBLIC_PRIVATE_IDENTIFIER_SHA256", hex.EncodeToString(sum[:]))
	t.Setenv("GITHUB_EVENT_NAME", "issues")
	path := filepath.Join(t.TempDir(), "event.json")
	t.Setenv("GITHUB_EVENT_PATH", path)
	for _, body := range []string{"Public description", "Private-Example-47"} {
		data, err := json.Marshal(githubTextEvent{Issue: githubText{Body: body}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		var output, warnings bytes.Buffer
		err = run([]string{"-github-identifiers"}, &output, &warnings)
		if body == "Public description" {
			if err != nil || output.Len() == 0 {
				t.Fatal("valid event failed", err)
			}
		} else if err == nil || output.Len() != 0 {
			t.Fatal("private event produced success output")
		}
	}
	for _, args := range [][]string{{"-github-identifiers", "-repository"}, {"-github-identifiers", "extra"}} {
		if run(args, &bytes.Buffer{}, &bytes.Buffer{}) == nil {
			t.Fatal("combined mode accepted")
		}
	}
}
