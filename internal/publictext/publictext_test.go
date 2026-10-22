package publictext

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

func TestPublicMessagePolicy(t *testing.T) {
	for _, text := range []string{
		"Changes requested. The validation evidence targets a different commit.",
		"Addressed the review feedback. Native validation was not run.",
		"The `runtimeSessionId` field identifies a saved runtime.",
		"The `REQUEST_CHANGES` state rejects stale results.",
		"Published artifact SHA-256: " + strings.Repeat("a", 64),
		"See internal/runtime/controller/publication.go for exact-content approval.",
	} {
		if err := Check(text, false); err != nil {
			t.Errorf("legitimate public explanation rejected: %v", err)
		}
	}
	for name, text := range map[string]string{
		"repair-report":      "REQUEST_CHANGES findingをrepairしました。",
		"follow-up-report":   "別sessionでre-reviewを実行します。",
		"handoff":            "Coderへ戻します。",
		"raw-yaml":           "blockingItem: test failure",
		"raw-json":           `{"safeCurrentState":"ready","requiredOwner":"reviewer"}`,
		"fenced-operation":   "```json\n{\"runtimeSessionId\":\"example\"}\n```",
		"raw-review":         `{"decision":"APPROVE","findings":[]}`,
		"raw-enum":           "REQUEST_CHANGES",
		"inline-status":      "`REQUEST_CHANGES`",
		"local-session":      "/home/example/.copilot/session-state/example/files/review.json",
		"checkpoint":         "/tmp/private/checkpoint.json",
		"windows-checkpoint": `C:\Users\example\private\checkpoint.json`,
		"private-audit":      ".git/private-review/result.json",
		"packet-hash":        "packet SHA-256 " + strings.Repeat("b", 64),
		"session-id":         "session 12345678-1234-1234-1234-123456789012",
		"token":              "ghp_" + strings.Repeat("x", 36),
		"key":                "api_key=" + strings.Repeat("x", 32),
		"private-key":        "-----BEGIN OPENSSH PRIVATE KEY-----",
		"control":            "人間へ戻らず一括継続します。",
		"usage-credits":      "The experiment consumed 8.47 AI credits.",
		"usage-tokens":       "Completed with 124,915 reported input/output tokens.",
		"usage-session-cap":  "sessionごとの30 credits。",
		"usage-sessions":     "成功・失敗を含む39sessionの利用量集計。",
	} {
		t.Run(name, func(t *testing.T) {
			err := Check(text, false)
			if err == nil {
				t.Fatal("internal or sensitive public message accepted")
			}
			if strings.Contains(err.Error(), text) {
				t.Fatal("diagnostic echoed rejected content")
			}
		})
	}
}

func TestTechnicalDocumentContext(t *testing.T) {
	for _, text := range []string{
		"Schema example: `runtimeSessionId: string`.",
		"`REQUEST_CHANGES` is the stored task state.",
		"```json\n{\"decision\":\"APPROVE\"}\n```",
		"役割別 Session、自動実装・独立レビューを含める。",
		"Home directory placeholders: `/home/<user>` and `C:\\Users\\…`.",
		"The accounting example configures a ceiling of 2,000 AI credits.",
	} {
		if err := Check(text, true); err != nil {
			t.Fatal(err)
		}
	}
	for _, text := range []string{
		"repairしました", "/home/example/private/checkpoint.json", "ghp_" + strings.Repeat("x", 36),
		"```yaml\nblockingItem: private controller state\nsafeCurrentState: stopped\n```",
		`{"reviewerPacketHash":"private","requiredOwner":"controller"}`,
		`{"runtimeSessionId":"12345678-1234-1234-1234-123456789012"}`,
	} {
		if Check(text, true) == nil {
			t.Fatal("document context bypassed privacy or agent-report checks")
		}
	}
}

func TestProtectedIdentifierScan(t *testing.T) {
	sum := sha256.Sum256([]byte("private-example-47"))
	digests := []string{hex.EncodeToString(sum[:])}
	for _, text := range []string{"PRIVATE-EXAMPLE-47", "対象private-example-47の変更"} {
		if CheckIdentifiers(text, digests) == nil {
			t.Fatal("protected identifier not detected")
		}
	}
	if err := CheckIdentifiers("Generic consumer environment.", digests); err != nil {
		t.Fatal(err)
	}
	if CheckIdentifiers("content", []string{"invalid"}) == nil {
		t.Fatal("invalid configuration silently accepted")
	}
}

func reviewFixture() Review {
	return Review{
		Outcome: "REQUEST_CHANGES", Commit: strings.Repeat("a", 40),
		FeedbackAddressed: []string{"Not applicable; this is the initial review."},
		Changes:           []string{"No source changes made during review."},
		Validation:        []string{"Documentation validation was not run."},
		RemainingConcerns: []string{"Validation evidence must match the reviewed commit."},
	}
}

func TestGeneratedReviewGolden(t *testing.T) {
	actual, err := RenderReview(reviewFixture())
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile("testdata/changes-requested.md")
	if err != nil {
		t.Fatal(err)
	}
	if actual != string(expected) {
		t.Fatalf("generated public review differs:\n%s", actual)
	}
	for _, outcome := range []string{"APPROVE", "repair completed", "re-review dispatched", "CONTRACT_BLOCKED", "USAGE_EXCEEDED"} {
		report := reviewFixture()
		report.Outcome = outcome
		output, err := RenderReview(report)
		if err != nil || rawEnum.MatchString(output) || !strings.Contains(output, "was not run") {
			t.Fatalf("outcome not presented accurately: %v", err)
		}
	}
	for _, mutate := range []func(*Review){
		func(r *Review) { r.Outcome = "unknown" },
		func(r *Review) { r.Commit = "latest" },
		func(r *Review) { r.Validation = nil },
		func(r *Review) { r.Changes = []string{" "} },
		func(r *Review) { r.RemainingConcerns = []string{"blockingItem: unknown"} },
	} {
		report := reviewFixture()
		mutate(&report)
		if _, err := RenderReview(report); err == nil {
			t.Fatal("invalid report accepted")
		}
	}
}
