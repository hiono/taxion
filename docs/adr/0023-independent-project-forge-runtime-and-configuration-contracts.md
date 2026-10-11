# 23. project・開発基盤・AI 契約と設定の意味を独立して管理する

Date: 2026-10-11

## Status

Accepted

ADR-0003 の SDK-neutral な core と adapter の境界、
ADR-0005 の Go による先行実装を具体化する。
判断の記録と作業の追跡は ADR-0007 に従う。

## Context

Taxion の project に開発者を追加すると、作業を依頼する利用者、
repository を操作する主体、Copilot を利用する主体と費用負担者を
それぞれ確認する必要がある。GitLab・Bitbucket の repository と
Copilot を組み合わせる場合も、各サービスのアクセス権と利用契約を
独立して選択する必要がある。

2026-10-11 の Human 判断では、基盤・利用者・契約・実行環境の組合せから
モジュール化と責務分離を見直す方針が承認された。
Go を先に採用した経緯についても、TOML・YAML・JSON を扱えることを踏まえ、
設定の中心を key と value の意味・関係に置いて実装を進めると補足された。
この補足は現在の Human 判断を出典とする。

## Decision

Taxion は、共通の作業・承認・証拠の契約と、
独立して選択する接続先・操作主体・AI 利用契約を管理する制御プレーンである。
core と共通 controller が判断と実行許可を管理し、
各 adapter が外部サービス固有の API・型・credential 取得を担当する。

認証の所有者は provider CLI とする。
Copilot は Copilot CLI、GitHub は `gh`、GitLab は `glab` に
ログイン・ログアウト、credential の保管・更新・失効対応を委ねる。
Taxion は独自の OAuth、token 保管庫、provider の認証ファイルの直接解析を実装しない。
ローカル利用では provider CLI の保存済み認証を使い、
CI・無人 worker では実行環境が provider の対応する credential を注入する。
adapter の明示的な環境変数選択は provider に渡す credential の指定であり、
Taxion が token を発行・永続保存する機能ではない。

Taxion は承認した主体・接続先と provider が返す実効主体・権限を照合し、
不一致・失効・確認不能を理由付きで停止する。
provider の認証状態は Taxion の作業承認を代替しない。
利用可否の判定と課金処理は provider に委ね、
公式 API から取得できる事実と Human が承認した利用根拠を別々に記録する。
独自の契約認証・課金管理は実装せず、取得不能な支払者や課金先は推定しない。
この責務分担は 2026-10-11 の Human 指示
「Copilot CLI の認証を利用し、repository 認証は gh・glab に任せる」により採択した。
GitLab の `glab` adapter は M13 の後続実装であり、採択だけで対応済みとは扱わない。
GitLab は gitlab.com と self-hosted の両方を対象とし、
選択した API host に対して `glab` の実効主体・namespace/repository・権限を照合する。
既定 host や別 host の同名 account へ黙って切り替えない。
API host と Git clone の接続先・SSH port は別々に扱い、
SSH port を API port として推定しない。社内の接続先や認証情報は公開記録に含めない。

| 管理対象 | 保持・確認する情報 |
| --- | --- |
| project と利用者 | 参加者、依頼者、承認者、操作権限 |
| repository | provider、host、path、Git 操作主体とアクセス権 |
| 作業・レビュー記録 | 接続先、オブジェクト種別と ID、公開・更新権限 |
| AI runtime | 認証主体、利用権、契約参照、費用帰属、取得可能な利用量と上限 |
| 実行環境 | worker、credential 保管先、実行とデータの分離条件 |

controller は承認済みの組合せを Dispatch・session に結びつけ、
開始・再開・担当交代時に権限と参照先を照合する。
永続記録には主体・契約・credential 保管先の参照と確認結果を保持し、
秘密値は保護された保管先で扱う。
契約上の費用負担者と実測可能な利用量を別々に記録し、
取得できない課金情報は取得不能として明示する。

設定の共通契約は、キーの意味、値の型、必須性・既定値、参照関係、
適用範囲、上書き規則、検証条件で定義する。
初期の設定形式は YAML とする。
Go の loader が YAML を共通の型付き設定へ変換し、検証する。
TOML・JSON による設定入力の追加は将来の拡張として扱い、
追加時にも同じ設定内容に同じ検証と実行の意味を適用する。
この設定入力の選択と、既存 API・機械生成物の JSON は別の用途である。

