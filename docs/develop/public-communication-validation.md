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

## 読者と言語を対象にした追加確認

Issue title 114件、PR title 33件、Release title 16件、milestone name 32件、ADR title 45件を、保全済み一覧と現在の記録から確認した。240件のタイトルのうち、当初7件を読者と主題の不一致、80件を表記や言語の修正対象、153件を変更不要と分類した。公開記録85件のタイトルと、ADR 2件のタイトルを修正した。Release の版名は維持した。

本文・コメント・Release Notes は対象語を含む227候補を文脈付きで分類し、6件の本文を修正した。機能としての役割間の受け渡し、controller の API、設定検証、実験方法やその制約を説明する文章は、用語の出現だけで書き換えていない。公開側の変更は87記録で、作成日時、識別子、技術的な判断と失敗の履歴を保持し、編集後の再取得で一致を確認した。タイトルと本文の両方を変更した記録があるため、変更数は単純に合算しない。

日本語の利用者向け説明と、英語で統一する CLI help を区別した。Conventional Commits、正式な CLI / API / schema の名前と標準見出しは保持した。過去の独立した英語の技術説明は副言語の記録として保持し、一文内で意味なく作業用語を混在させる表現と区別した。今後の投稿は明示した profile のタイトル・本文言語で検査する。

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

回帰テストは全7状態の golden、未来の作成時刻、結果確定前の参照、未完了 CI、未公開 Release、欠落した参照、取得不能、曖昧な参照と明示した予定の区別を含む。offline の hash / lint が外部 metadata を取得した証拠になるわけではない。

## プロジェクト設定と Forge 共通検査

`TAXION_COMMUNICATION_PROFILES` に version 1 の設定ファイルを指定する。形式は [Taxion の設定](../../.github/communication-profiles.json) を参照する。設定内の `provider`、`host`、`repository` は公開対象と完全一致する必要がある。`primaryLanguage` と `secondaryLanguages` は許可する言語、`titleLanguage` と `bodyLanguage` は各文章の言語を指定する。現行実装は `ja` と `en` に対応する。`technicalTerms` は正式名称、`glossary` は自然言語として統一する用語の置換方針を定義する。識別子を説明する inline code は用語の誤検出を避けるが、機密情報や未確定結果の検査を免除しない。

`visibility` は `public` または `private`、`intendedAudience` は `users`、`contributors`、`maintainers` の配列とする。private repository でも同じ品質要件を適用する。匿名の GitLab fixture は Taxion と異なる英語の設定で検証し、実運用の製品設定には使用しない。未設定のプロジェクトでは新規投稿を停止し、既存の他プロジェクトの記録は別途承認された是正作業がない限り変更しない。

公開 request の `update.statement` に、記述時点の `at` と参照の `claims` を指定する。リンクがない文章でも `at` は必須。参照がある場合は、すべての URL に `url` と `state` を対応付ける。GitHub / GitLab の adapter は書込み直前に存在・状態・時刻を照合する。任意の外部サイトや相対リンクは成功扱いにせず、対応外として拒否する。GitLab の対応参照は同一 project の Issue、MR、note、job、pipeline、Release。Issue の close は `CLOSED`、MR の merge と CI 成功は `COMPLETED`、Release 公開は `PUBLISHED`、note は `EXISTS`。検査した対象と本文以外の API payload は送信できない。

`go run ./scripts/publictext -mutation FILE` は、`provider`、`host`、`repository`、`surface`、`title`、`body`、`statement` を持つ JSON を同じ共通検査へ渡す。参照の読取りは `gh:active` / `glab:active` を既定とし、`-credential env:NAME` で明示した資格情報を選べる。本文は修正せず、検査に失敗した場合は標準出力へ出さない。このコマンド自体は投稿しない。

設定は公開承認の hash に含まれるため、設定の変更後に旧 hash を送信することはできない。移行時は未送信の記録を自動変換せず、新しい設定・記述時点・本文を明示して別 ID で再承認する。既存の未送信記録の取消しが未対応である制約は維持する。

日本語を指定した文章では、同じ行に日本語が含まれていても、未許可の英語本文を拒否する。正式名称は設定した技術用語、ソースの識別子はコード表記で区別する。送信準備中に設定が変わり、送信前の再検査で拒否された場合は、以前の診断を含めて元の未送信記録を保持する。GitHub の Issue を完了扱いにする際は、承認したアカウントによる終了と、承認以降の終了時刻も照合する。

下位 API の直接書込み、設定なし、別 project、対象変更、本文差替え、未完了 CI、未来の参照、秘密情報を拒否する回帰テストを両 Forge に置く。9種類の公開面の分類は共通検査で検証するが、現在の送信 API は Issue 作成・コメント・close と PR / MR コメントに限る。未実装の送信経路は下位 API でも拒否する。表現の検査は決定的なルールであり、任意の日本語・英語の意味を完全に判定する機構ではない。公開内容の文脈確認と独立レビューを併用する。
