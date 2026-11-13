# 2. code と履歴を移植せず clean-room で開発する

Date: 2026-10-09

## Status

Accepted

## Context

非公開 prototype で Task Graph・Dispatch・Evidence・独立レビューを検証した。その code、Git 履歴、Issue、識別子を移植すると、非公開の出典情報（名前・path・host）を漏らし、prototype 固有の前提も持ち込む。

## Decision

clean-room で開発し、code、履歴、Issue、名前、path、credential は移植しない。非公開情報と出典情報を除いた、一般化した要件、guardrail、手順、失敗知見だけを入力に使える。

## Consequences

- 公開 tree に非公開の出典情報を含めず、PRIV-001 を CI で強制できる。
- 開始には時間がかかる。識別 ID を付けた公開可能な仕様から、各動作を導き直す。
- repository と検証対象の混同など、prototype の失敗は修正 code をコピーせず、guardrail（DSP-001・SHA-001）として定義し直す。