設定ファイルは Global と Project の固定配置を使用する。
Global は OS 標準のユーザー設定ディレクトリ内の `taxion/config.yaml`、
Project は現在のディレクトリの `.taxion/config.yaml` とする。
親ディレクトリの探索は行わず、設定の project 境界を明示する。
優先順位は環境変数 > Project > Global とする。
`--file`・`--user-file`・`--project-file` による任意配置の選択は廃止する。
未指定値は下位層から継承し、各層の不正値は理由付きで停止する。

`.taxion/` は実設定・実行状態・cache 等のローカル用ディレクトリとし、
ディレクトリ全体を Git ignore の対象にする。
配下の追跡済みファイルと、ignore を打ち消す例外を検査する。
共有する仕様は `docs/`、採択した設計判断は `docs/adr/`、
再利用する lesson・decision・guardrail の公開用記録は `docs/memory/` に置く。
共有記録の追加は内容と出典の明示レビューを経て行い、
ローカルの database・session・承認・契約利用量を一括公開しない。
既存の `memory.db` は選択した ledger の隣に保持する。
`project` と `global` は現在の同一 database 内の scope であり、
設定ファイルの Global／Project 配置とは別の区分である。
具体的な配置と既存記録の移行は `docs/08-memory-model.md` に記載する。
読み込む実設定はローカルの管理対象とする。
Git worktree 内の実設定は、`git ls-files` に含まれない未追跡状態と
Git ignore の適用を両方満たす場合に読み込む。
追跡済み設定や ignore 未設定は理由付きで停止し、
追跡解除と ignore 設定は利用者が確認して実施する。
symlink は選択したパスと解決先の両方を検査する。
worktree 外の個人設定はこの Git gate の適用範囲外とする。
共有する設定例は実設定と区別して管理する。
この方針は 2026-10-11 の Human 選択により採択した。
具体的なキー名、YAML のスキーマ、OS ごとの配置と移行方法は
#69 の仕様で具体化する。
実効値の出典、未指定・明示値・不正値の扱いを説明できるようにする。

環境変数は mise・shell・CI 等の実行環境から受け取る。
mise は共有 `mise.toml` に `[env]` の `_.file = ".env"` を記述し、
実 `.env` は未追跡かつ Git ignore 済みのローカルファイルとして管理する。
共有 mise 設定は読み込み指定と安全な既定値を保持する。
秘密値を設定・表示する操作やログへの出力は、秘密値の保存・送信として扱う。
Taxion は環境変数の出典を `environment` と記録し、
dotenv の読み取りは実行環境に委ねる。mise は任意の接続方法とする。
Git ignore は通常の追加操作に対する保護であり、
追跡済みファイルと強制追加の混入は commit 前の index 検査で防ぐ。

通常設定の上書きと実行権限の承認は、別の規則として扱う。
Global・Project 設定と環境変数は、承認済みの操作権限・AI 利用契約・
データ送信先の範囲内で候補を選択する。
優先順位はその範囲内の実効値を決め、
範囲を広げる変更は Human の承認を取得する。
設定ファイルの記述と credential の保有は、候補の指定・認証に用いる。
実行許可の根拠は、出典を持つ承認として保存する。

開始時に実効主体・契約・送信先と承認の対応を保存する。
再開時に主体・契約・送信先の変更や承認範囲からの逸脱を検出した場合は、
実行を停止して再認可する。credential の更新は、同じ主体・契約・
承認範囲が確認できる場合に更新として扱う。
M12 は単一利用者の明示承認でこの境界を実装し、
M15 は project 管理者と参加者の権限に基づく承認へ具体化する。
この方針は 2026-10-11 の Human 選択により採択した。

