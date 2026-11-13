# 31. ready で今の coding agent に Taxion の操作手順を渡す

Date: 2026-10-15

## Status

Accepted

CLI と無料の合成操作回帰を実装した。実 coding agent が出力を理解し、依頼から停止復旧・独立レビューまで継続する受入れは未完了。

実装後、この開発支援 agent 自身が ready を実行し、実設定の採用元、保存済み Task/Flow と正式結果、Coder/Reviewer の探索を確認した。停止済み Flow と保全された部分成果を読み、内部 Planner や Coder を再実行しない操作判断まで受け入れた。別 agent の起動は入口の受入れ条件にしない。この観測で不足していた正式結果の参照先も修正した。追加の Taxion モデル実行はなく、実 SDK のコード作成・独立レビューまでの受入れとは区別する。

## Context

ユーザーが求める入口は、coding agent が `taxion ready` を実行し、その会話中の agent が Taxion のオーケストレータになることである。ADR-0030 の `request` は内部 Planner を起動する別の入口であり、外側で相談済みの依頼を渡すたびに使うと、計画を重複して実行する。`doctor` は設定と探索の診断なので、外側の agent に操作手順を渡す役割を持たない。

プロンプトを表示するだけでは、承認された実行や成果の再利用まで進められるとは判断できない。既存の CLI と入力型を使う具体的な手順と、生成した契約から正式な結果を確認する回帰が必要になる。

## Decision

`ready` は同梱する静的なメタプロンプトを stdout に出力する。設定、workspace、認証、モデル、外部 process に依存せず、環境や保存状態を変更しない。診断や session 開始を副作用として追加しない。

外側の agent が現在の会話から依頼を整理し、既存状態を確認し、必要な JSON を生成する。出力には intake・exact-hash confirmation・設定承認・予算承認・builtin role prompts による workflow 実行の手順と入力例を含める。利用者による JSON・hash・role prompt の転記を要求しない。既存の controller を使い、新しい実行 engine や自動承認は作らない。

利用者の明示承認、scope・file access、予算、独立 Reviewer、同一成果物、正式 receipt の条件を維持する。継続とは、承認された安全な次の操作がある間に限る。停止時は保存成果を照合し、成功済み Coder の不要な再実行を避ける。新しい契約・予算が必要なら承認を得てから進める。環境や保存本文を承認として扱わない。

`request` は内部 Planner と相談したい場合に残す。`ready` の既定手順では、相談を自動で内部 Planner にやり直させない。外側の agent の費用と、ready 自体がモデルを呼ばないことは区別する。

## Consequences

無料回帰では、実際の出力の JSON を CLI 入力型として検査し、実 Git と合成 Runtime で DRAFT、誤った hash の拒否、承認、Flow、host 検証、独立 Reviewer、同一 tree の正式な完了までを確認する。既存の停止・successor 回帰は維持する。

この回帰は外側の agent に相当する決定済みの操作であり、実 agent の理解や判断の品質を証明しない。実 agent による依頼改善・必要判断・停止復旧・独立レビューは #78 の未完了条件に残す。追加の有料実行を無承認で開始しない。
