# ロードマップ

現在の構成は[アーキテクチャ](architecture.md)、公開済み機能は
[利用者ガイド](../userguide/user-guide.md)と[GitHub Releases](https://github.com/hiono/taxion/releases)が正本である。
このページは未達事項を選び、過去の開発段階へたどるために使う。
Issue の open 状態を「機能全体が未実装」、部分実装を「受入れ完了」とは読み替えない。

## 進めている課題

| 目的 | 追跡先 |
|---|---|
| 承認した外枠で柔軟に続け、検証・独立レビューまで到達する | [#196](https://github.com/hiono/taxion/issues/196)、[#189](https://github.com/hiono/taxion/issues/189)、[#201](https://github.com/hiono/taxion/issues/201) |
| 実行基盤と利用枠に応じた課金判断を一貫させる | [#234](https://github.com/hiono/taxion/issues/234) |
| バックエンドの診断と、実サービスでの受入れを確認する | [#231](https://github.com/hiono/taxion/issues/231)、[#216](https://github.com/hiono/taxion/issues/216) |
| 永続化・独立実行・認可・追跡など、ハーネスの到達条件を確認する | [#220](https://github.com/hiono/taxion/issues/220)と関連 Issue |
| 保存先の正本、機器間の継続、バックアップの復旧可能性を確認する | [#192](https://github.com/hiono/taxion/issues/192)、[#137](https://github.com/hiono/taxion/issues/137)、[#215](https://github.com/hiono/taxion/issues/215) |
| ゴール指向方針が判断品質と総費用を改善するか実測する | [#90](https://github.com/hiono/taxion/issues/90) |

優先するのは、具体的な利用を止めている問題である。
保存成果と既存の検証を使い、未達条件に必要な変更と確認へ絞る。
新しい仕組みや追加の実モデル比較は、目的・範囲・予算を決めてから実施する。
最新の状態は[未完了 Issue](https://github.com/hiono/taxion/issues?q=is%3Aissue%20is%3Aopen)で確認する。

## 次に進める順番

2026-11-03 の v0.7.21 公開後の順番である。
理由と変更条件は [ADR-0067](adr/0067-prioritize-target-bound-quality-evidence.md)に記録する。
これは実行順であり、未達条件を削除したり、外部実行を一括承認したりする表ではない。

| 順位 | Issue | 優先する理由・開始条件 |
|---|---|---|
| 1 | #221 | 全改善の完了判断に使う同一対象の品質証拠。直近で旧契約と品質ゲートの差が顕在化 |
| 2 | #231 | OpenCode の応答前停止と原因伝達。接続成功だけでは実作業を再開できない |
| 3 | #234 | 利用枠・課金判断を一貫させる。#231 の失敗原因を課金方式変更で迂回しない |
| 4 | #229 | 高リスク操作の承認と秘密の非露出。対応構成と仲介側の境界を先に確定 |
| 5 | #226 | 独立実行環境と被害封じ込め。#229 全体の完成待ちにせず境界試験を進める |
| 6–10 | #189 → #196 → #225 → #228 → #227 | 工程の提出・後続枠、承認内の継続、反復失敗の切替、再開、追跡を順に改善 |
| 11–14 | #222 → #223 → #224 → #191 | ループ・入力保持・並列委譲の受入れと、事前検査の残条件。既存実装を再実装しない |
| 15–19 | #192 → #216 → #90 → #215 → #64 | 保存先、SDK 追従、品質付き費用実測、復旧評価、計画担当の独立化。具体的な阻害があれば繰上げ |
| 条件待ち | #137、#63、#71、#75、#76、#77、#65、#205 | 必要な機器・組織権限・利用条件、または先行する評価を確定してから着手 |
| 親として追跡 | #220 | 子の個別証拠と未達条件を集約し、一括完了や相互の全件待ちを作らない |

現在の `priority: high` は #221 / #231 / #234 / #229 / #226 と親 #220、
それ以外の既存ラベルは維持する。同じ優先度内の順番はこの表で区別する。
実際の漏えい、未承認副作用、証拠喪失などが確認された場合は、安全上の阻害を先に扱う。

## 利用条件が揃ってから評価する課題

GitLab の追加実環境受入れは [#75](https://github.com/hiono/taxion/issues/75)、
Bitbucket は [#76](https://github.com/hiono/taxion/issues/76)、
M365 は [#77](https://github.com/hiono/taxion/issues/77)で追跡する。
契約と検証環境がない対象を、既存 CLI の完了条件へ暗黙に追加しない。
能力と確認範囲は[開発基盤の対応評価](forge-capabilities.md)を参照する。

複数利用者の権限・session 所有権は [#71](https://github.com/hiono/taxion/issues/71)、
Planner の独立化比較は [#64](https://github.com/hiono/taxion/issues/64)、
Rust 導入は [#65](https://github.com/hiono/taxion/issues/65)の評価対象である。
採択前の案を現行アーキテクチャとして説明しない。

## 過去の開発段階

Foundation・Core・Local・Adapter・Server・Platform は初期の開発順であり、
現在の機能を分類する構造ではない。M10〜M16 も当時の到達条件を追跡する識別子である。
完了時の対象・検証結果は[要件と到達条件](orchestration-requirements.md)、
ADR と実装の関係は[判断対応表](decision-traceability.md)から確認する。

初回設定の改善は [ADR-0039](adr/0039-prepare-project-configuration-with-explicit-repair.md)、
Memory と役割の分離は [ADR-0020](adr/0020-layered-memory-and-role-sessions-for-orchestration.md)、
project・開発基盤・実行契約の分離は [ADR-0023](adr/0023-independent-project-forge-runtime-and-configuration-contracts.md)、
ゴール指向の判断は [ADR-0040](adr/0040-constrain-goal-oriented-efficiency-by-quality-evidence.md)に記録している。
当時の「次版」の手順や完了済みの作業計画は、現在の操作手順へ重ねて掲載しない。