履歴混入防止は、ローカルファイルの配置・読み込み検査、
commit 前の index 検査、公開前の未公開コミットと外部記録の検査で分担する。
既知の禁止対象パスの検査と一般的な秘密検出は別の処理として扱う。
一般的な秘密検出は既存 scanner を評価して利用し、
検査結果は秘密値を除いた形式で保持する。
ローカル hook は直接 Git を操作する開発者への補助とし、
controller 管理の commit は同じ検査を直接実行する。
検査した index snapshot を commit tree に固定し、提案する message も内容検査する。
Git の通常 commit と project hook は実行せず、単一 parent の commit と
旧 HEAD を条件にした branch 更新を用いる。署名設定は維持し、署名失敗時に
無署名へ切り替えない。Git 公開後に workspace 保存が失敗した場合は、
公開済み SHA と部分失敗を明示する。
CI・基盤側の保護は各 provider の対応能力と承認条件に従って設定する。
漏えい対応は credential の失効・交換を優先し、
履歴の書き換えは影響する SHA・レビュー・証拠と clone の再照合を伴う
Human 承認済み作業として扱う。

初期のローカル秘密検査は MIT license の Betterleaks 1.9.0 を用いる。
安定版の CLI・同梱規則を固定し、実 index の blob を stdin で検査する。
開発中の v2 と既存の v1 の CLI は区別し、更新時は規則と検出結果を再評価する。
scanner の project 設定・ignore・inline allow と credential 環境変数を取り込まず、
credential API による有効性検証は無効とする。
秘密値や資料を外部に送らず、検査不能は失敗として扱う。
規則による検出と credential の実効認証は別の確認であり、
検出漏れ・誤検知の範囲を仕様と #72 の検証記録に残す。

## Consequences

M12 #69 は共通契約と既存 GitHub＋Copilot による主体の明示選択を実装し、
#54 の実 SDK 受入れで確認する。M13 #70 は GitHub・GitLab・Bitbucket の
能力差と外部 adapter の境界を評価し、追加実装を承認された後続 Issue に分ける。
M15 #71 は複数開発者の参加・離脱・契約選択・session 所有権を実装し、
#63 で採択した組合せを検証する。

設定形式を追加するときは、共通契約に対する値の型・参照・優先順位の
同等性を検証する。接続先や担当者が変わるときは、
保存済み実行の照合と必要な再認可を伴う。
既存データの互換性、adapter の対応済み能力、未確認の能力、
後続計画は仕様と受入れ記録に保持する。

この ADR の Accepted は設計判断の採択を表す。
各機能の実装・実環境検証の進捗は、参照する Issue と Milestone で追跡する。
2026-10-11 に `config check` を固定配置へ移行し、
`.taxion/` 全体と cwd の実 `.env` の追跡・ignore 検査を追加した。
`runtime commit` は実行中の Coder に結びついた host 操作として、
禁止パス・内容・message・scope の直接検査と検査済み tree の公開を行う。
`--managed-commit` は同じ操作を Coder SDK tool に明示接続する。
Dispatch・scanner 配置・操作版を session の設定 hash に含め、
作成・再開の両方で invoking session と実行状態を照合する。
stage 済み index を対象とし、commit 成功による Task 完了や
Reviewer への commit 権限付与は行わない。
`--managed-stage` は明示した相対ファイル・追跡済み削除を private index に stage し、
scope・禁止パス・内容を検査してから実 index に反映する。
commit-only session との操作版を区別し、作成・再開の設定 hash に反映する。
directory の再帰追加、force add、任意 shell は許可しない。
Git の clean filter は適用され、検査対象は変換後の blob とする。
検査失敗時にも local object database に未参照 blob が残る場合があり、
この gate は秘密値の local 保存全体を防ぐ sandbox ではない。
`runtime auth` は固定設定と現在／発行時の承認を照合してから、
SDK の認証状態を問い合わせる。認証済み account が選択した account と一致し、
host が明示的に github.com であることを要求する。
session 作成・再開や model 呼出しは行わず、照合結果を実行許可として保存しない。
この明示 probe は repository の権限と契約上の利用可能性を確認しない。
実行時には同じ SDK 認証照合と、後述する repository access の照合を呼び出す。
SDK の `copilot:logged-in` は保存済み認証を、`env:NAME` は指定した環境変数だけを選ぶ。
任意の credential 保管庫の独自実装は行わない。
provider が公開する利用可否の確認と Human の利用承認の接続は後続作業とする。
共有 Memory の配置は Decision と `docs/08-memory-model.md`、
実 SDK の管理 stage・commit と独立レビューの受入れは #72・#54 に記録する。

