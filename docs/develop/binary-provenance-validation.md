# Binary distribution / provenance の検証記録

## 対象と境界

Issue [#143](https://github.com/hiono/taxion/issues/143)、PR [#144](https://github.com/hiono/taxion/pull/144)。正式 binary 配布と協調的な provenance 検証を対象とし、local administrator への防御や旧 binary に対する物理的な SQLite アクセス制御は受入れ条件に含めない。source build を product policy で拒否し、開発 fixture に明示 policy が必要となる変更を含む。

## 記録済みの結果

`bff5329a682aa78ef3531204c907add89d56b900` の [distribution run 39858607442](https://github.com/hiono/taxion/actions/runs/39858607442) で、実際の GoReleaser 6 archive と6 native runner の archive / compiler-free mise snapshot smoke が成功した。runner は Ubuntu 24.04 amd64 / arm64、macOS 15 Intel / arm64、Windows 2025 amd64 / Windows 11 arm64。別 architecture の cross-build を native 実行結果として数えていない。

同じ commit の local full Go tests は1648 PASS / 26 SKIP / 0 FAIL。live provider、外部書込み integration は明示的に無効化し、model session を使わず実行した。初回 full suite で発見した offline completion / help / explicit development setup の互換性問題を修正した。`--help` が flag value の場合は Gate を省略しない。

Linux amd64 の native mise fixture では実 install path `installs/github-hiono-taxion/VERSION/taxion` を確認した。Go compiler を PATH から除き、immutable v0.7.8 raw binary の固定 hash を照合して version を切り替え、新しい pin へ戻せた。この結果は binary switch のみであり、旧版に新 Gate があることや product operation を継続できることの証明ではない。

## 回帰テスト

`internal/provenance` は正式 identity の synthetic fixture、local / dirty / Go install identity、manifest / binary / checksum / archive改変、platform・version・revision・path・store不一致、symlink差替え、manifestの必須identity、tar / Windows ZIP の内容契約を検査する。synthetic fixture はnative platform smokeとして数えない。

`TestProvenanceCLIFailsBeforeStoreMutation` は実際にbuildしたCLIを別processで起動し、policyなし・local source buildのproduct policy登録を拒否する。DB / ledger を含むdirectory全bytesの反復不変、既存policyの上書き拒否、明示development modeでのread-only authorization previewを検査する。

`scripts/test_release.py` は6種類のarchive名・構成・checksum・manifest schema / build identity・manifest自己hashを検査する。source revision / tag が一致しないmetadataやchecksumの改変では生成を拒否する。

### 配布物撤回後の回帰検査

v0.7.15 の候補では、`TestExplicitUnavailableRollbackProvenance` が schema 3 の出自検証と理由の出力を確認する。理由の欠落・未知値、戻し先との併記、対象環境の欠落、旧版への適用、承認 pin の不一致は拒否する。schema 2 の既存検証も維持する。

`scripts/test_release.py` は、撤回後でも明示指定なしには配布情報を生成しないこと、対応する公開済み版があれば例外を拒否すること、API エラーを利用不能に読み替えないことを確認する。smoke の戻し先選択は全6環境で撤回済みファイルを取得せず、通常の戻し先では版・対象環境・hash とダウンロード失敗を検査する。これらの単体検査は、新しい正式配布ファイルの実行結果ではない。

## 正式 release の完了条件

上記は候補検証の履歴であり、最新HEADのreviewや正式release完了を主張しない。最新HEADのfull tests / full race / vet / build / web / docs / links / hygiene、native matrix、独立したCopilot自動レビューはPRに紐付く結果を照合する。

tag workflowでは、draft assetを6 native runnerへ渡してcompiler-free mise fixture、product provenanceの実binary受入れと別pathのcopy拒否、synthetic store不変を検証する。公開後にも公開bytes/hash、GitHub backendの実際のasset自動選択、同じproduct Gateを照合する。snapshotで公開Release自動選択や正式product Gateが成功したとは記録しない。

Windows shell validation は明示未対応。旧配布物の撤回後は v0.7.8 を戻し先に使わず、schema 3 の明示例外では全環境の戻し先を利用不能として記録する。未実行・未提供をPASSと扱わず、製品storeやruntimeの再開判断と分離する。
