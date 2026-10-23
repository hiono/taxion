package publictext

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var temporalContradiction = regexp.MustCompile(`(?i)(?:未公開(?:の|な)?\s*release.{0,40}(?:利用可能|公開済み)|未実施.{0,40}(?:PASS済み|成功済み)|未起動.{0,40}(?:APPROVE済み|承認済み)|未merge.{0,40}main.{0,20}反映済み|(?:unpublished|not[- ]yet[- ]published) release.{0,40}(?:is available|has been published)|CI has passed when the run is still pending|後ほど作成される.{0,30}記載している|まだ作成されていないartifact.{0,30}hashは次のとおり)`)
var positionalReference = regexp.MustCompile(`(?i)(?:後続コメント|次のコメント|後続\s*PR|後続\s*release|次の記録|完了後の成果物|later comment|following release|completed as described later|will be recorded below)`)
var plannedReference = regexp.MustCompile(`(?i)(?:完了後.{0,50}(?:追記|公開|記載)(?:します|する予定)|対応後.{0,50}(?:記載|追記|公開)する|(?:検証|CI).{0,20}完了後.{0,30}(?:判断|公開)(?:します|する)|(?:まだ|現時点).{0,30}(?:未公開|未作成|ありません)|まだ.{0,20}(?:作成|公開)されていません|will (?:record|publish|add)|will be (?:recorded|published)|not yet (?:created|available))`)
var stableReference = regexp.MustCompile(`https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/(?:issues|pull|commit|actions/runs|releases/(?:tag|download))/[^\s<>()\x60]+`)
var completedLater = regexp.MustCompile(`(?i)(?:完了した結果.{0,30}(?:後続コメント|次のコメント).{0,30}(?:記載している|記載済み)|(?:completed result|review result).{0,50}(?:recorded in a later comment|is in (?:the )?next comment))`)

func checkTemporal(text string, document bool) error {
	if document {
		text = regexp.MustCompile("(?s)```.*?```").ReplaceAllString(text, "")
		text = inlineCode.ReplaceAllString(text, "")
	}
	for _, line := range strings.Split(text, "\n") {
		if temporalContradiction.MatchString(line) {
			return fmt.Errorf("public text asserts an unavailable result as completed")
		}
		if completedLater.MatchString(line) && !stableReference.MatchString(line) {
			return fmt.Errorf("public text conflates a completed result with an unidentified later record")
		}
		if positionalReference.MatchString(line) && !stableReference.MatchString(line) && !plannedReference.MatchString(line) {
			return fmt.Errorf("public text uses an ambiguous future or positional reference; use a verified direct link or an explicit plan")
		}
	}
	return nil
}

// Reference is a remote observation, not a caller's prediction of a future URL.
type Reference struct {
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"createdAt"`
	StateAt   time.Time `json:"stateAt"`
	State     string    `json:"state"`
}

type ResolveReference func(context.Context, string) (Reference, error)

// VerifyReference requires the remote observation to match the claimed state and
// exist by the statement's timestamp. Resolvers must not fabricate missing data.
func VerifyReference(ctx context.Context, target, state string, at time.Time, resolve ResolveReference) error {
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "github.com" ||
		parsed.User != nil || parsed.RawQuery != "" || !stableReference.MatchString(target) ||
		at.IsZero() || at.After(time.Now().UTC()) || resolve == nil {
		return fmt.Errorf("public reference requires a supported direct URL, statement timestamp and remote resolver")
	}
	observed, err := resolve(ctx, target)
	if err != nil {
		return fmt.Errorf("public reference could not be verified: %w", err)
	}
	if observed.URL != target || observed.CreatedAt.IsZero() || observed.CreatedAt.After(at) ||
		observed.StateAt.IsZero() || observed.StateAt.Before(observed.CreatedAt) || observed.StateAt.After(at) ||
		observed.State != state {
		return fmt.Errorf("public reference did not exist in the claimed state at the statement timestamp")
	}
	return nil
}

type ReferenceClaim struct {
	URL   string `json:"url"`
	State string `json:"state"`
}

type StatementContext struct {
	At     time.Time        `json:"at"`
	Claims []ReferenceClaim `json:"claims"`
}

// CheckReferences verifies all supported GitHub object links, not arbitrary
// websites. API failure or an unlisted link is not success.
func CheckReferences(ctx context.Context, text string, statement StatementContext, resolve ResolveReference) error {
	targets := stableReference.FindAllString(text, -1)
	claims := make(map[string]string)
	verified := make(map[string]bool)
	for _, claim := range statement.Claims {
		if _, duplicate := claims[claim.URL]; duplicate || !strings.Contains(text, claim.URL) {
			return fmt.Errorf("public reference context contains a duplicate or unrelated link")
		}
		claims[claim.URL] = claim.State
	}
	for _, target := range targets {
		target = strings.TrimRight(target, ".,;。、")
		state, ok := claims[target]
		if !ok {
			return fmt.Errorf("public reference has no explicit state claim")
		}
		if !verified[target] {
			if err := VerifyReference(ctx, target, state, statement.At, resolve); err != nil {
				return err
			}
			verified[target] = true
		}
	}
	if len(claims) != len(verified) {
		return fmt.Errorf("public reference context contains an unsupported link")
	}
	return nil
}

// RenderProgress accepts only existing remote evidence for completed/published
// results. Other stages never emit a predicted link or completion claim.
func RenderProgress(ctx context.Context, state, target string, at time.Time, resolve ResolveReference) (string, error) {
	if at.IsZero() || at.After(time.Now().UTC()) {
		return "", fmt.Errorf("public progress requires a nonfuture statement timestamp")
	}
	body, ok := map[string]string{
		"PLANNED":       "完了後に結果を追記します。",
		"IN_PROGRESS":   "作業は進行中です。結果はまだ確定していません。",
		"FAILED":        "作業は完了していません。確認された問題を記載します。",
		"NOT_RUN":       "この検証は実行していません。",
		"NOT_PUBLISHED": "この release は公開されていません。",
	}[state]
	if ok {
		if target != "" {
			return "", fmt.Errorf("unfinished public progress must not include a result link")
		}
		return body + "\n", nil
	}
	if state != "COMPLETED" && state != "PUBLISHED" {
		return "", fmt.Errorf("unsupported public progress state")
	}
	if err := VerifyReference(ctx, target, state, at, resolve); err != nil {
		return "", err
	}
	label := "作業は完了しました。"
	if state == "PUBLISHED" {
		label = "Release は公開されています。"
	}
	body = label + "確認済みの記録: " + target + "\n"
	if err := Check(body, false); err != nil {
		return "", err
	}
	return body, nil
}
