# 37. GitHub Release にリリースノートを保存する

Date: 2026-10-16

## Status

Accepted

## Context

GitHub Release は各 version の本文を保存し、タグと対象 commit に結び付ける。
同じ本文を repository に version ごとの Markdown ファイルとして追加すると、
公開履歴が二重になり、Release ごとに管理ファイルも増える。固定名の文書を
repository に置く方法も、GitHub Release の本文と内容が重複する。

リリースノートには公開 version の機能と利用上の制約を記載する。受入れの進捗、
未完了の checkbox、判断根拠は利用者向けの公開説明ではなく、対応する Issue に
記録する。導入・更新・削除の操作手順は利用者ガイドに置く。

## Decision

リリースノートは GitHub Release の本文だけで管理し、repository に版付きまたは
固定名のノートファイルを追加しない。Release 作成時はレビュー済み本文を
repository 外の一時ファイルから `gh release create --verify-tag --notes-file` で
送る。公開後の本文は Release object を正本として再取得・照合する。

本文は対象 version の機能と現行の利用制約に限る。将来の作業、受入れの進捗、
未完了条件と例外判断は Issue に記録し、Release note に含めない。利用者向けの
導入・更新・削除手順は利用者ガイドで管理する。

## Consequences

GitHub Releases が version ごとの本文と履歴を保持するため、repository 内の
release-note ファイルが増えない。Release 本文の再取得で公開内容を確認し、
受入れ状態は対応する Issue で追跡する。

v0.1.0 の公開 tag は既に既存候補 commit を指し、その tree には当時の
versioned release-note ファイルが含まれる。公開済み tag はこの判断だけを理由に
移動せず、現在の main からそのファイルを削除する。

## Alternatives

version ごとの repository ファイルは GitHub Release の本文と履歴を重複させ、
公開ごとにファイルを増やすため採用しない。固定名の repository ファイルも
Release 本文と内容が重複し、過去版の本文を別途保存する必要があるため採用しない。
