# Memory

この文書は、現在の CLI と保存処理を説明する。log・lesson・decision・guardrail の提案モデル、予定した保存場所、昇格の理由は [ADR-0017](../develop/adr/0017-memory-layers-ranges-and-promotion-path.md) に記録している。実装結果は #28〜#34、#38、#66 に記録している。

## 保存と scope

`memory put`・`get`・`list` は `lesson`・`decision`・`guardrail`・`concern` の4種類を扱う。新規 entry の既定 status は `proposed`。`guardrail` として保存しただけでは、実行時の検査として有効にならない。

CLI は、選択した Ledger と同じ directory の `memory.db` に `project` と `global` の両 entry を保存し、`scope` column で区別する。コマンドより前に global option の `--store PATH` を指定すると、Ledger と Memory database の directory を選べる。

書込みの `put`・`warn`・`approve` は、既定で `project` を使う。`--scope global` は明示が必要で、通知を表示するが確認は求めない。読取りの `list`・`search`・`export` は、`--scope` で絞らなければ両 scope を含む。現在の CLI は project と global に別々の保存 directory を設けていない。

### ローカル状態と共有する知識

既定の Ledger は `.taxion/ledger.json` で、`memory.db` と `workspace.db` は同じ directory にある。`.taxion/` 全体はローカル専用で、Git の ignore 対象とする。独自の `--store` は別のローカル directory を選ぶものであり、共有知識の取得元を選ぶものではない。権限と backup は所有者が管理する。Runtime session のファイルや、非公開の認証・利用量の観測もローカルに保持する。

共有する仕様は `docs/`、採択した設計判断は `docs/develop/adr/`、明示レビューした再利用可能な Memory 記録は `docs/develop/memory/` に置く。これらの Markdown は文書であり、Runtime entry として自動で読み込んだり承認したりしない。各記録には安定した識別子、revision、適用 scope、出典、保持する判断や知見を記載する。credential 値、ローカル設定、非公開の利用量、未レビューの database dump は含めない。

既存 entry を共有するときは、元のローカル database を保持し、本文、出典、バージョン、承認を個別に確認する。`memory export --scope project` は sanitizer を適用したプレビューを出力するが、database 移行や実行可能な policy の export ではない。共有する知識を選んでレビューし、元の記録への出典を添えて `.taxion/` の外に記載する。sanitizer によって意味が変わる場合があり、非公開情報の完全な除去も保証しない。通常の範囲確認と秘密検査を経てファイルを commit し、ローカル database 自体を移動したり強制的に add したりしない。

別のローカル保存先で共有記録を再利用するときは、`memory put` で kind、本文、scope を指定する。`--source` には、共有リポジトリの revision、path、記録の識別子と revision を記載する。取り込んだ本文は提案された知識として扱う。文書の公開や旧保存先の承認は、新しい保存先の実行許可にはならない。guardrail は構造化した述語を再構成し、明示的なローカル承認を得てから有効にする。

新しい Task の参照を置き換える場合は、旧ローカル ID・バージョンとの対応を保持する。保存済み Task・Dispatch が旧記録に依存している間は、発行済み Dispatch snapshot を書き換えたり、旧 database を削除したりしない。

共有ファイルの自動 import、保存先をまたぐ ID 移行、承認の引継ぎはない。`memory export` は、構造化 guardrail の述語と閾値を含まない。この手順はローカル履歴を保ち、レビューを求めるものであり、情報を失わない export・import 往復を保証するものではない。

## 出典と履歴

entry は、出典、バージョン、置換前の entry ID、作成・更新日時を保持する。出典は記録や外部の証拠を識別するもので、その証拠をコピーしたり独立検証したりはしない。複数の出典は `--source` を繰り返して記録する。

```sh
taxion memory put --kind decision --title "Use SQLite" \
  --source task:T-1 --source dispatch:D-1
taxion memory warn --body "Verify the target before dispatch" --source dispatch:D-1
```

