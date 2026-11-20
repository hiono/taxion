# ADR-0004: Job承認境界と禁止副作用によって認可する

Date: 2026-11-20

## Status

Accepted

## Source decision

アーカイブ済み `hiono/obsolete-taxion` の旧ADR-0080および旧ADR-0073の適用範囲を確認し、新TaxionのJobAggregateに適用する形で再構成した。旧ADRの操作allowlistに関する規定は採用しない。

## Context

静的なOperation allowlistやRole別tool allowlistを認可の中心にすると、承認済みJobの境界内で必要になった通常操作でも、事前列挙されていないという理由でRoleの協調を停止させる。一方、Agentの自由な手段選択だけに依存すると、Job境界外アクセスや未承認副作用を防止できない。

## Decision

Jobの明示承認で対象・資源・制約を固定し、その境界内での通常の手段選択をAgentに委ねる。ControllerはJob identity、承認、対象境界、禁止副作用、実行基盤の強制境界を検査する。

### 1. JobApproval

- JobApprovalはimmutable JobContract revisionのcanonical hashに束縛する。
- 承認対象にはJobID、単調増加する承認revision、原文依頼、目的、制約、受入条件、必要Evidence、対象repository/resource、worktreeとfile-access上限、aggregate budget、concurrency ceiling、absolute deadline、禁止事項、承認を要する副作用、秘密を含まない有効なRuntime configuration参照を含める。通常操作名やRole別operation grantは承認対象にしない。
- TaskはJob境界を狭められるが拡張できない。
- 会話、Planner proposal、Task、RoleExecution、Providerの自己申告はJobApprovalの代替にならない。
- JobApproval recordはJobIDをkeyとし、承認対象revision、canonicalization version、proposal hash、承認者identity、承認元、理由、承認時刻を保持するappend-only recordとする。
- 承認proposal hashはversion付きcanonical encodingから算出する。古いrevisionへの承認、同じapproval IDへの異なる内容の再利用、承認済み項目の変更は認可を無効化する。
- 各RoleExecutionのprivileged operation直前に、現在のJobApproval、Taskが狭めたresource scope、Runtime identity/configuration、残予算を再検証する。
- 承認基準を変更した場合は新しいrevisionと明示的な承認を要求する。

### 2. PlanningAuthorization

DRAFT Jobの初期Plannerに限り、明示的な利用者起動に基づく別個のimmutable PlanningAuthorizationを許可する。これはread-only context acquisitionとbounded planningだけを許可し、JobApproval、Job activation、実装Task作成、書込み、検証実行、commit、publish、credential使用、network、外部mutationを許可しない。

PlanningAuthorizationはJobID、原文依頼hash、認証済み利用者principalとintake source / time、Provider account / configuration digest、repository identity、強制可能なread-only境界、許可effect、planning budget、deadline、grant record hashに束縛する。開始、各turn、各effect、resumeの前に再検証する。Plannerの発言や利用者の回答でgrant scopeを拡張しない。Provider側でread-onlyを強制できない場合はimmutable snapshotまたは明示文書だけを渡し、それも強制できなければrepository readを許可しない。

### 3. 通常操作と禁止副作用

承認済みJobの強制可能な境界内で行う可逆な通常作業は、操作名の事前登録を要求しない。次は明示的に禁止するか、承認経路へ送る。

- 承認対象外repository/resource/worktreeへのアクセス
- 他Job・他実行主体のデータへの不正アクセス
- secret / credential / personal dataの不正取得・表示・外部送信
- 権限昇格、sandbox bypass、未承認credential利用
- 承認されていない外部サービス・本番環境への変更
- 承認範囲外のcommit / push / release / publish
- 履歴、provenance、検証結果の改変・捏造
- 二重所有、依存関係を無視した競合、実行状態不明のままの再送
- 上位安全ポリシーで禁止された副作用

### 4. 未知のTool / Effect

未知の名前だけを理由に一律拒否しない。Controllerは操作の影響を評価し、OS / sandbox / filesystem / network / credential等の実行基盤で封じ込められるかを確認する。通常操作はJob境界内で許可し、高リスク・不可逆・外部副作用は人間承認へ分岐する。影響を判定できず封じ込められない場合は具体的な承認要求または拒否とする。Tool discoveryやProvider AvailableToolsは能力観測であり、認可根拠ではない。

### 5. 承認値の再検証

ValidationCommandsは承認済みの完全なcommand stringと一致させる。genericなprocess.exec / test.run capabilityだけで任意commandを許可しない。repository/resource identity、対象commit/tree、worktree、configuration identity、承認revision、aggregate budget、concurrency ceiling、deadline等、人間が承認した具体的な値は実行時に照合する。通常操作allowlistを認可の正本にしないことは、承認値の完全一致や安全境界の廃止を意味しない。

### 6. 全経路での強制

Start、Resume、Send、tool preflight、Effect、save、restore、recovery、external inputで同一のJob境界と不変条件を強制する。Controllerのチェックだけでなく、Domain mutationと実行基盤でも安全境界を守る。

## Consequences

Roleは承認済みJob内で通常の手段を選択できる。Controllerはallowlistの網羅性ではなく、Job境界、禁止副作用、承認条件、実行基盤の封じ込めを強制する。

## Security and operational impact

認証、認可、秘密情報、Job境界、データ整合性は妥協しない。未知のEffectを無条件許可しない。promptやRoleの善意だけを安全境界にしない。

## Alternatives considered

- 通常操作をすべて事前列挙する方式は、適応的協調を不必要に停止させるため採用しない。
- 未知のToolをすべて許可する方式は、副作用を評価できないため採用しない。
- Role名を権限主体とする方式は、Job承認と実行主体の責務を混同するため採用しない。

## Relationship to other ADRs

ADR-0003のJobAggregateを唯一の認可対象とする。ADR-0005のJobMessageとADR-0007のA2Aメッセージは、それ自体で権限を付与しない。ADR-0005の不変条件強制とADR-0008のWorktree所有権は本ADRの境界を維持する。
