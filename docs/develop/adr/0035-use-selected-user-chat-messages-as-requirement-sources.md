# 35. 会話に残る利用者要求を限定して仕様入力に使う

Date: 2026-10-16

## Status

Accepted

## Context

Taxion の出発点は、Human が Orchestrator と相談し、生成された指示と各役割の結果を Coder・Reviewer 間で手動コピーする作業をなくすことだった。利用者は 2026-10-09 の会話で、専門家の役割分担を保ったまま、そのコピー作業を最初に自動化することを Day 1 の到達点として明示した。

この要求は M365 Copilot の会話 export 内にあり、export には assistant の提案・要約、引用、私的な会話メタデータも混在する。会話全体を仕様として扱うと、assistant の解釈を Human の決定と誤認したり、無関係な内容を後続の役割へ渡したりする。

## Decision

選択した会話 export `.json.md` を、ローカル仕様検索の private source として扱えるようにする。scope は原本の exact asset hash と、検索を許可する `messageIds` を含む。検索対象は時刻付きで `author=user` の選択済み本文だけに限定し、assistant の返答、未選択メッセージ、export header と会話 metadata は引用結果へ含めない。本文は書き換えず、source・export version・message ID・全文 document hash を引用 snapshot に保持する。

会話のローカル取り込み、Copilot への送信、外部公開は別々の用途であり、それぞれ exact scope/snapshot hash に対する承認を要求する。会話全体や未選択の内容を Copilot へ送らず、`publication` 承認を `copilot` 承認から推定しない。assistant の会話中の提案は、利用者が別途明示しない限り採択済み仕様と見なさない。

この追加はローカル export の読み取りだけを扱い、M365 connector、認証、remote search は実装しない。これらは [#77](https://github.com/hiono/taxion/issues/77) の利用条件評価に残す。

## Consequences

利用者がチャットで明示した要求を、出典を保ったまま検索し、通常の Task・Dispatch・Coder・Reviewer 契約に渡せる。選択 ID・原本 hash が変われば別の scope となり、再承認が必要になる。原本全文はローカルで hash 検証される一方、役割へ渡す検索結果は選択した user message に限られる。

元の受入れ範囲は PDF・DOCX・PPTX に限られていたため、この source class の追加は ADR-0025 を補う。自動選択、assistant 発話の権威化、無制限な transcript ingestion は行わない。

実装回帰は synthetic conversation fixture で行う。実際の利用者会話からの検索・Task 添付・Copilot 送信を確認するには、その exact hash と用途の承認を別に記録する。機能実装やこの ADR だけで、実 SDK の受入れ済みとは扱わない。

## Alternatives

会話全体を DOCX/PDF に変換する方法は、assistant 発話や private metadata まで仕様として検索するため採用しない。メッセージ本文を別の規範文書へ書き直す方法は、原文の出典・意味を保ちにくいため、利用者が別途承認した仕様化作業以外では採用しない。
