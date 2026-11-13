# 70. 固定 MCP 承認による認証付き操作の仲介

Date: 2026-11-03

## Status

Accepted

## Context

Issue #229 は、高リスク操作の承認とモデルからの認証情報の分離を要求する。
既存の `taxion.tools.v1` は Task・役割・実行ファイル・入力・引数を固定し、
各呼出しを Controller で再照合するが、認証付き操作の一回限りの実行と
秘密を含み得る応答の取扱いを定義していなかった。
既存の信頼された MCP を仲介側として使い、新たな HTTP client や汎用 broker API は作らない。

## Decision

MCP binding の `credentialRef`、`operationId`、`expiresAt` を一組として追加する。
`credentialRef` は `env:TAXION_BROKER_` に大文字の識別名を続けた専用参照だけを受け付ける。
秘密そのものは Task・引数・設定・承認へ書かず、信頼された host が呼出し直前に取得する。
専用接頭辞の環境変数は、broker を使わない役割も含めて Copilot 子プロセスから除外する。
この参照をモデル runtime 自身の認証に流用することも拒否する。

認証付き MCP は、他の tool・skill・plugin・管理操作を持たない Copilot の役割でのみ接続する。
通常探索と file hooks を無効にする既存の固定方式を再利用し、
file・shell・未承認 tool の permission も拒否する。
稼働中 session と Controller の現在の承認を照合してから、正確に承認した request を渡す。
外部本文や skill の指示は、新しい承認や引数変更の根拠にならない。

専用環境変数の値は信頼された MCP プロセスだけに渡す。
MCP の応答・stdout・stderr は、秘密を含む可能性があるためモデルにも保存記録にも渡さない。
成功時は固定の完了通知、失敗時は内容を伏せた不明・失敗通知を返す。
通常の認証なし MCP の原本保持は変更しない。

起動前に、専用証拠 directory 内へ `operationId` をキーとする排他的な試行記録を作り、
ファイルと directory の同期を終える。記録には操作 ID・承認内容の hash・不明状態だけを保持する。
同期できない filesystem ではプロセスを起動しない。
一度記録した操作は、失敗・取消・期限切れ・接続断・再接続後も再利用しない。
期限と既存の15秒制限の早い方を適用するが、外部で生じた副作用の取消までは保証しない。
取消や結果不明は「未実行」を意味しない。再実行には外部状態の確認と別の明示承認が必要となる。

## Compatibility and limitations

この経路は既存承認の opt-in 拡張であり、旧承認を移行しない。
Copilot の session hash に環境変数分離方式を含めるため、
旧 session を新しい契約で再接続せず、正式な継続判断を使う。取消専用接続は維持する。

MCP 実装・入力・host は引き続き信頼対象である。
専用環境変数の分離と permission は OS sandbox ではなく、
同一 UID の悪意ある process、host の侵害、MCP 自身の秘密送信を防がない。
秘密をモデルの worktree・設定・skill・入力ファイルへ複製してはならない。
Copilot 自身の SDK 認証や HOME の完全隔離を実現したとは扱わない。
OpenCode は実行型 MCP が未対応であり、この認証付き経路も実行前に拒否する。
OS 隔離・別 UID・runtime 認証の仲介・組織アプリ接続は別の受入れを必要とする。
これらを含む Issue #229 全体は未完了として保持する。

## Validation and recovery

合成 canary と fake service でメール・支払い・削除の正確な request、
引数変更の拒否、一回限りの実行、並行呼出し、取消・期限切れ・結果不明を検証する。
canary を故意に stdout・stderr・応答へ含め、モデル返却値と保存証拠に含まれないことを確認する。
SDK の環境、create/resume の探索抑止、file・shell 拒否、承認取消も合成確認する。
実 credential、実メール・支払い・削除、追加モデルは使わない。
試行記録を削除して再送することを復旧手順にしない。
旧版への rollback は新しい認証付き承認を未対応として扱い、試行記録を保持する。

## References

- [既存の固定 tools 承認](0025-approved-local-specification-search-and-tools.md)
- [認証付き操作の利用手順と信頼境界](../../userguide/credential-broker.md)
- Issue #229、#226、#71、#77
