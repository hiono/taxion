package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitHubIdentifierTexts(t *testing.T) {
	sum := sha256.Sum256([]byte("private-example-47"))
	digests := []string{hex.EncodeToString(sum[:])}
	pr := githubText{Title: "Public title", Body: "Public description", Number: 42, Commits: 1}
	pr.Base.SHA = strings.Repeat("b", 40)
	pr.Head.SHA = strings.Repeat("a", 40)
	event := githubTextEvent{PullRequest: pr}
	event.Issue = pr
	event.Comment.Body, event.Review.Body = "Public comment", "Public review"
	event.Release.Name, event.Release.Body = "Public release", "Public release notes"
	for _, name := range []string{"pull_request_target", "issues", "issue_comment",
		"pull_request_review", "pull_request_review_comment", "release"} {
		t.Run(name, func(t *testing.T) {
			messages := []string{"Public commit"}
			texts, err := githubEventTexts(name, event, messages)
			if err != nil || checkIdentifierTexts(texts, digests) != nil {
				t.Fatal("valid public text rejected", err)
			}
			private := event
			private.PullRequest.Title = "PRIVATE-EXAMPLE-47"
			private.Issue.Body = "PRIVATE-EXAMPLE-47"
			private.Comment.Body = "PRIVATE-EXAMPLE-47"
			private.Review.Body = "PRIVATE-EXAMPLE-47"
			private.Release.Body = "PRIVATE-EXAMPLE-47"
			texts, err = githubEventTexts(name, private, messages)
			if err != nil {
				t.Fatal(err)
			}
			if err := checkIdentifierTexts(texts, digests); err == nil || strings.Contains(strings.ToLower(err.Error()), "private-example-47") {
				t.Fatal("protected name accepted or echoed")
			}
		})
	}
	if _, err := githubEventTexts("pull_request_target", event, nil); err == nil {
		t.Fatal("missing commit messages accepted")
	}
	privateMessages := []string{"Private-Example-47"}
	texts, err := githubEventTexts("pull_request_target", event, privateMessages)
	if err != nil || checkIdentifierTexts(texts, digests) == nil {
		t.Fatal("protected commit message accepted", err)
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

func runGitTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func TestPullRequestCommitMessagesInventoriesMoreThan250(t *testing.T) {
	root := t.TempDir()
	bare := filepath.Join(root, "remote.git")
	work := filepath.Join(root, "work")
	if err := os.MkdirAll(work, 0700); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "init", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatalf("initialize bare repository: %v: %s", err, output)
	}
	if output, err := exec.Command("git", "init", work).CombinedOutput(); err != nil {
		t.Fatalf("initialize work repository: %v: %s", err, output)
	}
	runGitTest(t, work, "config", "user.name", "Taxion Test")
	runGitTest(t, work, "config", "user.email", "taxion-test@example.invalid")
	runGitTest(t, work, "remote", "add", "origin", bare)
	if err := os.WriteFile(filepath.Join(work, "base.txt"), []byte("base"), 0600); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, work, "add", "base.txt")
	runGitTest(t, work, "commit", "-m", "Base commit")
	baseSHA := runGitTest(t, work, "rev-parse", "HEAD")
	runGitTest(t, work, "push", "origin", "HEAD:refs/heads/main")

	const commitCount = 315
	for i := 0; i < commitCount; i++ {
		runGitTest(t, work, "commit", "--allow-empty", "-m", fmt.Sprintf("Public commit %03d", i+1))
	}
	headSHA := runGitTest(t, work, "rev-parse", "HEAD")
	runGitTest(t, work, "push", "origin", "HEAD:refs/pull/42/head")

	messages, err := pullRequestCommitMessagesAt(context.Background(), work, "hiono/taxion",
		42, baseSHA, headSHA, commitCount)
	if err != nil {
		t.Fatal("complete PR history rejected:", err)
	}
	if len(messages) != commitCount || !strings.Contains(strings.Join(messages, "\n"), "Public commit 315") {
		t.Fatalf("expected all %d commit messages, got %d", commitCount, len(messages))
	}
	if _, err := pullRequestCommitMessagesAt(context.Background(), work, "hiono/taxion",
		42, baseSHA, headSHA, commitCount-1); err == nil {
		t.Fatal("incomplete commit count accepted")
	}
	if _, err := pullRequestCommitMessagesAt(context.Background(), work, "hiono/taxion",
		42, baseSHA, strings.Repeat("a", 40), commitCount); err == nil {
		t.Fatal("changed PR head accepted")
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
