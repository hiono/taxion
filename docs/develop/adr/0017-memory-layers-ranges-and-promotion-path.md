# 17. Memory の層・範囲・昇格経路を決める

Date: 2026-10-10

## Status

Accepted

## Context

Issue #27（M6 Memory）。2026-10-09 の設計対話で、log を後続 Task に反映する（[127]）、繰返し警告を guideline・guardrail へ昇格する（[127]）、失敗・lesson・guardrail を Issue・MR で共有する（[155]）要件を特定した。ADR を置く層（[129]）、global・project の分担（[131]）、配置（[141] の project `.taxion/`、[143] の global `.config` 型）は未決だった。

## Decision

Memory は3層と2範囲にする。

```
log (raw: what happened)
  → lesson / decision (distilled: failures→lessons, judgments→ADRs)
    → guardrail (enforced: checked at dispatch time)
```

- ADR は lesson・decision 層に置く。整理済みで版を持ち、生 log でも強制検査でもない（[129]）。
- project Memory は `.taxion/` 内に限定する（[141]）。global Memory は `.config` 型とし（[143]）、repository でなく操作者に付随する。
- 昇格（[127][155]）は、設定可能な N 回目の繰返し警告から guideline 候補を作り、承認済み guideline を Dispatch 時に DSP-001 などとともに検査する guardrail にする。非公開の知見は Issue・MR（[155]）と独立した配置案（[157]）で共有する。

次の表は予定する設計で、現在の store ではない。

| 層 | 内容 | 例 | 予定する強制 |
| --- | --- | --- | --- |
| log | 起きたことの生記録 | task・dispatch・evidence row、audit JSONL | なし。追記専用 |
| lesson / decision | 整理した知見 | 失敗 lesson、ADR | 版を付けて参照 |
| guardrail | 通過が必要な検査 | DSP-001、SHA-001、昇格 guideline | Dispatch 時 |

| 範囲 | 予定する配置 | 関連先 | 内容 |
| --- | --- | --- | --- |
| project | `.taxion/` | repository | Task Memory、repo-local guideline |
| global | `.config` 型 | 操作者 | repository をまたぐ lesson、個人ルール |

## Consequences

- M6-2 は project 範囲の store を実装する。初期は SQLite を使う。
- M6-3 は counter・proposal・guardrail の昇格を実装する。
- M6-4 は検索方式を評価する（SQLite FTS・dolt・QMD 組込み）。
- sanitizer・export による自己保護は M6 対象外とし、M7 以降へ延期する。
- M6 の計画では費用制御を M8 候補とし、proc 経由の実 agent 接続を M3 の残件とした。計画時の背景で、現在の実装一覧ではない。

## 実装状況

上記は予定した設計を記録する。初期 M6-3 は固定3回の警告集計と guideline 候補作成だけで、閾値設定と強制 guardrail への昇格は未実装だった。CLI は project・global 別 directory でなく、同じ `memory.db` に両 scope を保存する。現在の動作は [Memory](../../userguide/08-memory-model.md)、実装結果は Issue #28–#34 に記録する。

閾値設定、明示承認、Dispatch 検査を完成させるため M6-3 を再開した。採択した述語の範囲は [ADR-0019](0019-structured-memory-approval-and-dispatch-checks.md)、現在の操作と制約は Memory 文書に記載する。上記の初期3回実装は過去の記録である。

M10 の Issue #66 は SQLite Memory に出典参照、版付き改訂、失効、lifecycle event を追加する。Task Graph の `memoryRefs` は Memory ID を参照し、Dispatch は欠落・置換済み・失効済み・未承認 guardrail の参照を拒否する。project・global は workspace の隣の同じ `memory.db` 内の scope のままで、保存先を分けない。旧 warning counter は個別証拠を保持しなかったため、移行は集計の出典を label で示し、各発生履歴を再構成しない。無関係 entry 間の conflict は Human が明示記録する関係とし、未解決なら両 entry の Dispatch 参照を止める。
