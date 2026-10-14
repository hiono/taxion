# 4. DayN でなく段階名で Milestone を表す

Date: 2026-10-09

## Status

Accepted

## Context

Day1・Day2 などは1日での完成を前提とする prototype の方式だった。Taxion の段階には固定期間がなく、日番号は進度を誤解させ、公開計画を非公開の進め方に結び付ける。

## Decision

Milestone は Foundation、Core、Local、Adapter、Server、Platform、Future の段階名で表す。code、文書、GitHub Milestone に DayN の参照を使わない。

## Consequences

- roadmap は日程の約束でなく範囲の順序を示す。
- GitHub Milestone は段階 ID を保ち、必要に応じて説明 label を付ける（ADR-0007）。
- prototype の知見を取り込むときは旧 DayN 用語を言い換える。
