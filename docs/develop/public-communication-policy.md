# Taxion OSS Public Communication Policy

Taxion の公開記録は、利用者、外部コントリビューター、将来の保守者、セキュリティ調査者、リリース利用者に向けて書きます。Issue、PR、レビュー、Release Notes、ADR、Actions の表示・summary に即時適用します。公開済みの記録も対象です。

## 問題と技術的事実を直接説明する

公開記録には、問題、利用者への影響、原因、変更内容、互換性、検証結果、既知の制約、次に必要な公開手続きを記載します。検証対象の commit と結果を対応付け、未実施、失敗、対象外を成功と混同しません。追加レビューで承認された場合も、最初の修正要求やその理由を隠しません。

一人開発で実在しない組織や担当者を演じたり、自分に向けて作業完了を報告したりしません。「レビューで指摘されたリンク検証の範囲を明記した」のように、何が変わったかを書きます。自動レビューは自動レビューと説明し、人間や別の GitHub アカウントによる承認に見せません。

Approved、Changes requested、Review feedback、Follow-up review、Validation などの標準用語は使用できます。Taxion の機能名、CLI、API、schema の識別子も説明に必要な範囲で使用できます。技術用語の使用と、開発時の内部通信の転載は区別します。

## 公開記録の標準構成

日本語の本文でも、次の見出しを使えます。該当しない項目は理由を簡潔に記載し、空欄を架空の事実で埋めません。

| 記録 | 構成 |
| --- | --- |
| Issue | Summary / User impact / Current behavior / Expected behavior / Reproduction / Scope / Non-goals / Acceptance criteria |
| PR | Summary / Motivation / Changes / Compatibility / Validation / Security considerations / Documentation / Related issue |
| Review response | Feedback addressed / Changes made / Validation / Remaining concerns |
| Release Notes | Added / Changed / Fixed / Security / Compatibility / Upgrade / Rollback / Known limitations |
| ADR | Context / Decision / Alternatives considered / Consequences / Security and operational impact / Compatibility / Status |

README と利用者ガイドは、導入や操作に必要な順序を優先します。この表を理由に操作手順を Issue の形式へ並べ替えません。

## 内部監査と公開説明を分ける

開発時の指示全文、役割間通信、packet の識別情報・hash、session ID、Dispatch の実行識別子、model usage、retry・repair counter、checkpoint、機器固有の絶対パス、controller の逐次状態は非公開の監査記録に保存します。raw YAML / JSON の実行結果を公開説明の代わりに貼り付けません。公開文から非公開パスや識別子を参照することもしません。

公開するレビュー要約は、対象 commit、判断、指摘された技術的問題、対応内容、検証結果、残る制約を記載します。入力全文や機械向け返却形式は転載しません。機械可読の内部証拠にはアクセス制御と保存期間を設け、原文と公開要約の対応を非公開で保持します。公開が必要な互換性 schema や Release manifest は、文書化した公開 artifact として扱います。

たとえば内部の修正要求は公開文では「Changes requested」、指摘への対応は「Review feedback addressed」、追加レビューの依頼は「A follow-up review has been requested」と表します。ただし、実際に依頼していないレビューを依頼済みとは書きません。必要条件が満たされない場合は、条件と影響を通常の文章で説明します。

## 過去記録も文脈を保って是正する

作成時期、Issue 番号、open / closed / merged の状態では除外しません。原文を非公開で保全し、編集可能な本文やコメントは GitHub の正式な編集機能で修正します。原因、影響、判断、失敗、risk、互換性、検証結果、日付、commit との関係を保持します。機械的な単語の一括置換で技術的意味を変えません。

編集できない場合や、編集によって会話の意味が変わる場合は、同じスレッドへ対象を明示した訂正を追加します。元の技術判断の有効性、正しい変更内容、検証結果を簡潔に記載します。同じ訂正文を無差別に投稿しません。commit や tag の履歴を文体修正のために書き換えず、必要な説明を対応する公開記録に追加します。

削除は credential、secret、個人情報、非公開製品情報、明白な重複、空または壊れた自動投稿に限定します。機密が含まれる場合は公開範囲を最小化し、必要に応じて GitHub の機密情報除去手続きを使います。文章が不自然という理由で、失敗やレビュー判断を削除しません。