`config bind --dispatch` は、現在の Task と発行時 instruction の承認に一致する
設定の組合せ、承認参照、各項目の出典、記録時刻を Dispatch に保存する。
保存後の Dispatch 設定検査と認証検査は、この組合せとの一致を要求する。
同じ値の再保存では最初の出典と時刻を維持し、別の承認済み候補への置換も拒否する。
既存 session への設定の後付けは行わず、新たに承認した Dispatch の発行を要求する。
設定未保存の旧レコードは読み取り互換性を維持する。
設定選択の記録だけでは実効認証や契約上の利用可能性を確認したことにならない。

runtime の開始・再開・送信・events・host commit は固定設定を解決し、
新しい Dispatch では未保存の承認済み設定を自動保存する。
SDK adapter は保存済みの完全な組合せを正規化して session hash に含める。
出典と記録時刻は hash に含めず、設定未保存の旧 hash は安全な停止のため維持する。
controller は設定保存済み Dispatch の開始・再開・送信前に
現在／発行時の承認、実効 Copilot account と repository API の主体・権限を照合する。
認証確認後、session の永続保存時、送信前にも承認を照合する。
管理 Git 操作では設定と承認を公開処理と同じ workspace transaction で検査する。

安全な停止では現在の設定や認証検査を要求せず、保存済みの設定で接続を試みる。
SDK 接続自体の失敗は明示し、停止できたと扱わない。
workflow run も同じ設定照合を要求し、生成 Task の承認を親 Task の承認から
自動で書き換えることはしない。provider の利用可否と Human の利用承認の接続、
会話 CLI を含む実 SDK の受入れは引き続き未完了とする。

repository access は GitHub.com の `gh api` に接続した。
`gh:active` は保存済み `gh` 認証を選び、環境の token と host の上書きを除く。
`env:NAME` は指定した値だけを `GH_TOKEN` に渡す。
`user` の account と対象 repository の `full_name`、明示的な read 権限を確認し、
Coder には write 権限も要求する。開始・再開・送信のたびに確認し、
未確認・不一致・権限不足・問い合わせ失敗は理由付きで停止する。
GitLab・Bitbucket・GitHub Enterprise はこの実行 gate の未対応対象とする。
`config check --capabilities` は repository access と runtime の実装状況、
契約・課金確認の未実装状態を表示する。表示は静的な能力の説明とし、
credential や選択した個人の識別子を出さず、外部アクセスも行わない。
認証・承認・契約上の利用権・支払者・実測利用量の違いと組合せの対応表は
README の設定契約に記載する。契約参照から支払者や取得不能な課金情報を推定しない。
API の権限は Git push の認証主体や local commit の作者を証明しない。
成功した照合は Dispatch の `configuration.accessChecks` に追記する。
操作・runtime hash・確認の開始／終了時点・役割に必要な権限を記録し、
契約上の利用可能性は `UNVERIFIED` とする。
選択済みの組合せが project・依頼者・承認・主体・接続先・契約参照を識別する。
記録 transaction は承認を再照合し、過去の照合結果を変更しない。
最初の照合が確認済み runtime hash を固定し、session 作成前の競合も拒否する。
照合の成功は session 作成や作業の成功を証明せず、次回の実行許可にも流用しない。
旧レコードに照合結果がない場合は未確認として読み取り互換性を維持する。
選択と session hash の形式は維持し、local database を一括公開しない。
安全な停止は repository credential の失効後も同じ経路を維持する。

設定承認の任意項目 `additionalTaskIds` には、同じ完全な設定候補を使える
追加 Task の ID を正確に列挙する。省略時は従来の単一 Task の承認範囲を維持する。
空 ID、空白、pattern、重複、主 Task 自身の列挙は拒否する。
flow ID が `flow`、最大修正回数が 1 の場合、Reviewer の ID は
`flow-review-0` と `flow-review-1` になる。修正 Coder は元の Task ID を維持する。
Human は必要な Reviewer ID を発行前に承認し、controller は出典を持つ承認を
そのまま生成 Task と Dispatch にコピーする。設定検査は ID の完全一致と
承認済み候補の完全一致を要求する。別 flow や未列挙の再レビューへは拡張しない。
workflow 自体の承認・上限・レビュー範囲・役割分離・実効認証は別途検査する。
