package publictext

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestTemporalMessagePolicy(t *testing.T) {
	for _, text := range []string{
		"完了した結果は後続コメントに記載している",
		"後続コメントに結果を記載済み", "後続PRを参照", "review結果は次のコメントにある",
		"完了後の成果物を参照してください",
		"The completed result is recorded in a later comment",
		"完了後に結果を追記します。完了した結果は後続コメントに記載している",
		"The unpublished release is available below",
		"CI has passed when the run is still pending",
		"未公開releaseを利用可能として記載", "後ほど作成される記録に記載している",
		"未実施のvalidationをPASS済みとして記載",
		"未起動のreviewをAPPROVE済みとして記載",
		"未mergeの変更をmainへ反映済みとして記載",
	} {
		for _, document := range []bool{false, true} {
			if Check(text, document) == nil {
				t.Errorf("inconsistent temporal claim accepted: %q (document=%v)", text, document)
			}
		}
	}
	for _, text := range []string{
		"完了後に結果を追記します",
		"結果は既存のコメントへの直接リンクで確認できます",
		"CIの完了後にmerge可否を判断します",
		"現時点ではreleaseは未公開です",
		"次のコメントはまだ作成されていません。", "The result will be recorded below after validation.",
		"追加レビューの結果は https://github.com/example/tool/pull/12#issuecomment-34 を参照。",
		"後続コメントの結果: https://github.com/example/tool/pull/12#issuecomment-34",
		"Artifact hashes will be published after release validation.",
		"対応後の判断は後続コメントに記載する。",
	} {
		if err := Check(text, false); err != nil {
			t.Errorf("plan or qualified reference rejected: %q: %v", text, err)
		}
	}
	if err := Check("Rejected fixture: `The completed result is recorded in a later comment`.", true); err != nil {
		t.Fatal("technical quotation rejected", err)
	}
	if Check("`The completed result is recorded in a later comment`", false) == nil {
		t.Fatal("inline code bypassed a public message check")
	}
}

func TestTemporalReferenceAndProgressGolden(t *testing.T) {
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	target := "https://github.com/example/tool/actions/runs/123"
	resolve := func(_ context.Context, url string) (Reference, error) {
		return Reference{URL: url, CreatedAt: at.Add(-time.Hour), StateAt: at.Add(-time.Minute), State: "COMPLETED"}, nil
	}
	var golden strings.Builder
	for i, state := range []string{"PLANNED", "IN_PROGRESS", "COMPLETED", "FAILED", "NOT_RUN", "PUBLISHED", "NOT_PUBLISHED"} {
		link := ""
		resolver := resolve
		if state == "COMPLETED" || state == "PUBLISHED" {
			link = target
			if state == "PUBLISHED" {
				link = "https://github.com/example/tool/releases/tag/v1.0.0"
				resolver = func(_ context.Context, url string) (Reference, error) {
					return Reference{URL: url, CreatedAt: at.Add(-time.Hour), StateAt: at.Add(-time.Minute), State: "PUBLISHED"}, nil
				}
			}
		}
		body, err := RenderProgress(t.Context(), state, link, at, resolver)
		if err != nil || rawEnum.MatchString(body) {
			t.Fatalf("progress rendering failed: %s: %v", state, err)
		}
		if i > 0 {
			golden.WriteString("\n")
		}
		golden.WriteString(body)
	}
	expected, err := os.ReadFile("testdata/progress.md")
	if err != nil || golden.String() != string(expected) {
		t.Fatalf("temporal golden differs: %v\n%s", err, golden.String())
	}
	for _, mutate := range []func(*Reference){
		func(r *Reference) { r.CreatedAt = at.Add(time.Second) },
		func(r *Reference) { r.StateAt = at.Add(time.Second) },
		func(r *Reference) { r.CreatedAt = time.Time{} },
		func(r *Reference) { r.StateAt = time.Time{} },
		func(r *Reference) { r.StateAt = r.CreatedAt.Add(-time.Second) },
		func(r *Reference) { r.State = "IN_PROGRESS" },
		func(r *Reference) { r.State = "NOT_PUBLISHED" },
		func(r *Reference) { r.URL += "4" },
	} {
		resolver := func(ctx context.Context, url string) (Reference, error) {
			r, err := resolve(ctx, url)
			mutate(&r)
			return r, err
		}
		if body, err := RenderProgress(t.Context(), "COMPLETED", target, at, resolver); err == nil || body != "" {
			t.Fatal("unverified/future/pending evidence produced completed output")
		}
	}
	unavailable := func(context.Context, string) (Reference, error) {
		return Reference{}, errors.New("resource not found")
	}
	if body, err := RenderProgress(t.Context(), "PUBLISHED", target, at, unavailable); err == nil || body != "" {
		t.Fatal("nonexistent result produced a public link")
	}
	if body, err := RenderProgress(t.Context(), "NOT_RUN", target, at, resolve); err == nil || body != "" {
		t.Fatal("unperformed work produced a result link")
	}
	for _, state := range []string{"unknown", ""} {
		if _, err := RenderProgress(t.Context(), state, "", at, resolve); err == nil {
			t.Fatal("unknown state silently accepted")
		}
	}
	text := "Result: " + target + "\nRepeated reference: " + target
	statement := StatementContext{At: at, Claims: []ReferenceClaim{{URL: target, State: "COMPLETED"}}}
	if err := CheckReferences(t.Context(), text, statement, resolve); err != nil {
		t.Fatal(err)
	}
	if CheckReferences(t.Context(), text, StatementContext{At: at}, resolve) == nil {
		t.Fatal("unclaimed reference accepted")
	}
	statement.Claims = append(statement.Claims, statement.Claims[0])
	if CheckReferences(t.Context(), text, statement, resolve) == nil {
		t.Fatal("duplicate claims accepted")
	}
}
