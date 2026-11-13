# 8. ADR に Nygard 形式を使う

Date: 2026-10-09

## Status

Accepted

## Context

`adrs` は Nygard と MADR 4.0.0 に対応する。MADR は decision-maker、consulted・informed、候補、pros・cons の節を加える。しかし Taxion の decision-maker は一人で、候補比較も少ないため、空の節が増えて記録の負担になる。

## Decision

全 ADR は Nygard 形式（`Status / Context / Decision / Consequences`）で書く。将来、本当に候補比較が必要な判断には、例外として単一の MADR 記録を使える（`adrs new --format madr`）。

## Consequences

- 判断記録の保守に必要な労力を減らす。
- decision-maker は専用項目で追跡せず、誰が決めたかは Git・Issue に記録する。
- 移行なしで記録ごとに形式を混在できる。
