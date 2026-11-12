# 公開情報と出典の方針

## 公開 branch の条件

`main` は常に PRIV-001 を満たす必要があります。例外は設けません。

<a id="oss-development-boundary"></a>

## OSS開発と実業務環境の分離

OSS環境ではTaxion本体を開発・検証し、継続して利用できる品質まで安定させます。
機密コードや機密workspaceの提示・搬入は、その前提条件ではありません。
実業務の対象情報がなくても、承認済みの範囲でOSS側に再現できる調査・修正・検証を進めます。

実業務環境では安定版Taxionを導入し、その環境内の承認済みworkspaceで開発します。
機密コード、workspace、実行記録は実業務環境内に保持し、OSS環境へ持ち込みません。
この分離は、実業務環境内でTaxionが承認済みの機密コードを扱うことを禁止する規則ではありません。

OSS側へ戻すのは、機密情報を除いた不具合報告、改善要望、利用上の知見です。
再現できない問題は、非機密の再現条件・観測結果・期待動作を使って分析します。
実業務の機密workspace、実コード、raw log、storeの提出を求めず、公開可能な入力で最小の再現例を作ります。
情報を匿名化しても、非公開の実装や履歴を移植してよいことにはなりません。

この方針は [ADR-0002](adr/0002-develop-clean-room-with-no-ported-code-or-history.md) の出典境界を維持します。
Taxionの開発Agentは [repositoryの指示](../../AGENTS.md) からこの方針を参照し、作業対象や停止理由を判断するときに適用します。
導入、実行、審査、公開、配布、復旧には、それぞれの既存の承認・品質・予算・期限条件を適用します。
追加予算や時間だけで、別の操作の権限が増えることはありません。

## 公開成果物に含めない情報

- 個人名、email、user ID。
- home directory の path（`/home/<user>`、`C:\Users\…`）。
- 内部 hostname、intranet URL。
- 組織固有のリポジトリ名・プロジェクト名。
- 非公開の先行プロジェクトや PoC の名称。
- token、key、credential（`gho_`、`github_pat_`、`glpat-`、`.pem`）。

## 他の資料から知見を取り込む手順

1. 要件、ガードレール、手順、失敗のパターンを取り出します。
2. 人物、host、path、リポジトリ名を除き、`coder`、`reviewer`、`implementation-repo`、`validation-target` などの一般的な役割に置き換えます。
3. timestamp、内部 URL、個人情報を含む screenshot を除きます。
4. merge 前に、別の担当者がレビューします。

## CI の検査範囲

`hygiene` は、Go source と選択した設定ファイルで、完全な token パターンと秘密鍵 header を検査します。Markdown と workflow 自体は対象外です。一致すると job が失敗します。

個人名、内部 host、任意の機密情報は検査しません。これらはレビューで確認します。
