# Binary release の作成と検証

候補の実測結果と受入れ条件は [検証記録](binary-provenance-validation.md) に保存する。正式release完了とは区別する。

## 固定した入力

`VERSION` が次の正式 version を指定する。tag は `v` とその内容を結合した値に一致させる。GoReleaser は configuration schema 2 / v2.18.3、Go は1.27.2。CGO_ENABLED=0 で Linux / Darwin / Windows、amd64 / arm64 の6 binary を build する。SQLite は pure-Go driver を使用する。release job で `go mod tidy` を実行しない。

`.github/workflows/distribution.yml` は PR の snapshot archive を各 native runner に渡す。Linux arm64 / Windows arm64 / macOS Intel を他 architecture の実行結果で代用しない。`scripts/mise_smoke.py` は Go compiler を子 process の PATH から除く。snapshot では loopback URL を明示し、正式 release 後は `github:hiono/taxion` の公開 asset 自動選択を検査する。

snapshot と正式 release の build 前に `go mod download`、`go mod verify`、`git diff --exit-code -- go.mod go.sum` を実行する。依存 metadata の不足や変更を検出したら停止し、build job 内で source を修正しない。各 native smoke は pin の無効化、uninstall、同じ版の再導入と executable bytes の一致も検査する。

## 正式公開

docs / links の明示stepは、既存の `TestDocumentation*`、`TestConfigurationGuide*`、`TestUserGuide*` を実行する。Markdownのlocal pathとheading anchor、設定項目と利用手順の整合性が対象で、外部URLの可用性検査は含まない。これらはfull Go testsにも含まれるが、release log上でも検証範囲を確認できるよう別stepに固定する。

独立 model review の対象 HEAD と PR HEAD、CI、clean worktree を確認して merge する。その merged source の VERSION と一致する未使用 tag を push すると `release.yml` が起動する。既存 release がある tag は拒否し、asset の clobber は使用しない。

正式公開の前に `python3 scripts/workspace_hygiene.py` で正本の作業場所を検査する。Git に登録された全作業場所の状態、branch、HEAD、merge 済みかどうか、正本とのコミット差分、名前付き参照からの到達可能性、レビューと配布証拠からの参照、未保存変更の保全を列挙する。merge 済みの作業場所や切り離された HEAD の存在だけでは公開を停止しない。

各作業場所を `ACTIVE`、`PRESERVED_EVIDENCE`、`SAFE_TO_DELETE`、`REQUIRES_OWNER_DECISION` に分類する。正本と、正確な HEAD・所有者・稼働状態を明示した作業場所は `ACTIVE` として保持する。保全した内容が現在の状態と完全一致する場合、またはレビューや配布の証拠に結び付く変更のない作業場所は `PRESERVED_EVIDENCE` として保持する。未保存変更がある場合は、証拠の参照だけでは保全済みと扱わず、ステージ済み・未ステージ・未追跡ファイルを含むスナップショットの一致を必要とする。

変更がなく、merge 済みで、コミットが名前付き参照から到達可能で、確認範囲を明示した完全な参照一覧に関連証拠がない作業場所だけを `SAFE_TO_DELETE` とし、自動削除候補に列挙する。候補の列挙は削除の実行ではない。参照範囲が不完全な場合は未参照と断定しない。残りは個別の理由を付けて `REQUIRES_OWNER_DECISION` とし、補助作業場所に関する公開停止対象はこの区分だけに限定する。

`--evidence` は `records` と `snapshots` を含む非公開 JSON を指定する。`records` の各要素は `kind`（`review` または `release`）、証拠ファイルの `path`、`sha256` を持ち、その内容の正確な作業場所またはリビジョンへの参照を検査する。`snapshots` は作業場所の絶対パスから、保全用の `metadata.json`、`status.txt`、`staged.patch`、`unstaged.patch`、`untracked.tar` のあるディレクトリへの対応表とする。ハッシュと現在の変更内容、未追跡ファイルのバイト列またはシンボリックリンクを照合する。`referenceInventoryComplete: true` は列挙対象の全証拠を確認した明示的な宣言であり、検査結果から自動生成しない。省略時に未参照を主張しない。

