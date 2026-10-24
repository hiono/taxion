# 正式バイナリと運用モード

## 配布単位と信頼する情報

製品運用では GitHub Release の binary archive を使う。対象は Linux、macOS、Windows の amd64 / arm64。Windows は zip、他は tar.gz で、`taxion` または `taxion.exe`、`LICENSE`、`release-info.json` を含む。archive と executable の hash は別の値である。

利用者が承認する trust root は、正式な GitHub Release から取得して別途照合した `release-manifest.json` の SHA-256 pin と operation policy。ローカルで作った manifest の hash を、そのまま正式 release の証明として採用しない。manifest URL、source revision、Git tag、workflow run の成功、GitHub asset digest と `release-manifest.sha256` を公開記録で確認する。付属 hash ファイルだけでは、manifest と hash ファイルの同時差替えを検出できない。

この機構は正式 binary の誤使用防止であり、OS のアクセス制御ではない。管理者が policy や source を変更して検査を削除する行為、検査を持たない旧 binary による SQLite への直接書込みは防御対象外。store 専用 daemon や別 OS identity は導入しない。

## mise の binary-only 導入

正式 release では [mise GitHub backend](https://mise.jdx.dev/dev-tools/backends/github.html) を使う。`go:` backend、`go install`、source checkout は product mode の導入に使わない。version の `v` prefix は mise が除く。archive 名は `taxion_VERSION_OS_ARCH.tar.gz`、Windows のみ `.zip` で、source archive は配布しない。

```sh
mise install github:hiono/taxion@0.7.9
mise use --pin github:hiono/taxion@0.7.9
mise exec -- taxion --version
mise which taxion
```

これは新しい配布 workflow 用の手順であり、release workflow の各 platform の `mise-smoke` 成功を確認するまで、その platform の導入を検証済みと扱わない。PR の snapshot smoke はローカル URL 指定の archive 展開検証で、公開 Release の自動 asset 選択検証とは区別する。失敗・未実行の platform は PASS にしない。

Go compiler を PATH から除いた native runner で version pin、archive bytes、実 binary の version を照合する。正式 release 後の native smoke は実際の GitHub backend の自動選択も検査する。全6 platform の成功前に operation store を変更しない。

## 明示 policy と初回設定

新 CLI は、store directory の `operation-policy.json` がない場合、operation の開始を拒否する。help、version、read-only doctor、Git の保護操作、offline completion は例外。保存状態を読む一部 command にも保守的に同じ Gate を適用する。環境変数で product policy を development に上書きしない。

`--store` は DB ではなく、同じ directory 内の legacy ledger path を指定する既存 option。directory を用意してから、`taxion provenance configure --file POLICY.json` で policy を初回登録する。登録は既存 policy を上書きしない。既存 store の自動移行や mode 推定はない。

Development mode は、隔離した開発 directory の realpath だけを明示する。

```json
{
  "schemaVersion": 1,
  "mode": "development",
  "storeDirectory": "/absolute/isolated-development/.taxion"
}
```

Product mode は、確認した正式 manifest の pin、取得した archive と checksum file、mise の data directory と実 binary path を明示する。以下の path と hash は例で、既定値や自動承認ではない。

```json
{
  "schemaVersion": 1,
  "mode": "product",
  "storeDirectory": "/absolute/operation/.taxion",
  "binaryPath": "/absolute/mise/installs/github-hiono-taxion/0.7.9/taxion",
  "miseDataDir": "/absolute/mise",
  "manifestPath": "/absolute/verified/release-manifest.json",
  "manifestSha256": "REPLACE_WITH_VERIFIED_MANIFEST_SHA256",
  "archivePath": "/absolute/verified/taxion_0.7.9_linux_amd64.tar.gz",
  "checksumsPath": "/absolute/verified/SHA256SUMS"
}
```

Windows は absolute native path と `taxion.exe` を使う。JSON の backslash は escape する。`mise which taxion` の出力と symlink 解決後の path を確認し、既定の install layout と一致させる。shim や alias の表示だけを承認根拠にしない。directory が symlink の場合は実体の directory を policy に記録する。

```sh
taxion provenance configure --file POLICY.json
taxion provenance verify
taxion init
```

product policy の登録自体にも正式 artifact の検証が必要。source build で先に product policy を登録して後から binary を切り替える手順は使わない。登録後も各 CLI invocation の dispatch 前に検証する。Web / ACP / HTTP の起動も Gate を通す。

## 拒否と復旧

Gate は executable の実 path / bytes、embedded version / revision / release channel、manifest pin、platform、archive hash / contents、checksums を照合する。dirty、local build、Go install、candidate、未知 revision、version 偽装、manifest / archive の変更、再pack、symlink / PATH 差替えは拒否する。version が同じでも hash が違えば拒否する。

拒否時の JSON は `code: provenance-rejected`、`stateChanged: false` を返す。DB を開く前に拒否するため、Task、Flow、Dispatch、approval、audit、deadline、budget は変更しない。`--help` を flag の値として渡しても Gate を省略しない。

pin 変更、version upgrade、mode 変更は既存 policy を自動上書きしない。全 runtime を停止し、旧 policy と DB を保護したうえで、operator が新しい正式 manifest と実 path を再承認する。既存 policy の退避・再登録は明示的な管理作業で、controller は黙って削除・再作成しない。

## rollback と制約

v0.7.9 の配布情報の rollbackVersion は v0.7.8。既存 release を変更しないため、v0.7.8 の binary があるのは linux-amd64 だけ。他5 platform は `NOT_AVAILABLE` とし、source build で穴埋めしない。初回対応 platform に存在しない rollback artifact を指定したら停止する。

v0.7.8 は新しい product provenance Gate を持たない。binary の切替を新 Gate の成功と扱わず、旧版への rollback 後は新 policy に基づく product operation を開始しない。deadline、budget、authorization、DB を巻き戻さない。

次の修正版では、6環境の正式ファイルがある v0.7.9 を戻し先にする。manifest の固定ハッシュと環境別の配布ファイルを照合し、各環境の検証を満たしてから利用する。v0.7.8 の対応範囲が増えるわけではない。旧版で新しい取消状態を解釈できない場合は、同じ保存先の操作を再開せず停止する。

v0.7.12 以降の配布情報では、rollbackVersion に直前の正式版の tag を記録する。tag が取得できない場合、正式版の形式でない場合、または直前の版が下書き・先行版・配布ファイル不足の場合は、配布情報を作成しない。v0.7.10 と v0.7.11 の配布情報には v0.7.9 が記録されているが、公開済みの配布物は変更しない。v0.7.11 から戻す場合は、配布説明文に記載した v0.7.10 を使用する。

| Platform | v0.7.8 への binary rollback | 同じ正式版の再導入・無効化・削除 |
| --- | --- | --- |
| linux-amd64 | 既存の単体 binary と公開 checksum の照合後に切替可能。新 Gate に基づく operation は停止する。 | 固定した archive から再導入できる。設定解除と uninstall は下記手順。 |
| linux-arm64 / darwin-amd64 / darwin-arm64 / windows-amd64 / windows-arm64 | 対応 artifact がないため不可。別 platform や source build で代替しない。 | 各 platform の固定 archive から再導入できる。設定解除と uninstall は下記手順。 |

無効化は、稼働中の runtime を停止したうえで、対象 project / global の mise 設定から Taxion の項目を外す。binary を削除する場合は `mise uninstall github:hiono/taxion@0.7.9` を使う。この操作は version pin の設定を削除しないため、再導入時は pin を確認して `mise install` を実行する。既存の store と承認を削除したり、別の版へ暗黙に切り替えたりしない。複数の設定 layer がある環境では `mise which taxion` で選択結果も確認する。

Windows archive は導入・identity / provenance の検証対象だが、controller の shell validation は Unix process-group cancellation を要求するため未対応で明示拒否する。Unix と同じ安全な process-tree 停止を実装するまで Windows の workflow 実行をサポート済みと扱わない。native archive smoke は workflow 実行の証明ではない。
