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
