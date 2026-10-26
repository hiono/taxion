# 20. 階層化した Memory と役割別 Session によるオーケストレーション

Date: 2026-10-10

## Status

Accepted

ADR-0003 の SDK-neutral な core、ADR-0005 の Go 先行、
ADR-0017 の Memory model、ADR-0019 の明示承認を維持する。
SDK の採用を任意の将来候補とする扱いは、この決定で置き換える。
SDK の対応確認は採用方針を実現するための検証であり、
非対応が判明した場合は blocker と代替案を記録して判断を求める。

Task の情報と Dispatch の変更規則は
[ADR-0021](0021-durable-task-graph-and-dispatch-change-control.md) に記録する。

## Context

役割別の開発支援セッションを手動で運用すると、指示と結果の受け渡しを
ユーザーが担う。会話の途中で目的、許可、保留作業、対象 SHA、以前の指摘を
失うと、ユーザーは同じ軌道修正を繰り返すことになる。
コピー操作を減らすには、次の担当へ目的、許可、保留作業、対象 SHA、
以前の指摘を渡す処理も必要になる。

Taxion の目的は、ユーザーとの会話で決まったこと、未完了の作業、
実行中の指示を保持し、独立した担当による実装とレビューを進めることである。
役割別 Session と prompt cache は受け渡しを支える。
Memory と Task Graph が判断・証拠を永続化し、Session は実行中の会話を保持する。

## Decision

### 階層化した Memory を実行の基盤にする

ADR-0017 の三層を基に、記録と適用の責任を分ける。

| 階層 | 責任 |
| --- | --- |
| log | 起きたことと元の証拠を保持する |
| lesson / decision | 原因、是正、判断、保留した懸念を出典付きで保存し、次の作業で参照する |
| guardrail | 明示承認された検証可能な条件を、適用範囲に従って実行前に検査する |

guideline 候補は抽出・承認中の状態として管理し、実行時の検査条件は guardrail に記録する。
guardrail は明示承認と検証可能な条件を持つ。繰り返しの指摘は候補の作成に使い、承認後に条件として適用する。
ADR は decision の出典として保存し、実装済みの検査はコードとテストで確認する。
project/global は階層とは独立した適用範囲とし、情報をその範囲ごとに管理する。
判断や規則の更新・失効を記録し、出典と利用した版を追跡できるようにする。

Task Graph と Dispatch は現在の実行状態、依存関係、権限を管理する。
Memory は判断や経験の根拠を保存・再利用する。
両者を ID と証拠で関連づけ、判断と実行状態の記録先を区別する。
承認済み scope、acceptance、利用者の判断、repository、baseline・対象 SHA、
指摘、是正結果、証拠から次の指示を構成する。
controller は必須情報の欠落、矛盾、古い結果を検出すると実行を止め、
不足項目と必要な確認を示す。次の指示は保存済みの判断と証拠から構成する。

### Go の Copilot SDK で役割別 Session を管理する

Orchestrator、Coder、Reviewer、Planner ごとに独立した Session を管理し、
役割固有の system prompt と履歴を保持する。
同じ役割の Session を継続利用し、安定した prompt と文脈の再利用を図る。
各 Session の会話履歴はその役割の実行に保持する。
役割間では必要な指示、仕様、証拠を選び、明示して渡す。

Go の Copilot SDK を Runtime adapter に接続し、core は SDK-neutral な型を使う。
Copilot SDK の Runtime adapter を主要な連携方式とし、tmux は手動運用に使う。
Copilot Auto がモデルを選択し、role ごとの prompt と tool 設定を維持する。
Planner の Session を扱える設計と、Planner を実際の進行に採用する判断は分ける。
Planner の起動条件と効果は後続の評価で決める。
Go の実行経路を維持した上で、Rust の導入は後続の評価で決める。

Session の分離と、提供側の prompt cache の分離・保持・命中は区別する。
Session の独立性は Taxion が管理し、cache の分離・保持・命中は提供側の指標で確認する。
新規・継続 Session の利用量と選択モデルを記録し、測定結果の範囲で比較する。
Session の終了、再作成、compaction 後も、必要な契約と判断は永続記録から復元する。

### controller が受け渡しと完了条件を検査する

Orchestrator は会話、調整、判断の確認を担当し、
Coder は許可された実装、Reviewer は独立した検証を担当する。
controller が Task・Dispatch・role・repository・SHA と結果を照合する。
完了判定には保存済み契約に一致する result と必要な evidence を要求する。
Reviewer の承認は対象 SHA に結び、SHA が変わった成果物は再レビューする。

利用者の質問や懸念は Task に記録し、Dispatch の変更は明示的な遷移として管理する。
変更、停止、再開、差し替えの関係を記録し、各結果を対象 Dispatch と照合する。
保留作業と独立した成果物には実行前に永続的な作業記録を用意する。
Task Graph は実行状態を、Memory は判断の根拠を保持する。
Issue・PR は作業と証拠を共有する外部記録として参照する。

仕様書は出典、版、利用可能な範囲を伴って渡す。
資料の閲覧権限と別サービスへの送信許可を分け、外部送信には明示承認を求める。
仕様検索の接続方式は公式の対応範囲と組織の許可を確認して決める。

### 軌道修正の保存と再利用を検証する

最初の到達条件は、実際の Copilot SDK と Git repository で、
会話から実装、独立レビュー、必要な修正と再レビュー、結果報告までを
指示・結果の手動コピーなしで通すことである。
Memory の基本的な保存・参照・指示への反映も、この実行経路に含める。
Memory の保存・参照・指示への反映は M10〜M12 で実装し、検索や tools 連携の前に検証する。

一度受けた軌道修正が次の計画、指示、レビューに反映されることも完了条件とする。
判断待ちの途中会話、保留作業、古い指摘の再送、Session の再作成、
controller の再起動後に永続記録から決定を復元し、既知の判断を次の計画に再利用することを確認する。
新たな判断が必要な場合と、既に決まったことの復元を区別する。
情報不足を検出して止まることは、無人で推測して続けることより優先する。

## Consequences

Memory は後付けの検索機能ではなく、実行契約とともに初期段階から実装する。
Session、永続記録、Git、外部の作業記録を照合する処理が必要になる。
実行環境の分離は権限を管理し、controller は Task・Dispatch の契約と結果を検査する。
この二つを role と履歴の分離に組み合わせ、権限、独立レビュー、情報保持を管理する。

JSON Ledger、Runtime、Memory store を拡張し、決定に定めた保存・実行・照合を実装する。
現在のコマンドと制約は [Memory reference](../../userguide/memory-model.md)、
現在の adapter は [Runtime contract](../runtime-contract.md) に記載する。
wire と保存形式を拡張する場合は、移行方法と互換性を定義する。
到達条件は実 SDK と Git repository を用いた検証結果で判定する。

実装は [M10〜M16 の計画](https://github.com/hiono/taxion/issues/46)で追跡する。
階層化した Memory の保存・参照は [#66](https://github.com/hiono/taxion/issues/66)、
指示への反映と同じ軌道修正の再要求を防ぐ検証は
[#67](https://github.com/hiono/taxion/issues/67) に分けて追跡する。
M10〜M12 に実行契約、階層化した Memory、役割別 Session、自動実装・独立レビューを含め、
M13 で復旧と外部記録、M14 で仕様検索・Memory 移行・tools 連携、
M15 で各クライアントを含む到達条件を検証する。
M16 の Planner 採用と Rust 導入の評価は、この到達条件とは別に扱う。
