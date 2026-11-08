# ADR-0076: ADR適合の根拠を承認と独立レビューへ固定する

## Status

Accepted。2026-11-08 の利用者の明示承認に基づく。
対象は ADR 確認ゲートの実装・検証・文書同期であり、配布や既存 store への適用は別途扱う。

## Context

認可と検証の成功だけでは、候補が採択済み ADR に従うことを確認できない。
一方、確認欄の追加や Coder の自己申告だけで合格にすると、ADR を変更せず実装だけが逸脱した状態を通してしまう。
適用する決定と確認した候補を固定し、独立 Reviewer の判断を既存の受入れ証拠として扱う必要がある。

## Decision

新しい汎用 ADR 解釈器や別の承認方式は作らない。
既存の `Task.acceptance`、`Task.specifications`、独立 Reviewer の `DispatchResult.acceptance` を使う。
ADR ごとの受入れ criterion は `adr-compliance:<specification-id>:<SpecificationHash>` とする。
`SpecificationHash` は出典、version、引用本文、用途を含む canonical hash であり、
既存の ingestion と runtime 利用の承認を代替しない。
単独の document hash ではなく、承認した specification 全体の hash を使う。
これらの criterion と source は Task の提案・承認・発行指示に含まれるため、無断で差し替えられない。

適用する ADR がない場合に限り、提案へ `adr-compliance:not-applicable` を明示する。
独立 Reviewer の根拠と、理由を述べた `workReport` がなければ、この判断を `PASS` として受理しない。
source の未選定、読取り失敗、本文不足を「適用なし」に読み替えない。
引用資料は決定の根拠であり、それ自体が実行権限を追加するものではない。

各ゲートで次を確認する。

- Contract Gate は適用 ADR の正確な source と利用承認、criterion の結合を検査する。未宣言は `BLOCKED`、不正な結合は `FAIL` とする。`bound` は確認対象の固定であり、候補の ADR 適合合格ではない。
- Execution Gate は最新の正式な独立 Reviewer、現在の候補 commit/tree、元の ADR criterion と snapshot、各判断の根拠を照合する。Coder の結果や別の候補の判定は使わない。
- Publication Gate は同じ ADR 判定を引き継ぐ。違反は `FAIL`、必要な確認が未完了なら `BLOCKED` とし、既存の品質・diagnostic・公開承認の検査も維持する。

複数の ADR の判定が混在していても、確認済みの違反を未確認の判定で隠さない。
criterion の並びにかかわらず `FAIL` を `BLOCKED` より優先する。

Reviewer の `PASS`・`FAIL`・`BLOCKED` は、該当する完全な criterion を evidence に含める。
`APPROVE` だけ、一般的な test 成功だけ、根拠を持たない違反の自己申告だけでは正式な ADR 判断として受理しない。
既存の独立性、結果 receipt、同一対象、検証と Git 証拠の検査は省略しない。
発行と Flow 作成の入口でも、宣言された ADR criterion と source の不整合を拒否する。

## Compatibility and rollback

ゲートの JSON に `adrCompliance` を追加する。version 1 と既存の exit code は維持する。
`status` は `bound`・`pass`・`fail`・`blocked`、根拠は `criteria` と `reviewerDispatchId` で示す。
識別子は credential redaction の対象とし、本文やローカル source の内容を report に出さない。
ゲート生成は read-only であり、Task・Flow・Dispatch・承認・監査を変更しない。

ADR criterion のない旧 Task は、新しい適合ゲートの合格にはしない。
旧 Task と承認を自動更新せず、既存実行を取消したり、保存結果を成功へ補完したりしない。
適合確認を含む新しい提案を作る場合は、変わった承認対象に対する既存の正式な差分承認を使う。
`gate resume` は保存済み内部遷移の照合であり、この ADR 適合合格の代用にはしない。
直接の Flow 作成・Dispatch 発行は、宣言された criterion の不整合を拒否する。
未宣言の契約にまで新しい ADR 確認を自動挿入する入口の全面移行は行わない。
直接操作の従来の実行許可を、ADR 適合ゲートの合格と読み替えない。
同一承認内の内部遷移へ、ADR 未選定を理由とした無断の再契約や権限拡張を追加しない。

Task と結果の保存形式に新しい field は加えないため、既存の保存履歴は書き換えない。
旧 binary は新 criterion の意味を検査できない。rollback 時も ADR 適合の合格を旧版へ委ねず、
候補・承認・結果・検証記録を保全する。

## Limitations and validation

ゲートは、承認に固定した ADR に対する正式な独立判断と対象の一致を検証する。
すべての ADR の自動発見、自然言語の完全な意味解釈、Reviewer の見落としがないことは保証しない。
適用範囲を省いたり、ADR の Decision を弱めたりして合格にしない。
独立 Reviewer は適用する決定の原文と候補の具体的な箇所を確認し、未確認の主張を `PASS` にしない。

対象試験は source/hash の差替え、汎用 evidence による代用、根拠のない非適用、
Coder-only、同一候補の独立 `PASS`・`FAIL`・`BLOCKED`、候補 drift、墨消しと read-only 不変性を扱う。
通常試験・変更 package の race 試験の結果は下記に記録する。
独立品質レビューと公開識別子検査、配布、既存の公開復旧問題の解消は、この実装の検証成功だけでは証明しない。

この決定は [ADR-0075](0075-blacklist-authorization-and-recovery.md) の拒否条件と承認境界を維持し、
追加された適合確認を「同じ承認にないことだけを理由とした拒否」の代用にはしない。

## Validation record

2026-11-08 の実装検証では、`go build ./...`、`go vet ./...` と ADR・ゲート・resume・CLI・文書の対象試験が成功した。
旧 fixture の未宣言 ADR を合格とする期待は修正した。
既存承認だけを更新する fixture は ADR 未確認の `BLOCKED` を期待し、旧契約の criterion を補完しない。
合格を検証する新しい synthetic fixture は、非適用を最初の承認へ含めて独立 Reviewer の根拠を返す。

その後の全通常試験と変更 package の race 試験、最終 CLI race は command timeout で終了した。
core race と最終 CLI 通常試験の成功は保存したが、timeout を package 全体の合格と扱わない。
未完了の Controller 通常試験、Controller race、CLI race だけを直列で検証し、
成功済みの変更のない package は再実行しない。
最終の直列検証は exit 0 で完了した。

- `go test -json ./internal/runtime/controller`: package 成功、334.653 秒。
- `go test -json -race ./internal/runtime/controller`: package 成功、1457.735 秒。
- `go test -json -race ./cmd/taxion`: package 成功、163.851 秒。

直列の通常試験開始後に、先行する `BLOCKED` が後続の ADR 違反を隠す集約処理を修正した。
修正後の `go test ./internal/runtime/controller -run "^TestADR" -count=1` は成功し、
最終 Controller race でも両方の判定順序を検証した。
通常試験の記録は、一括 command の timeout を成功に置き換えたものではなく、
保存済みの package 成功と修正後の対象試験を組み合わせたものである。
この検証成功は、独立品質承認、保護済み公開識別子検査、配布、
既存の公開復旧問題の解消や認可入口の全面移行を示さない。
