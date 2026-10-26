# 開発基盤の対応評価

Taxion は、Task の判断・証拠・承認を共通 controller で管理し、
repository 操作と外部の作業記録を接続先ごとの adapter に委ねる。
M13 の Human 選択は「GitLab Self-Managed＋glab を先行。Bitbucket は環境選定後に実装」である。
GitHub adapter を共通の承認・送信状態・証拠照合の実装例とし、
追加 adapter と実環境受入れは M15 の完了条件と分けて後続 Issue で追跡する。

## 公式仕様と実装・受入れの区別

| 基盤 | repository・作業記録の識別 | 公式 API の操作 | Taxion の状態 |
| --- | --- | --- | --- |
| GitHub.com | host、owner/repo、Issue/PR と番号 | Issue 作成・編集・comment・close、SHA を指定した PR review | Issue 作成・Issue/PR comment・Issue close を実装。Git・SQLite・gh 脚本で回帰確認し、承認済みコメントを実 GitHub へ一件公開して再送を照合 |
| GitHub Enterprise Server | instance host、owner/repo、種別・番号、server 版 | server 版の REST API と権限に従う | 対象 host・版・credential を選定して追加受入れする |
| GitLab.com・Self-Managed・Dedicated | instance host、project ID／URL encoded path、Issue/MR と project 内 iid | Issue 作成・note・close、MR note | Self-Managed の Issue adapter は実環境で確認済み (#75)。MR の作成・更新・close は `glab` から直接確認したが、Taxion の MR 作成・approval・close／merge は未実装。Web prefix を持つ MR の参照解決にも制約がある |
| Bitbucket Cloud | workspace、repository UUID／slug、PR ID、別作業管理の host・project・key | Cloud REST API の repository・PR・comment・approval。Issue tracker の利用可否は対象 tenant の提供状況で確認する | Cloud／Data Center と作業管理を Human が選定してから実装する |
| Bitbucket Data Center | instance host・context path、project key、repository slug／ID、PR ID、server 版 | Data Center REST API の PR・comment・参加者 approval／decline・merge | Data Center API 用 adapter と実環境受入れを後続にする |
| Jira | 作業管理 host、project、Issue ID／key、transition ID | Issue 作成・comment、許可された workflow transition | repository adapter と独立した作業管理 adapter として選定する |

外部識別は provider・host・repository path・object kind・ID の組で保存する。
同名 repository の別 host は、承認設定と origin の照合で検出する。
Bitbucket Cloud と Data Center は API と認証の系統が別である。
Cloud の Issue tracker reference だけで対象環境の利用可否を断定せず、
環境選定時に現在の提供状況と Jira 等の作業管理先を確認する。

## credential と権限

Git の fetch・push は承認された Git 認証を使用する。
Issue・PR・MR・Jira の API は各 adapter が選択した API credential を使用し、
AI Session は runtime adapter が選択した AI credential・契約参照を使用する。
Git の接続成功、API の主体確認、AI の利用可否は、それぞれ別の検証結果として保存する。

| 接続先 | API 認証と実効主体の確認 | 受入れで確認する権限 |
| --- | --- | --- |
| GitHub | gh の保存済み login、または承認された環境参照。`user` と repository を照合 | Taxion の現在の公開経路は repository の write 権限を要求する。fine-grained token の対象 repository と Issues／Pull requests 権限も確認する |
| GitLab | `glab auth login --hostname <host>` を使用。keyring・保存先と環境 token の優先順位を確認 | project のアクセスと選んだ操作の token scope、role、eligible approver、instance の承認ポリシーを確認する |
| Bitbucket Cloud | 選定した OAuth／API token／resource access token の種別と scope を確認 | token の主体と対象 resource を識別し、repository・PR・作業管理 API ごとの権限を確認する |
| Bitbucket Data Center | 対象版の HTTP access token／認証方式を選定 | repository の read／write、PR の操作権限、token 所有者と有効期限を確認する |
| Jira | Jira の host と許可された API 認証を選定 | Browse、Create／Edit／Comment、Transition 等の project 権限と workflow 条件を確認する |

glab は GitLab の認証取得を担当し、controller は実効主体・host・project と承認を照合する。
keyring の利用可否と保存方式は対象環境で確認する。
環境 token を選ぶ場合は、選定された接続先の adapter にだけ渡す。

## review・close と送信状態

共通の完了判断には、実施、acceptance、検証結果、独立 Reviewer、対象 SHA、
必要な例外承認、依存状態、外部記録を使用する。
provider の状態変更は、その判断後に行う公開操作である。

GitHub review は PR の commit と verdict、GitLab approval は MR の現在の HEAD SHA と approver に結び付く。
GitLab は `sha` 不一致を conflict として扱う。
Bitbucket は対象 PR の版・head と provider の approval semantics を追加 adapter で照合する。
Issue の完了、PR/MR の close・decline、merge は別の操作として承認する。
Jira の完了は、対象 workflow で許可された transition ID を選ぶ。

adapter は能力を確認してから操作を受け付ける。
現在の GitHub 経路は対応する host・操作・形式を検証し、追加機能の要求には能力エラーを返す。
各 adapter は共通の内容 hash、承認出典、sender claim、`PENDING/CLAIMED/UNKNOWN/SENT` を使用する。
応答不明な書込みは provider 側の ID・作者・内容・状態で照合して復旧する。

## 秘密情報の保護と漏えい対応

| 段階 | 保護と確認条件 |
| --- | --- |
| 公開前 | controller の固定 Betterleaks ルールで全未公開 commit・本文を検査する。添付の公開は形式別の検査を持つ adapter で行う |
| GitHub の受信時 | repository／user push protection の有効化、対象プラン・repository 可視性、対象 secret pattern、bypass 権限・監査を確認する |
| GitLab の受信時 | Secret Push Protection は Ultimate の GitLab.com／Self-Managed／Dedicated で提供される。対象 instance の版と有効化、除外・skip と記録を確認する |
| Bitbucket の検出・受信時 | Data Center の secret scanning は受信した新 commit を検出してメール・audit に記録する。受信拒否が必要な環境は管理者が pre-receive 保護を構成・受入れする。Cloud は選定した機能・契約の公式仕様と実測を記録する |
| 公開後 | CI の秘密検出・merge gate と通知を有効化し、公開済み範囲の調査に使う |

例外公開には、対象 SHA・内容・理由・期限・判断者と証拠を伴う Human 承認を要求する。
provider の bypass と controller の例外判断は、互いの記録を関連づけて確認する。

漏えい時は credential の失効・交換を先に行い、利用履歴と公開範囲を調査する。
clone、fork、cache、ログ、添付の残存先を列挙し、管理者と保持・削除手順を決める。
履歴書換えは Human が承認し、変更前後の SHA、署名、PR/MR、レビュー・検証証拠への影響を保存する。
古い clone からの再混入を防ぐ手順を共有し、新しい SHA でレビュー・検証をやり直す。

## 採択した後続作業と受入れ環境

[#75](https://github.com/hiono/taxion/issues/75) の Issue-only 実験では、非公開の
GitLab Self-Managed 環境で `glab` の host 指定、API account と project access を確認した。
Taxion adapter から一時 Issue を作成し、comment・close・送信結果の再照合まで実行した。
非公開の instance・利用者・project・Issue の識別情報は公開しない。

### 2026-10-26 の MR 追加確認

Human の承認に基づき、指定された非公開の実験用 project で一時 branch と draft MR の作成・更新・close を実行した。
既存ファイルと保護された既定 branch は変更せず、新しい branch に実験用テキストだけを追加した。
merge、approval の付与、実験用モデル session の起動、既存成果の削除は行っていない。
確認対象は Taxion `v0.7.19` のソースと、version API が `19.5.1`・`enterprise=false` を返す instance である。

| 対象 | 実測と受入れ範囲 |
| --- | --- |
| Taxion の repository adapter | `TestLiveGitLabRepositoryAccess` で実効主体・project・書き込み権限の照合が成功 |
| MR の作成・更新 | `glab api` で作成し、title と description の更新を再取得して照合。Taxion adapter からの操作ではない |
| 対象 SHA | commit 応答、MR の `sha`、`diff_refs.head_sha`、実験 branch の先端が一致。ソース変更後の再審査は未実施 |
| MR の close | API 操作後の再取得で `state=closed`・`closed_at`・`merged_at=null` を確認。既定 branch の SHA は実験前後で同一 |
| approval | project の `approvals`、MR の `approvals`・`approval_state` はいずれも `404`。この主体では照合できず、承認済みとして扱わない |
| 秘密保護機能 | project の `security_settings` は `404`、`secret_push_protection_enabled` は `null`。有効化・拒否・除外・監査の実環境受入れは未完了 |
| CI | project は `builds_access_level=disabled`・`jobs_enabled=false`、MR の `head_pipeline` は `null`。pipeline 一覧は `403` のため列挙できず、CI 実行の受入れは行っていない |
| Taxion の MR 参照 | 実際の MR URL を `ResolvePublicReference` に渡すと `unsupported GitLab reference`。Web prefix が repository path の前にある URL を、現在の照合処理が拒否する |

MR の操作結果と API 応答は非公開で保存し、公開記録には host・account・project・MR 番号・実験 SHA を含めない。
一時 MR は close 済みで、実験 branch は証拠として保持した。
読み取り専用の adapter 照合と、provider API の直接操作を区別する。
CLI・ACP を通した MR の一貫した操作、共通証拠への結合、approval 判定は確認済みとしない。

#75 の残件は、MR 操作の Taxion 対応、明示した Web endpoint を使う参照解決、
approval と秘密保護機能を検証できる環境での受入れである。
`404` は、この主体で API を確認できなかった記録であり、機能不在や契約 tier の断定には使わない。
MR の追加受入れは M15 や現在の GitHub 対応の受入れ条件には含めない。

### Bitbucket の環境選定

[#76](https://github.com/hiono/taxion/issues/76) で Bitbucket の Cloud／Data Center、
Jira 等の作業管理、利用する認証主体・権限を先に採択する。
現時点で Bitbucket の契約・検証環境がないため、adapter の実装・実環境受入れは後続候補とする。

## 公式参照

- [GitHub Issues REST](https://docs.github.com/en/rest/issues/issues)、
  [PR reviews](https://docs.github.com/en/rest/pulls/reviews)、
  [push protection](https://docs.github.com/en/code-security/concepts/secret-security/push-protection)
- [GitLab Issues](https://docs.gitlab.com/api/issues/)、
  [MR](https://docs.gitlab.com/api/merge_requests/)、
  [MR approval と SHA](https://docs.gitlab.com/api/merge_request_approvals/)、
  [glab auth](https://docs.gitlab.com/cli/auth/login/)、
  [Secret Push Protection](https://docs.gitlab.com/user/application_security/secret_detection/secret_push_protection/)
- [Bitbucket Cloud REST と認証](https://developer.atlassian.com/cloud/bitbucket/rest/intro/)、
  [Issue tracker reference](https://developer.atlassian.com/cloud/bitbucket/rest/api-group-issue-tracker/)、
  [Jira 連携](https://support.atlassian.com/jira-cloud-administration/docs/integrate-bitbucket-with-jira/)
- [Bitbucket Data Center REST](https://developer.atlassian.com/server/bitbucket/rest/v1000/intro/)、
  [PR API](https://developer.atlassian.com/server/bitbucket/rest/v1000/api-group-pull-requests/)、
  [secret scanning](https://confluence.atlassian.com/bitbucketserver/secret-scanning-1157471613.html)
- [Jira Issues と transitions](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issues/)