## 自動生成と検証

公開文章の生成では、内部の判断値を読者向けの文章へ変換してから、正確な本文を承認します。承認後の送信処理では本文を変換しません。本文の hash と送信内容の一致、重複送信の防止、既存の secret scan は維持します。

公開用 templates、生成コメント、Actions summary、Release Notes を検査します。内部状態の列挙、非公開パス、secret の形、内部役割間の進捗報告を検出し、正当な技術説明は区別します。検査の成功だけで文章の正確性や機密情報の不存在を保証せず、公開前に文脈と事実を確認します。自動検査には正常例、拒否例、誤検出を防ぐ回帰、生成文章の期待結果を含めます。

権限不足で確認できない公開面は、存在・内容・準拠状況が未検証であると個別に記録します。属性が取得できないことを「変更不要」の根拠にせず、確認可能な他の対象の是正は継続します。

## 公開用の文章を準備する手順

Issue と PR は `.github` の templates、レビューへの対応は [response template](../../.github/review-response-template.md)、新しい ADR は [ADR template](adr/template.md) を使用します。Release Notes の構成は `.goreleaser.yaml` の header を正本とします。raw なモデル返却を直接投稿する自動処理は設けません。

既存の review 結果から文章を作る場合は、非公開の JSON に `outcome`、40桁の `commit`、文字列配列の `feedbackAddressed` / `changes` / `validation` / `remainingConcerns` を指定します。各配列には実際の結果を記載し、未実施や対象外も明記します。`go run ./scripts/publictext -report FILE` は公開向け Markdown を標準出力に生成するだけで、承認や投稿はしません。内容と識別情報を確認し、その本文を既存の `records proposal` / `approve` へ渡します。

`records proposal` / `approve` は内部実行ログや非公開パスを含む文章を拒否します。送信時にも承認済み本文を再検査し、変換・自動修正は行いません。このため、旧版で承認された未送信の本文が新方針に違反する場合は送信できません。保存済み記録を編集せず、読み手向けの新しい本文を別 ID で明示承認してください。送信済みの記録は自動再送しません。

新しい本文を承認しても、前の未送信記録が解決済みになるわけではありません。現行の `records` には承認済み公開の取消機構がなく、未解消の公開がある Task の自動 close / archive は引き続き停止します。DB の直接編集や、送信していない記録を送信済みとする操作で回避しません。この制約は公開本文を承認後に変更しないことを優先したものです。

`go run ./scripts/publictext -repository` は docs、templates、workflow、Release header、公開用 golden output と入口文書を検査します。schema を説明する技術文書では識別子を許容しますが、secret や具体的な非公開パスは許容しません。外部の公開文を検査するときはファイルを引数に指定します。`-document` は技術文書に限って指定し、通常のコメントの検査を回避するためには使いません。

protected identifier の照合には、非公開の識別子を小文字に正規化した ASCII token の SHA-256 一覧を `TAXION_PUBLIC_PRIVATE_IDENTIFIER_SHA256` に渡します。実際の `records proposal` / `approve` / `send` はこの一覧を必須とし、未設定・空・不正な digest・保護対象との一致があれば公開を停止します。送信直前にも再検査するため、承認後に一覧を更新して保護対象となった保存済み本文も送信しません。一覧は信頼する運用設定として管理し、空設定を回避するためのダミー値を本番に設定しません。

CI は repository secret `PUBLIC_TEXT_PRIVATE_IDENTIFIER_SHA256` から digest だけを受け取り、識別子そのものは受け取りません。repository 検査では source と test fixture を含む Git 管理対象・未除外の text file も照合し、一覧が未設定・空なら失敗します。fork PR などで secret が渡されない場合も検査成功とはせず、変更を確認したうえで信頼する repository branch の設定済み CI で検証します。secret を得るために未確認の fork code を privileged な workflow で実行しません。

投稿を行わない単体ファイルの lint / render に限り、設定が利用できない事実を警告し、private-name の網羅性を主張しません。この警告付きの実行を、repository 検査の成功や実際の公開処理の許可として扱いません。未知の名前や識別子をすべて自動検出する仕組みではありません。