明示指定がない場合、共通 Git ディレクトリの `release-workspace-evidence.json` があれば同じ照合を行い、直接検査と正式配布の準備処理に適用する。`--ownership` では正確なパスをキーに HEAD、所有者、`activeReleaseCandidate: true` を指定できる。非正本の作業場所を稼働中と分類しても、そこからの正式ビルドは許可しない。検査は読取り専用であり、変更や証拠を自動削除しない。

正本以外の場所からのビルド、正本の未保存変更や未mergeのリビジョン、残存する生成物は、補助作業場所の分類とは別に正式公開を拒否する。結果ではリリース元の曖昧さと生成物の残存を区別する。CI の単独 checkout はその隔離環境の正本とし、`origin/main` に含まれるリビジョンであることを検証する。

版が未公開であることは API の HTTP 404 で確認する。権限不足や通信障害を未公開と見なさない。固定した GoReleaser が実際に生成した版番号・リビジョンを CI で照合し、正式配布では tag も照合する。公開後のバイト比較は、6個の配布ファイル、チェックサム、配布情報とその自己ハッシュの9ファイルに限定する。ビルド用の内部メタデータは公開物に含めない。

workflow は full Go tests / full race / vet / build / web / docs / links / hygiene を実行し、GoReleaser check と archive 生成を行う。最初は draft Release に保存し、6 native runner で archive、mise の明示URLによるfixture導入、product provenance Gate、別pathの同一binaryの拒否・store bytes不変を検証してから公開する。公開後にも全 assets を再取得して bytes/hash を照合し、各 native runner で実際の GitHub asset 自動選択を含む同じ検査を繰り返す。

GoReleaser は `--skip=publish` で実行する。Release 本文は `.goreleaser.yaml` の静的 header だけを使用し、自動 changelog や footer は認めない。`scripts/publictext -release TAG` が Taxion の Communication Profile と保護対象一覧で正確な title / body を検査してから、`gh release create --draft --verify-tag --notes-file` で保存する。正式公開の直前にも共通検査を行い、draft の tag・title・body が検査対象と完全一致することを再取得で確認する。設定の欠落や内容の変更を無視して公開しない。

公開後の検証が失敗した場合も release asset を差し替えない。workflow の失敗を記録してその版の運用導入を停止し、新しい修正版を作成する。`release-manifest.json` の CI run は当該 workflow の参照であり、manifest 生成時点で将来の最終成功を主張しない。利用側は run 全体の完了と成功を別途確認する。

manifest schema 2 は、全 archive と executable の hash、checksums hash、source revision、version/tag、GoReleaser の build timestamp、Go/GoReleaser version、workflow identity、CI URL、mise identifier、rollback を保持する。manifest 自体は `release-manifest.sha256` に記録する。archive 内 metadata から公開 manifest URL を参照できる。checksum と manifest は同一発行元なので、別途確認・承認した manifest pin が必要。

## 開発と互換性

source build は [Development mode](../userguide/binary-provenance.md#明示-policy-と初回設定) の隔離 store でのみ使用する。CLI の operation 開始前に immutable な明示 mode policy を要求する。既存 store の policy は自動補完しない。product policy の再pinや development への downgrade は自動実行しない。

provenance は協調的なアプリケーション制御であり、local administrator や検査を削除した旧/改造 binary から SQLite を隔離するものではない。外部 daemon、writer identity、OS 強制境界は対象外。

v0.7.9 の配布情報は、戻し先を v0.7.8、対応環境を `linux-amd64` に限定している。この環境の実行ファイル切替は v0.7.9 の検証対象である。`linux-arm64`、`darwin-amd64`、`darwin-arm64`、`windows-amd64`、`windows-arm64` には v0.7.8 の配布ファイルがなく、同版への切替は利用できない。ソースからのビルドや別環境の配布ファイルで代替しない。

次の修正版の配布情報は v0.7.9 の6環境を戻し先とし、その正式 manifest の固定ハッシュと環境別ファイルを使用する。配布前の各環境の検証で、その環境の切替と元の固定版への復帰を確認する。検証前の結果を成功としない。戻し先がない場合は停止する。

戻し先がない環境で確認済みの復旧手段は、v0.7.9 の検証済み固定版の再導入、選択設定からの削除による無効化、アンインストールである。版の固定だけを外して未知の版を自動選択する手順は推奨しない。binary-only の切替は保護された操作の継続を許可せず、runtime、store、approval、期限・予算の rollback は行わない。旧版が新しい保存形式を読めない場合も、保存データを改変して回避しない。
