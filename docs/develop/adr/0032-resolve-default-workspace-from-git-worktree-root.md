# 32. 既定の workspace を Git 作業ツリールートから解決する

Date: 2026-10-15

## Status

Accepted

## Context

通常の利用者や coding agent が、DB の保存場所や内部ファイル名を依頼ごとに指定する必要はない。
保存先は Taxion が解決し、同じ作業の既存状態を継続して参照する必要がある。
従来の `.taxion/ledger.json` はカレントディレクトリに対する相対パスだったため、
同じ repository でもサブディレクトリから起動すると異なる workspace を選んでしまう。

また、`--store` は旧 ledger のパスを受け取り、実際の SQLite はその隣の
`workspace.db` に保存する。この内部事情を知らず DB 自体を指定すると、
作成した DB を旧 JSON として読み込もうとして失敗した。
誤指定を拒否するだけでは、通常操作に内部ファイルの知識を要求する問題は残る。

当初の候補 `$(git rev-parse --git-dir)/../.taxion/` は通常の repository では
プロジェクト直下を指すが、linked worktree では作業ツリールートを指さない。
Git 管理領域の配置ではなく、作業対象のルートを基準にする必要がある。

## Decision

Git repository 内で `--store` を省略した場合は、`git rev-parse --show-toplevel`
で解決した作業ツリールート配下の `.taxion/` を既定保存先とする。
プロジェクト直下とサブディレクトリの起動で同じ workspace を選ぶ。
linked worktree はそれぞれの作業ツリールートに独立した `.taxion/` を持つ。
`--git-dir` や `--git-common-dir` 配下への保存、全 worktree の暗黙共有は採用しない。
Git によるルート解決が失敗した場合は、別の場所へ黙って切り替えずエラーにする。

Git repository 外の standalone workspace は、互換性のため従来どおり
カレントディレクトリの `.taxion/` を使う。
設定ファイルの探索規則はこの判断では変更しない。
2026-10-17 の [ADR-0039](0039-prepare-project-configuration-with-explicit-repair.md) で
Project 設定も作業ツリールート基点へ変更した。旧サブディレクトリ設定は自動で移動しない。
静的な `ready`・help 等は workspace の探索や Git 起動を要求しない。

`--store` は隔離テスト・既存状態の指定などの例外用途に残す。
明示した相対パスは従来どおりカレントディレクトリ基準とし、既定保存先へ補正しない。
通常の案内は保存先の指定を前提にせず、DB 自体や同一ファイルの別名を指定する入力は
DB・directory の作成前に拒否する。

過去にサブディレクトリへ作られた状態の自動移動・統合は行わない。
その状態を使う場合は明示指定し、正式な作業状態や証拠を勝手に移さない。
別 worktree の agent が親の制御状態を操作する必要がある場合も、
親が解決した保存先を明示して引き継ぎ、子の既定 workspace と混同しない。

## Consequences

通常はプロジェクト内のどこから実行しても、保存先を編集せず同じ DB・記録を参照できる。
SQLite の保存先や旧 ledger の形式は変えず、既存のプロジェクト直下の状態を再利用する。
作業ツリー内の `.taxion/` は引き続き Git ignore 対象とする。

linked worktree の分離、サブディレクトリからの同一保存先、明示指定の維持、
Git 解決失敗時の非作成、旧 ledger の移行と誤指定時の既存 DB 保持を回帰で確認する。
ADR の採択だけではこれらの実装・検証完了や Release の受入れ達成を意味しない。
現在の操作契約は[利用者ガイド](../../userguide/user-guide.md#設定と実行前の確認)と
[CLI リファレンス](../../userguide/cli-reference.md#cli)に記載する。
