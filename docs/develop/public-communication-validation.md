# 公開コミュニケーション方針の監査と検証

## 確認した公開面

Issue 113件、PR 31件、Issue comment 262件、PR comment 32件、Release 16件を保全した原文から検査した。全 PR の review API も確認し、review summary と inline review comment はいずれも0件だった。Discussions と Wiki は repository 設定で無効だった。

公開 refs の commit message 314件、annotated tag 17件、Actions run 298件の metadata、1,092 jobs、8,058 steps、2,748 log files、Release の補助 metadata / license assets 5件を検査した。対象語の全件検査と候補の文脈確認を組み合わせ、CLI・API・schema や実験方法の正当な説明を内部運用ログと区別した。原文や個別の分類台帳は公開しない。

GitHub Projects は read 権限がなく、公開 Web からも一覧を検証できなかった。存在、内容、準拠状況は未検証であり、「変更不要」と判断した対象ではない。今回の変更対象からは権限を理由に除外するが、他の対象の是正は停止しない。正式な権限または検証可能な公開一覧が利用可能になった場合に追加監査する。

## 是正と技術履歴

既存の本文・コメント・Release Notes は37記録を編集し、GitHub からの再読取りで確認した。訂正コメントの大量投稿や技術履歴の削除はしていない。無関係な環境識別情報を含んだ補助 metadata attachment 1件だけを公開から除き、検証結果と互換性は該当 Release 本文へ保存した。実行ファイルと source tag は変更していない。

Issue #143 は採用済みの正式 binary 配布と協調的な provenance 検証、保証範囲、非目標、platform ごとの rollback 制約を説明する本文へ修正した。PR #144 は監査前に merge 済みであり、merge を取消したり、文体修正のために過去の CI を再実行したりしていない。最初のレビューの修正要求、対応内容、追加レビューの承認、候補検証と正式 Release 検証の違いを保持した。

個別環境の例は一般的な Task ID と合成 hash に置き換えた。回帰テストの比較条件や拒否条件は変更していない。公開文書の正本は現在のガイドと [ADR-0045](adr/0045-public-communication-for-users-and-contributors.md) とし、commit・tag の過去の object は文体修正のために書き換えない。

既存の versioned Release manifest も immutable な配布証拠として保持した。v0.7.8 の本文とレビュー要約を編集したため、manifest に記録された本文・レビュー結果の digest は公開当時の原文に対する履歴値であり、現在の公開文の hash ではない。この区別は Release 本文にも明記し、既存の manifest や binary を再発行していない。

SDK 実験コメントからは内部の実消費量集計と実行回数を除き、事前に承認された上限は検証条件として保持した。修正要求、追加レビューの承認、停止した実行、検証範囲と未実装事項も保持した。取り下げた補助 metadata は Release の最終添付一覧から除かれ、旧 asset URL が取得不能であることも確認した。編集途中の添付一覧やリンクを、是正後の最終状態の証拠として扱わない。

## 再発防止の検証

`internal/publictext` のテストは公開 review の生成結果、正常な技術説明、raw な実行結果、内部状態、非公開パス、secret-pattern、protected identifier、誤検出・見逃しの回帰を検査する。`scripts/publictext` は不正入力で公開用の本文を出力せず、未実施の検証を成功へ補完しない。

共通 prompt の方針が各 role と runtime に伝わること、公開本文の検査が承認前に行われること、GitHub / GitLab の正確な承認本文・hash・再送制御が維持されることも検査する。旧版の未送信公開に関する制約は [Policy](public-communication-policy.md#公開用の文章を準備する手順) に記載した。

CI の `public-communication` は policy / golden / false-positive tests、公開用文書・templates・Release header の lint、secret-pattern / protected identifier scan、既存の docs / local links tests を実行する。外部 URL の可用性を検証済みとはしない。その他の既存 Go、race、web、hygiene 検証は維持する。最終 commit の結果と独立レビューの判断は対応する PR の checks とレビュー要約を参照する。

新しい binary Release は、この変更のレビューと merge が完了するまで公開しない。snapshot の native 検証を正式公開後の受入れ結果として扱わない。

## 時制と参照の検証手順

保全済みの Issue 113件、PR 31件、comment 294件（Issue 262件・PR 32件）、Release 16件、Actions run 298件の metadata を再利用し、入力 hash を確認した。公開文書・templates・automation も追加確認した。2件の既存コメントは、当初の予定と現在の追加レビュー結果を分け、更新時点を持つ注記から既存の結果へ直接リンクするよう修正した。結果の存在は GitHub API と作成時刻で確認し、編集後の再取得で本文の一致を検証した。

この2件の曖昧な案内は、結果が存在した後の編集で導入された表現だった。元の作成日と編集日を同一視して、存在しない結果を当初から断言していたという履歴には変更していない。確定したレビュー結果、当初の修正要求、未実施の正式公開検証は区別したまま保持している。

offline lint は、未確定の結果を確定済みとする表現や、参照先を特定できない位置依存の案内を検出する。単語だけで履歴を是正せず、予定を述べた文章と既存の結果を説明した文章を区別する。技術文書の inline / fenced code は説明用の引用として扱うが、公開コメントは code 記法で検査を回避できない。

`go run ./scripts/publictext -progress STATE` は明示した状態から文章を生成する。状態は `PLANNED`、`IN_PROGRESS`、`COMPLETED`、`FAILED`、`NOT_RUN`、`PUBLISHED`、`NOT_PUBLISHED`。内部の値は公開文へ出力しない。未完了・未実施・失敗・未公開の文章は結果リンクを受け取らない。完了と公開の文章には `-reference URL` を指定し、GitHub API から存在・状態・時刻を確認する。URL を補完・予測せず、取得エラー時は本文を出力しない。`-at RFC3339` は記述時点を明示する場合に使い、未来時刻は認めない。

既存の本文を検証する場合は、非公開の context JSON に記述時点の `at` と、参照ごとの `claims` を保存する。各 claim は本文内に実在する `url` と主張する `state` を持つ。`go run ./scripts/publictext -context FILE MESSAGE` で全対象リンクを検証する。作成時刻だけでなく、merge、CI 完了、review submission、Release 公開、artifact 作成などの状態確定時刻が記述時点を超えないことも検査する。未完了の CI や未 merge の PR を `COMPLETED` として検証することはできない。

対応するのは github.com の Issue、PR、Issue / PR comment、PR review、Actions run、Release tag、Release artifact の直接 URL。comment は存在を示す `EXISTS` として扱い、本文の内容から作業完了を推測しない。PR / CI / closed Issue の成功は `COMPLETED`、公開済み Release / artifact は `PUBLISHED` とする。PR の close だけでは merge と扱わない。API の更新時刻を結果の時刻の保守的な上限として使うため、後日の更新がある記録は過去時点の確認に失敗する場合がある。

この検査は参照先の metadata の確認であり、任意の自然言語の意味やリンク先の技術的正しさを保証しない。commit SHA、外部サイト、対応外の anchor はこの機能の検証対象として成功にせず、別に確認する。commit の author date だけで公開日時を推定しない。offline lint、API 参照検証、公開前の内容レビューを組み合わせる。metadata から当時の事実を確定できなければ未検証として保持し、推測で過去を書き換えない。

回帰テストは全7状態の golden、未来の作成時刻、結果確定前の参照、未完了 CI、未公開 Release、欠落した参照、取得不能、曖昧な参照と明示した予定の区別を含む。実際の公開処理も共通の本文 lint を承認前と送信前に実行するが、offline の hash / lint が外部 metadata を取得した証拠になるわけではない。
