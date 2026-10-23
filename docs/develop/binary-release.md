# Binary release の作成と検証

候補の実測結果と受入れ条件は [検証記録](binary-provenance-validation.md) に保存する。正式release完了とは区別する。

## 固定した入力

`VERSION` が次の正式 version を指定する。tag は `v` とその内容を結合した値に一致させる。GoReleaser は configuration schema 2 / v2.18.3、Go は1.27.2。CGO_ENABLED=0 で Linux / Darwin / Windows、amd64 / arm64 の6 binary を build する。SQLite は pure-Go driver を使用する。release job で `go mod tidy` を実行しない。

`.github/workflows/distribution.yml` は PR の snapshot archive を各 native runner に渡す。Linux arm64 / Windows arm64 / macOS Intel を他 architecture の実行結果で代用しない。`scripts/mise_smoke.py` は Go compiler を子 process の PATH から除く。snapshot では loopback URL を明示し、正式 release 後は `github:hiono/taxion` の公開 asset 自動選択を検査する。

snapshot と正式 release の build 前に `go mod download`、`go mod verify`、`git diff --exit-code -- go.mod go.sum` を実行する。依存 metadata の不足や変更を検出したら停止し、build job 内で source を修正しない。各 native smoke は pin の無効化、uninstall、同じ版の再導入と executable bytes の一致も検査する。

## 正式公開

docs / links の明示stepは、既存の `TestDocumentation*`、`TestConfigurationGuide*`、`TestUserGuide*` を実行する。Markdownのlocal pathとheading anchor、設定項目と利用手順の整合性が対象で、外部URLの可用性検査は含まない。これらはfull Go testsにも含まれるが、release log上でも検証範囲を確認できるよう別stepに固定する。

独立 model review の対象 HEAD と PR HEAD、CI、clean worktree を確認して merge する。その merged source の VERSION と一致する未使用 tag を push すると `release.yml` が起動する。既存 release がある tag は拒否し、asset の clobber は使用しない。

正式公開の前に `python3 scripts/workspace_hygiene.py` で正本の作業場所を検査する。Git に登録された全作業場所の状態、branch、HEAD、merge 済みかどうか、切り離された HEAD、存在しない作業場所、所有者が不明な作業場所、既存の配布用生成物を列挙する。所有者の記録は、正確なパス、HEAD、所有者、判断の参照、用途、候補の稼働状態、保持または削除候補の区分を含む非公開の JSON として `--ownership` で指定できる。記録があるだけで削除や公開を許可しない。

正本以外の場所からのビルド、未保存の変更、不明な所有者、未整理の merge 済み作業場所、一時的な切り離し HEAD、孤立した作業場所、稼働中の候補、残存する生成物は正式公開を拒否する。検査は読取り専用であり、変更や証拠を自動削除しない。削除候補を整理する場合も、merge、PR、レビュー、配布結果からの参照と未保存成果の保護を確認し、未参照と確認できた具体的な対象だけを削除する。CI の単独 checkout はその隔離環境の正本とし、追加の一時 worktree がないことと `origin/main` に含まれるリビジョンであることを検証する。

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
