# ADR-0047: Coder に worktree 内の shell を限定して許可する

## Context

Copilot adapter の permission handler は、Coder の shell request を常に拒否していた。一方、`--coder-tools` には `bash` を指定でき、モデルには shell tool が見える。そのため Coder は build や test を呼べても実行できず、Controller 検証の失敗に対して、手元で確かめずに修正を繰り返していた。TKB3 の Task では、この状態で repair が r7 まで続いた（#165）。

## Decision

Coder の shell request は、次の条件をすべて満たす場合に限り一回ずつ承認する。

- 承認済みの Coder tool allowlist に `bash` または `powershell` が含まれる。
- sandbox bypass と managed approval を要求していない。
- `possibleUrls` が空である。
- 作業ディレクトリと `possiblePaths` が、すべて割当 worktree の内側に解決され、`.git` を含まない。
- SDK がコマンドを解析でき、`commands` の各 identifier が `commandSegments` にも現れる。
- 入れ子の shell、`env`・`xargs` などの起動ラッパー、権限昇格、`gh`・`curl`・`ssh` などのネットワーク系コマンドを含まない。
- `git` は `status`・`diff`・`log`・`show`・`ls-files`・`rev-parse`・`grep`・`blame` に限る。commit・push・tag などの更新は Controller が行う。

条件を満たさない request は、理由を書いた feedback を付けて拒否する。Reviewer の shell は従来どおり拒否する。shell tool を allowlist に含めない既定設定では、動作は変わらない。

## Alternatives considered

- shell を禁止したまま、`bash` の指定を設定時のエラーにする。不整合は解消するが、Coder が build・test を確認できない問題は残る。
- 全 tool を無条件で許可する。commit・push・ネットワーク送信の禁止を permission handler で守れなくなる。

## Consequences

Coder は worktree 内で build・test・差分確認を実行し、その結果をもとに修正できる。Controller 検証と独立レビューは引き続き必須で、Coder の確認結果は承認の根拠にならない。

## Security and operational impact

この検査は SDK が解析したコマンドと path に基づく。`make` やテストスクリプトが内部で起動するプロセスまでは追跡しない。ネットワーク、Git の更新、worktree 外への書込みを確実に止めるには、OS や実行環境の隔離が別に必要である。Controller は従来どおり、保存済みの成果を scope と file-access policy で検査する。

## Compatibility

shell tool を allowlist に含めない構成は、従来と同じく shell を拒否する。OpenCode ACP adapter の shell は、ADR-0038 のとおり `deny` のままとする。

## Status

Accepted on 2026-10-24.
