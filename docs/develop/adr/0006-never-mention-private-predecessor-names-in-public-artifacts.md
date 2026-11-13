# 6. 非公開の先行 project 名を公開 artifact に含めない

Date: 2026-10-09

## Status

Accepted

## Context

prototype は非公開である。README、文書、ADR、Issue、commit にその名前や repository・host・人名を記すと、存在を明かし、出典との関連付けを可能にする。

## Decision

公開 artifact に非公開の先行 project・repository・host・人名を含めない。`private predecessor`・`implementation-repo`・`validation-target` の一般的な役割だけを使う。`docs/develop/privacy-provenance.md` に記録し、hygiene CI で秘密の形式を検査する。

## Consequences

- 取り込む知見を merge する前に、非公開情報を除いたか確認する。
- 失敗知見は一般化して書き直す（ADR-0002）。
- Issue・ADR の表現をより厳密に扱う。
