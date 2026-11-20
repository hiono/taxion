# GitHub Actions 調査と Fixup 修正手順

Version: 1.0
Date: 2026-11-20

本書は、現在のPull Requestに対するActions調査、失敗原因の特定、既存変更のFixup修正、Push後の検証を定義する。旧リポジトリのPR番号、Issue番号、branch名、Commit SHAを流用しない。対象リポジトリ・PR・branch・Issueは作業開始時に現在の情報から特定する。

## 1. 対象と認証

- 書込み対象は開発主体である `hiono/taxion` のみとする。
- `hiono/obsolete-taxion` と `hiono/obsolete-taxion-poc` は読み取り専用とし、Commit、Push、Issue、PR、コメントを変更しない。
- GitHub CLIを使用する場合は、事前に `gh auth status` で認証状態を確認する。
- Token、資格情報、秘密情報をソース、コマンド履歴、Issue、PR、Actionsログへ出力しない。環境変数や認証ストアを利用し、Token値をコマンド引数へ直接記述しない。

## 2. PRとHEADの確認

対象PRの現在状態を取得する。

```bash
gh pr view "$PR" --repo "$REPO" \
  --json state,isDraft,mergedAt,headRefName,headRefOid,baseRefName,baseRefOid
```

以降の調査・修正対象は、取得したHEAD SHAとbranchを基準にする。作業中にHEADが更新された場合は、差分と対象Commitを再確認する。

## 3. Commit履歴と修正対象の特定

```bash
gh api --paginate "repos/$REPO/pulls/$PR/commits?per_page=100" \
  --jq '.[] | [.sha, (.commit.message | split("\n")[0]), .parents[0].sha] | @tsv'
```

Fixup Commitを抽出する。

```bash
gh api --paginate "repos/$REPO/pulls/$PR/commits?per_page=100" \
  --jq '.[] | select(.commit.message | startswith("fixup!")) | [.sha, .commit.message, .parents[0].sha] | @tsv'
```

`fixup!` の対象メッセージとCommit履歴から修正対象となる意味的Commitを特定する。対象を特定できない場合はFixupを作成しない。

対象Commitの差分を確認する。

```bash
gh api "repos/$REPO/commits/$TARGET_COMMIT" \
  --jq '{sha, message: .commit.message, parents: [.parents[].sha], files: [.files[] | {filename, status, additions, deletions, patch}]}'
```

変更対象、変更理由、親Commit、差分、修正対象との対応を確認する。

## 4. Actionsと失敗ログの調査

対象SHAに対応するWorkflow Runを取得する。

```bash
gh run list --repo "$REPO" --commit "$TARGET_SHA" \
  --json databaseId,name,status,conclusion,headSha,url
```

PR branchの実行を確認する場合も、各Runの `headSha` が調査対象SHAと一致することを確認する。

Workflow RunのJobとStepを確認する。

```bash
gh run view "$RUN_ID" --repo "$REPO" --json jobs
```

ログを取得する。

```bash
gh run view "$RUN_ID" --repo "$REPO" --log
```

失敗したJob / Step、コマンド、対象ファイル、行番号、エラー内容を特定する。古いHEADの成功結果を現HEADの成功として扱わない。

## 5. Fixup修正

修正内容を失敗原因に限定し、無関係な意味的変更を混在させない。

ローカルcheckoutを利用する場合:

```bash
git status --short
git diff --check
git diff
# Commit前に秘密情報検査を実施する
git add <対象ファイル>
git commit --fixup="$TARGET_COMMIT"
```

Commit messageが `fixup! <対象Commitのsubject>` 形式であることを確認する。フォーマットのみの修正も既存の意味的変更に対する修正であれば対象CommitへのFixupとして分離する。

ローカルcheckoutを利用しない場合は、GitHub Contents APIまたはGit Database APIを用いて修正を反映できる。新しいFixup Commitは現在のbranch HEADを親とし、branch更新時は取得済みHEADをexpected SHAとして競合を検出する。更新前に対象差分と秘密情報検査を完了する。既存履歴の書換えやautosquashは、明示的な承認がない限り実施しない。

## 6. PushとHEAD照合

ローカルcheckoutの場合:

```bash
git push origin "$BRANCH"
git rev-parse HEAD
gh api "repos/$REPO/branches/$BRANCH" --jq '.commit.sha'
```

ローカルHEADとGitHub上のbranch HEADが一致することを確認する。リモート更新を行う場合は、更新前に確認したHEADとの不一致を検出し、他者のCommitを上書きしない。

## 7. 修正後のActions確認

修正後のHEAD SHAを取得し、そのSHAに対するWorkflow Runを再取得する。

```bash
gh run list --repo "$REPO" --commit "$NEW_HEAD" \
  --json databaseId,name,status,conclusion,headSha,url
```

必須ゲートはDevelopment Quality Gatesに従う。Linux、Windows、GoReleaser cross-build（macOS targetを含む）を確認する。macOS nativeは任意とし、通常開発の停止条件にしない。

失敗が残る場合は、再度ログから原因を特定し、対象CommitへのFixupを作成する。

## 8. Issueへの記録

現在の開発主体の関連Issueへ、次の情報を記録する。

- 修正前HEADと修正後HEAD
- Fixup Commit SHAと対象となる意味的Commit SHA
- 対象ファイルと修正内容
- Workflow Run ID、対象SHA、Linux / Windows / GoReleaserの結果
- macOS nativeの結果または保留状態
- 未確認事項と残作業

旧リポジトリのIssueへ記録しない。実行していない検証を成功扱いしない。

## 9. 完了条件

- Actionsログから失敗原因を特定している。
- Fixup対象の意味的Commitを特定している。
- 修正差分が失敗原因に限定されている。
- Commit前の秘密情報検査を実施している。
- Fixup Commitを作成し、許可されたbranchへPushしている。
- GitHub上のHEAD SHAを確認している。
- 修正後HEADに対するActions結果を確認している。
- Linux / Windows必須ゲートとGoReleaser cross-buildが成功している。
- 関連Issueへ実行結果と未確認事項を記録している。

未確認の項目を成功扱いしない。
