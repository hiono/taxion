# ADR-0047: Coder に worktree 内の shell を限定して許可する

## Context

Copilot adapter の permission handler は、Coder の shell request を常に拒否していた。一方、`--coder-tools` には `bash` を指定でき、モデルには shell tool が見える。そのため Coder は build や test を呼べても実行できず、Controller 検証の失敗に対して、手元で確かめずに修正を繰り返していた。利用先の Task では、この状態で repair が7回目まで続いた（#165）。

## Decision

Coder の shell request は、次の条件をすべて満たす場合に限り一回ずつ承認する。

- 承認済みの Coder tool allowlist に `bash` または `powershell` が含まれる。
- sandbox bypass と managed approval を要求していない。
- `possibleUrls` が空である。
- 作業ディレクトリと `possiblePaths` が、すべて割当 worktree の内側に解決され、`.git` を含まない。リンク先が存在しない symbolic link も拒否する。
- SDK がコマンドを解析でき、`commands` の各 identifier が `commandSegments` にも現れる。identifier は大文字小文字、`/`・`\` の区切り、`.exe` などの実行形式の拡張子を正規化して比較する。
- 入れ子の shell、`env`・`xargs`・`nice`・`stdbuf`・`command` などの起動ラッパー、権限昇格、`gh`・`curl`・`ssh` などのネットワーク系コマンドを含まない。
- `git` は `status`・`diff`・`log`・`show`・`ls-files`・`rev-parse`・`grep`・`blame` に限る。プログラムを起動する `-O`・`--open-files-in-pager`、ファイルを書く `--output`、`--ext-diff`、`--textconv` は省略形や短いオプションの連結も含めて拒否する。git の segment では引用符、エスケープ、変数展開、glob を使えない。コマンド全体に `GIT_` で始まる環境変数や `pager` を含む request、`alias`・`source` も拒否する。commit・push・tag などの更新は Controller が行う。

条件を満たさない request は、理由を書いた feedback を付けて拒否する。Reviewer の shell は従来どおり拒否する。shell tool を allowlist に含めない既定設定では、動作は変わらない。

## Alternatives considered

- shell を禁止したまま、`bash` の指定を設定時のエラーにする。不整合は解消するが、Coder が build・test を確認できない問題は残る。
- 全 tool を無条件で許可する。commit・push・ネットワーク送信の禁止を permission handler で守れなくなる。

## Consequences

Coder は worktree 内で build・test・差分確認を実行し、その結果をもとに修正できる。Controller 検証と独立レビューは引き続き必須で、Coder の確認結果は承認の根拠にならない。

## Security and operational impact

この検査は SDK が解析したコマンドと path に基づく拒否リストであり、sandbox ではない。`make`・テストスクリプト・`python` などのインタープリタ・`find -exec` のように、引数や内容から別のプログラムを起動するコマンドの内側は追跡しない。Git の設定で定義済みの diff・textconv driver が既定で起動する場合も対象外である。Coder は `.git` を変更できないため、driver は Controller 側の設定に限られる。ネットワーク、Git の更新、worktree 外への書込みを確実に止めるには、OS や実行環境の隔離が別に必要である。Controller は従来どおり、保存済みの成果を scope と file-access policy で検査し、commit は Controller だけが作る。

## Compatibility

shell tool を allowlist に含めない構成は、従来と同じく shell を拒否する。OpenCode ACP adapter の shell は、ADR-0038 のとおり `deny` のままとする。

## Status

Accepted on 2026-10-24.