警告は発生ごとに記録する。昇格候補は、その発生記録と指定された出典 ID を参照する。`warning:<id>` は `memory source --warning-id ID` で確認する。旧 database は警告の合計数だけを保持していたため、移行時は `legacy:aggregate-count-only` と記録し、個別の証拠を創作しない。

`memory revise --kind KIND --id ID --title TITLE --actor ACTOR --reason REASON` は、`proposed` の新しいバージョンを作り、前のバージョンを `superseded` にする。`--source` の省略時は、前の出典一覧を保つ。`memory revoke` は同じ kind、ID、actor、reason を受け取り、本文を削除せずに entry を無効にする。

承認済みの候補 lesson を改訂・失効させると、そこから作成した guardrail も失効する。新しい lesson からの guardrail を有効にするには、改めて承認が必要。`memory history --kind KIND --id ID` は履歴を表示する。検索は `superseded`・`revoked` を除き、`get`・`list` は監査のために保持した記録も返す。

現在の Memory 参照一覧は Task Graph が保持する。`memoryRefs` には `lesson:12`・`decision:4` などの標準形の ID を指定する。CLI・HTTP・ACP の Dispatch は各参照を解決し、欠落・失効・置換済みの entry と未承認 guardrail を拒否する。参照する entry には出典が必要。

Task Graph は ID を保存し、変更不可の Dispatch instruction snapshot は、選択した各 entry のバージョン、scope、status、title、body、出典を保存する。承認済み guardrail には、構造化述語と元の lesson ID も含める。そのため worker は、Dispatch 発行時に選択した Memory のバージョンを受け取る。

entry 間の矛盾は明示して記録する。Taxion は自由文から意味上の矛盾を推測しない。未解決の矛盾があれば、どちらかの entry を参照する Dispatch を拒否する。

```sh
taxion memory conflict add --left decision:4 --right lesson:12 \
  --actor human --reason "The entries prescribe different storage sources"
taxion memory conflict list --ref decision:4
taxion memory conflict resolve --id 1 --actor human \
  --reason "Decision 4 supersedes the lesson for this workspace"
```

矛盾の登録と解決は、両 entry の履歴に残す。解決には、記録した Human の説明が必要。

## 警告からの昇格

`memory warn` は同一の `(body, scope)` を数える。設定した閾値に達すると、指針候補として `proposed` の lesson を一件作る。既定の閾値は3回。`memory configure --promote-after N` で、選択 workspace に正の閾値を設定する。

閾値は `memory.db` に保存し、既存の counter を含む以後の警告に適用する。変更しても既存候補は削除しない。counter、候補、両者の対応は一つの transaction で保存する。新しい発生記録には、指定された場合に出典も保持する。

## 承認と Dispatch 前の検査

候補は明示して承認する。自由文を policy として解析しない。

```sh
taxion memory approve --id 1 --check repo-equals --value target-repo
taxion memory approve --id 2 --check task-title-matches --value '^fix:'
taxion memory checks
```

`--id` は `warn` が返す候補 lesson の ID であり、guardrail ID ではない。global 候補には `--scope global` が必要。承認は `approved` の guardrail を作り、lesson の status 変更と同じ transaction に述語を記録する。同一の承認を繰り返すと同じ guardrail を返し、矛盾する承認は失敗する。通常の lesson と `memory put --kind guardrail` は policy を有効にしない。

`repo-equals` は、Dispatch に指定した repo 文字列を `--value` と完全一致で比較する。path の解決やリポジトリの存在確認はしない。`task-title-matches` は保存済み Task title に Go の正規表現を適用し、Dispatch 時に指定した title は使わない。`^`・`$` がなければ部分一致。空の値、不正な正規表現、未知の述語は承認時に拒否する。

CLI・HTTP API・ACP は、非推奨の prompt 形式を含め、Dispatch 作成前に両 scope の承認済み述語をすべて評価する。全件一致が必要（MEM-001）。Web は API の判定を伝え、別の policy 評価処理は設けない。policy 不一致は HTTP 409、取得不能・形式不正は 500 を返す。どちらも Dispatch を書き込まない。

