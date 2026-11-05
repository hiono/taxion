# ADR-0071: 受理済み Coder 出力の差分基点と未開始 Reviewer を保持して継続する

## Context

通常 Flow の `git-diff-v1` は、元 baseline A から完全 tree T までの変更を受理する。
Coder の実行 commit B が A と異なる場合、Reviewer の実行場所は B を HEAD とするが、
照合する全変更パスの差分基点は A のままである。
この区別が持込み成果物にしかなく、通常 Coder 出力では B から T までの差分を
全変更パスと比較して準備を拒否していた。

準備失敗後の未開始 Reviewer は、既存の持込み成果物専用復旧には該当せず、
通常後続の承認も未取消の指示を理由に拒否する。
受理済み結果を再生成することや、DB を直接編集することは解決にしない。

## Decision

新しく発行する Reviewer 指示へ、既存の hash 付き target schema を用いた
`coder-review-target:` decision を保存する。A、B、T、全変更パス、repository、
成功 Coder の出典を結び付け、元結果と照合する。
Reviewer の HEAD と実 index は B、完全 tree は T、差分照合の基点は A を維持する。

既存の `reconcile-stopped` を、通常 Flow の受理済み Coder に続く未開始 Reviewer に限定して広げる。
静止証拠と配信・利用量の不確実性への承認、preview hash、受理 receipt と Git 証拠を確認して
Reviewer だけを取り消す。旧 Flow/HOLD、親の `WAITING_FOR_REVIEW`、成功結果、検証と履歴を保持する。
後続は既存の intake/confirm と `continueFromDispatchId` を用い、受理 receipt を後続承認と開始時にも再照合する。
Coder と受理済み検証は再実行せず、通常の Reviewer 結果受理ゲートは維持する。

レビューのみ、repair0 の後続の Coder 枠は0とする。
承認 JSON の任意 `deadline` は工程 hash に含めた固定上限であり、
`timeoutSeconds` はそれを短縮できるだけである。旧期限が切れた場合は、将来期限を別途明示承認する。

## Alternatives considered

全変更パスを Coder の今回申告へ縮める、Git 照合を省略する、旧指示を書き換える、
Coder を再実行する、通常 Flow を持込み成果物専用復旧へ混在させる案は採用しない。
既存の取消と成功 Coder 継承が使えるため、新しい復旧サブシステムは追加しない。

## Consequences

旧版の不変な Reviewer 指示は再生成しない。安全に取消後、新しい後続指示に正しい出典を発行する。
完全 target が維持できない場合、ACK・実行・session・receipt がある場合、
未記録の管理 worktree が残る場合は拒否する。人の静止申告は未送信・無課金の証明ではない。

## Security and operational impact

identity、ownership、設定承認、利用量、期限、品質ゲートは維持する。
秘密、session 識別子、製品コード、運用機の原本は公開しない。
取消後にも source receipt が失われた場合は後続を拒否する。

## Compatibility

DB schema の変更はない。既存の持込み成果物と実行済み Reviewer の復旧契約は維持する。
新しい取消・指示・期限を扱う store は旧バイナリで操作しない。戻す場合は、適用前の整合バックアップを別配置で使う。
操作例は [CLI reference](../../userguide/cli-reference.md#受理済み-coder-出力からのレビュー継続)に一本化する。

## Status

2026-11-05 時点の実装判断。合成再現と検証範囲は
[通常 Reviewer 復旧の確認記録](../normal-reviewer-handoff-validation.md)を参照する。
実サービスと運用機での受入れは未実施である。
