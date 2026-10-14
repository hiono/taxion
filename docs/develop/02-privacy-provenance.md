# 02 公開情報と出典の方針

## 公開 branch の条件

`main` は常に PRIV-001 を満たす必要があります。例外は設けません。

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