`memory.db` がない既存 workspace は従来の動作を保つ。存在する database が読めない場合に、検査を通知なく無効にすることはない。project・global は、選択した workspace database 内の entry を区別し、別々の repo database を指さない。承認済み検査は、その database を使うすべての Dispatch に適用する。

guardrail または承認済みの元 lesson を失効させると、検査は無効になる。元 lesson の改訂時も、新バージョンを Human が承認するまで旧検査を失効させる。Memory の読取りは SQLite workspace transaction と別に行うため、二つの database をまたぐ変更は atomic ではない。shell command、worker の出力、将来の Task 編集を制限する検査ではない。

## 検索

`memory search --query TEXT [--kind KIND] [--scope SCOPE]` は、entry の title と body を検索する。3文字以上の query は FTS5 trigram と BM25 ranking を使う。短い query と FTS に一致がない query は LIKE を使う。索引は SQLite の insert・delete trigger が更新し、呼出し元は別途更新しない。`superseded`・`revoked` の entry は監査用に保存するが、検索結果には含めない。

## export

`memory export [--scope SCOPE] [--sanitize=false]` はプレビューを標準出力に表示するだけで、データを送信しない。sanitizer は既定で有効。`--profile`・`--dry-run` flag はない。

既定の sanitizer は、title、body、出典内の、一致する秘密鍵 block、token パターン、email、IPv4 形式の文字列、home directory の path を隠す。両 scope で同じ規則を使う。規則に一致しない本文と他の field はそのまま出力するため、機密情報の完全な除去は保証しない。共有前にプレビューを確認する。

`--sanitize=false` は原文を `UNSANITIZED` header とともに表示する。export は entry の本文を含み、構造化述語や閾値は含まない。有効な policy は `memory checks` で確認する。

## 完全なローカル移行

`memory snapshot` は SQLite の一貫した複製を作り、
project/global 両方の本文・出典・版・適用範囲に加えて、
承認・失効・改訂・矛盾・警告履歴・構造化 guardrail・設定・検索索引を保持する。
`memory export` は共有前の本文プレビュー、snapshot は完全な移行用ファイルである。

```bash
taxion memory snapshot --output /private/migration/snapshot.db
taxion memory inspect-snapshot --file /private/migration/snapshot.db
taxion memory restore --file /private/migration/snapshot.db \
  --output /private/new-workspace/memory.db \
  --expected-hash sha256:<確認したhash> \
  --actor human --source human:approved-local-migration
```

snapshot と移行先は新しい絶対パスを指定する。
親ディレクトリは管理者が事前に用意する。
移行先には元の ID と参照関係をそのまま保持し、ローカル承認の receipt を追記する。
既存の移行先は保持されるため、利用中の Memory との切替は管理者が明示して行う。
別の workspace と統合する場合の ID 再割当ては、別途移行設計を必要とする。

レビュー出力は元の snapshot の hash、サイズ、各 table の件数、
sanitizer が検出した table・column・規則名・件数を表示する。
全 table の文字列・blob を既存規則で調べ、該当本文をレポートから省く。
検出がある場合は内容をローカルで確認し、
同じ hash と移行先に対して `--approve-sensitive` を明示して再実行する。
移行は原文を保持するため、sanitizer の検出結果はレビューの補助として使う。
検出範囲は前節のパターンに限り、社内情報や未知の秘密の判断は管理者が行う。

ファイルは権限 `0600`、作業ディレクトリは `0700` で保存する。
CLI はファイルをローカルへ作成し、Copilot や外部サービスへの送信を行う機能を持たない。
snapshot は 256 MiB までを扱い、live SQLite sidecar を伴う入力は一貫した snapshot の再作成を要求する。
本文・イベント・承認などが snapshot に含まれるため、送信先の利用許可は別途確認する。

## 開発機間の配布・集約

export と snapshot・restore は手動での共有・ローカル移行を扱う。開発機間の自動同期、別々の Memory の集約、競合解決は行わない。Dolt は将来の候補であり、その採用と配布・集約の実装計画は同じではない。共有する範囲と競合時の扱いは別途設計する必要がある。
