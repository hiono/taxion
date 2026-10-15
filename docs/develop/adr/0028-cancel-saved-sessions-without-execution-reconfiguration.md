# 28. 保存 session の停止専用接続と結果履歴の保持

Date: 2026-10-14

## Status

Accepted

## Context

期限切れの dogfood Flow は停止していたが、Coder Dispatch は `SUSPENDED` のままだった。
従来の CLI は停止にも最新版の execution contract を要求し、元の prompt・tool・schema から
session hash を再生成していた。版更新や prompt 変更により、新しい実行を許可していない
旧 session まで停止できなくなった。

停止専用接続を適用すると、SDK の取消・切断は成功したが、結果保存で別の不具合が判明した。
既存の suspension と新しい cancellation の receipt が同じ識別子になり、
SQLite の transaction が重複を拒否した。取消要求の成功と永続記録の成功は別の確認事項である。

設定承認にも費用前の確認不足があった。生成される Reviewer Task の承認漏れは、
Coder の有料 turn が終わってから検出されていた。

## Decision

CLI の safe stop は、保存 session ID、role、worktree、configuration hash、
選択済み credential reference に結び付いた停止専用 adapter を使う。
一般の execution adapter の hash を上書きする方式や、通常実行の照合を緩める方式は採らない。
旧 execution contract でも停止を許可するが、Start、Send、通常の Resume は拒否する。

SDK attachment では pending work の続行、resume event、transcript の修復、
設定探索と tools を無効にし、すべての permission request を拒否する。
この process に busy-state の観測がなくても、abort を要求してから切断する。
接続・取消・保存に失敗した場合は error を返し、停止済みの結果を捏造しない。

同じ Dispatch の結果 receipt は sequence を付けて追記する。
元の `SUSPENDED` receipt は消さず、最新結果との一致、履歴の順序、
過去の suspension の識別子・内容 hash・利用量を照合する。
旧 sequence 0 の receipt はそのまま読み出せる。

`workflow run|step` は session 作成前に、現在の Task と `maxRepairs` までの
残りの Reviewer Task に対する設定承認を確認する。
この確認は設定 binding や実行許可ではなく、各 role 開始時の照合と認証も維持する。

## Consequences

停止のために元の prompt や tool 設定を再現したり、有料の指示を送ったりする必要がなくなる。
credential の欠落や破損した transcript は依然として error となり、代替 credential や
履歴修復を暗黙に選ばない。

旧 version 4 の保存 Dispatch に停止を実適用し、`CANCELLED`、部分 Git 証拠、
元の suspension と cancellation の両 receipt の永続化を確認した。
成果物、旧 Flow の契約・予算、記録済み usage、SDK event 数は変わらず、追加のモデル指示は発行していない。
この取消の確認は、新方式の実 SDK coding・独立 review の受入れとは区別する。

回帰は `TestStopOnlySDKProtocolNeverCreatesOrSends`、
`TestSuspendedResultCanBeCancelledWithoutLosingReceiptHistory`、
`TestReviewFlowUsesUnchangedExplicitMultiTaskConfigurationApproval` に置く。
現行の操作は [CLI リファレンス](../../userguide/cli-reference.md) と
[利用者ガイド](../../userguide/user-guide.md)、保存・照合条件は
[実行記録](../../userguide/execution-records.md) が所有する。
