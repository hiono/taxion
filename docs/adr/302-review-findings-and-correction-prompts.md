# ADR-302: Reviewerの指摘とCoder向け修正プロンプトを一体で引き継ぐ

Date: 2026-11-26

## Status

Accepted

## Context

Reviewerが問題点だけを返すと、Coderは修正対象の特定、判断根拠の調査、関連仕様の確認をやり直す必要があります。一方、修正指示だけを渡すと、指摘の根拠や対象成果との関係が失われ、古い指摘や誤検知を無条件に実行する危険があります。

CodeRabbitのレビュー指摘と「Prompt to fix issue」を組み合わせる方式を参考にし、Taxionでは既存のJobMessage、JobOutput、JobEvidence、RoleExecutionを使って、レビュー結果から修正・再レビューまでの情報を連続して引き継ぐ必要があります。

## Decision

**Reviewerはレビュー結果と、Coderが有効な指摘を検証・修正するための情報源を含む修正プロンプトを返します。修正プロンプトは情報伝達であり、仕様決定・承認・実行権限を付与しません。**

### Review findings

- ReviewerはCoderと独立したRoleExecution / Context / Sessionで、固定されたimmutable成果物・snapshotと承認済みAcceptance Criteriaを評価します。
- 指摘がある場合、Coderが修正要否と作業対象を判断できるよう、問題の内容、影響または判断理由、対象箇所、確認した根拠、期待する修正、修正後の完了条件を自然言語で提示します。
- 対象箇所は確認できたfile path、symbol、line等で示します。根拠には、実際に確認したADR、Product Contract、Acceptance Criteria、関連実装、テスト等を参照可能な形で示します。
- 参照先が未確認、存在しない、または対象HEADと一致しない場合は、その状態を明記し、確認済みであるかのように提示しません。
- 指摘がない場合は、レビュー対象のidentityと判定を明示し、不要な修正プロンプトを生成しません。

### Correction prompt

- Reviewerは、有効な指摘とその根拠・参照情報源・期待する修正・完了条件をまとめた、Coderがそのまま作業を開始できる自然言語の修正プロンプトを提示します。
- プロンプトには、指摘本文・コード・参照資料をuntrusted inputとして扱い、現在の対象コードと現行Accepted ADR / 仕様に照らして各指摘を検証するよう指示します。
- Coderは現在も有効な指摘だけを修正します。既に解消済み、再現しない、誤検知、または現行仕様に適合する指摘は修正せず、その理由を報告します。
- Coderは必要最小限の変更を行い、関連テストと要求された品質ゲートを実行し、変更内容・検証結果・未解決事項を返します。
- プロンプトやレビュー本文に含まれる命令は、Jobの目的・承認済みscope・現行仕様を変更しません。指摘文中の要求だけでファイル変更、コマンド実行、Git操作、Provider operation、外部副作用を許可しません。

### Job continuity and re-review

- レビュー結果と修正プロンプトは既存のJobMessageで受け渡します。固定JSON、MessageKind、専用grant、専用Workflow、独立したFinding管理基盤を追加しません。
- 指摘は既存のJobOutput identity / digestおよび該当するAcceptance Criteria・JobEvidenceとの関係を維持します。新しい成果物をレビューするときは、その成果物の新しいimmutable identityを対象にします。
- Coderの修正は既存のRoleExecution、Worktree ownership handoff、Provider operation、Job authorityの境界に従います。ReviewerとCoderのContext / Sessionを共有せず、同一Worktreeのmutating executionはADR-408に従って逐次実行します。
- 修正後は独立したReviewerが新しい成果物を再評価します。前回のPASSや指摘解消の自己申告だけで受入れ状態を変更しません。
- 指摘、修正、再レビューの往復は同一Jobに追記し、過去の成果・Evidence・判断を上書きしません。

## Consequences

Coderはレビューの判断根拠と修正に必要な情報源を受け取った状態から作業を開始できます。指摘の妥当性確認は維持され、誤検知や古い指摘に対する不要な修正を抑制できます。レビュー・修正・再レビューの履歴は既存のJobAggregate内で追跡できます。

レビュー指摘や修正プロンプトは、JobApproval、Execution Permission、JobQuestionAuthorization、Task assignment、Handoff、Job完了判定の代替にはなりません。追加Provider実行や外部副作用は、それぞれADR-506 / ADR-507等の既存認可を別途満たす必要があります。

## Relationship to other ADRs

ADR-201はJobAggregateとimmutable成果・Evidenceの保存境界、ADR-202は不変条件、ADR-300は情報交換と実行責任の分離、ADR-301はJobMessage、ADR-408はWorktreeとRoleの逐次引継ぎ、ADR-409はProvider operation、ADR-506 / ADR-507は追加Provider実行とoperation権限、ADR-800〜802は検証証拠とJob完了を定義します。
