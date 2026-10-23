# Binary release の作成と検証

候補の実測結果と受入れ条件は [検証記録](binary-provenance-validation.md) に保存する。正式release完了とは区別する。

## 固定した入力

`VERSION` が次の正式 version を指定する。tag は `v` とその内容を結合した値に一致させる。GoReleaser は configuration schema 2 / v2.18.3、Go は1.27.2。CGO_ENABLED=0 で Linux / Darwin / Windows、amd64 / arm64 の6 binary を build する。SQLite は pure-Go driver を使用する。release job で `go mod tidy` を実行しない。

`.github/workflows/distribution.yml` は PR の snapshot archive を各 native runner に渡す。Linux arm64 / Windows arm64 / macOS Intel を他 architecture の実行結果で代用しない。`scripts/mise_smoke.py` は Go compiler を子 process の PATH から除く。snapshot では loopback URL を明示し、正式 release 後は `github:hiono/taxion` の公開 asset 自動選択を検査する。

snapshot と正式 release の build 前に `go mod download`、`go mod verify`、`git diff --exit-code -- go.mod go.sum` を実行する。依存 metadata の不足や変更を検出したら停止し、build job 内で source を修正しない。各 native smoke は pin の無効化、uninstall、同じ版の再導入と executable bytes の一致も検査する。

## 正式公開

docs / links の明示stepは、既存の `TestDocumentation*`、`TestConfigurationGuide*`、`TestUserGuide*` を実行する。Markdownのlocal pathとheading anchor、設定項目と利用手順の整合性が対象で、外部URLの可用性検査は含まない。これらはfull Go testsにも含まれるが、release log上でも検証範囲を確認できるよう別stepに固定する。

独立 model review の対象 HEAD と PR HEAD、CI、clean worktree を確認して merge する。その merged source の VERSION と一致する未使用 tag を push すると `release.yml` が起動する。既存 release がある tag は拒否し、asset の clobber は使用しない。

workflow は full Go tests / full race / vet / build / web / docs / links / hygiene を実行し、GoReleaser check と archive 生成を行う。最初は draft Release に保存し、6 native runner で archive、mise の明示URLによるfixture導入、product provenance Gate、別pathの同一binaryの拒否・store bytes不変を検証してから公開する。公開後にも全 assets を再取得して bytes/hash を照合し、各 native runner で実際の GitHub asset 自動選択を含む同じ検査を繰り返す。

公開後の検証が失敗した場合も release asset を差し替えない。workflow の失敗を記録してその版の運用導入を停止し、新しい修正版を作成する。`release-manifest.json` の CI run は当該 workflow の参照であり、manifest 生成時点で将来の最終成功を主張しない。利用側は run 全体の完了と成功を別途確認する。

manifest schema 2 は、全 archive と executable の hash、checksums hash、source revision、version/tag、GoReleaser の build timestamp、Go/GoReleaser version、workflow identity、CI URL、mise identifier、rollback を保持する。manifest 自体は `release-manifest.sha256` に記録する。archive 内 metadata から公開 manifest URL を参照できる。checksum と manifest は同一発行元なので、別途確認・承認した manifest pin が必要。

## 開発と互換性

source build は [Development mode](../userguide/binary-provenance.md#明示-policy-と初回設定) の隔離 store でのみ使用する。CLI の operation 開始前に immutable な明示 mode policy を要求する。既存 store の policy は自動補完しない。product policy の再pinや development への downgrade は自動実行しない。

provenance は協調的なアプリケーション制御であり、local administrator や検査を削除した旧/改造 binary から SQLite を隔離するものではない。外部 daemon、writer identity、OS 強制境界は対象外。

rollbackVersion は v0.7.8 だが、旧版の対応 binary がない5 platformは fail-closed の `NOT_AVAILABLE`。旧版には新 Gate がないため、binary-only rollback と保護された operation 継続の可否は分ける。runtime、store、approval、期限・予算の rollback は行わない。
