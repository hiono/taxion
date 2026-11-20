# ADR-0000: Architecture Decision Records の管理規約

Date: 2026-11-20

## Status

Accepted

## Context

Taxion は、曖昧な人間の意図を対話で整理し、複数の Agent / Role の協調と安全境界によって Job の完遂を支援する製品として再構築する。設計判断、現行仕様、変更履歴、検証証拠を混在させると、過去の判断や検証結果を現行の正本と誤認する。

旧 `hiono/taxion` と旧 `hiono/taxion-poc` は、現在はそれぞれ `hiono/obsolete-taxion` と `hiono/obsolete-taxion-poc` に改名・アーカイブされ、読み取り専用の歴史資料である。新しい `hiono/taxion` は空の非公開リポジトリとして新しい履歴を開始する。旧リポジトリのGit履歴・Issue・PR・コメントは変更しない。

旧Taxionと旧Taxion-PoCは同一リポジトリの別名や単純な親子ブランチではない。旧Taxionは製品本体の設計判断・実装の履歴を持つ比較基準であり、旧Taxion-PoCは旧Taxionの公開済み製品要求・Accepted ADRを入力として独立に構築されたSix-Actor候補実装である。旧PoCは使い捨て実験ではなく、旧Taxion本体と比較評価する候補実装だった。ただし、その実装・テスト結果が新Taxionの採用を自動的に決定するものではない。

## Decision

### 1. ADRの役割

ADRは、現在採用しているアーキテクチャ上の判断、その理由、適用範囲、トレードオフおよび他の判断との関係を記録する正本とする。

ADRに記録しないもの:
- 作業進捗、TODO、ロードマップ、実験計画、時点依存のCI結果
- 現行仕様ではない過去の実装説明
- 未検証の仮説を実証済みとする記述
- コード・Issue・PR・検証証拠の代替となる詳細な作業ログ

### 2. ADRの必須構成

各ADRは原則として次の構成を持つ。

1. Title and number
2. Date
3. Status: Proposed / Accepted / Rejected / Superseded / Deprecated
4. Context
5. Decision
6. Consequences
7. Security and operational impact（該当する場合は必須）
8. Alternatives considered
9. Relationship to existing ADRs
10. Acceptance criteria（実装・受入条件を規定する場合）

Decisionは採用する規則を肯定形で明示する。Contextは問題と制約を説明し、Consequencesは採用による影響を記述する。実装の時点依存状態は含めない。

### 3. 番号とファイル

- 新TaxionのADRは `docs/adr/NNNN-kebab-case-title.md` に保存する。
- 連番は新リポジトリ内で一意とし、旧リポジトリの番号をそのまま再利用しない。
- 番号は識別子であり、優先順位や規範性を意味しない。
- ADRの一覧と関係は `docs/adr/README.md` に集約する。
- 採番済みADRの番号を再利用しない。内容の変更は既存ADRの改訂、または明示的な置換ADRで記録する。

### 4. 適用階層と衝突解決

- 製品目的を定めるADRが、個別の実装方式を暗黙に決めることはない。
- 横断的な安全・整合性不変条件は、個別機能の利便性より優先する。
- より具体的なADRは、上位の目的を実現するための詳細を定める。上位の安全境界を弱めてはならない。
- 後続ADRが既存ADRを置換する場合、置換対象・範囲・維持する決定を明示する。番号の大小や作成日の新しさだけで全体を上書きしない。
- 衝突が解決するまで、実装は安全側の既存Accepted decisionに従い、矛盾する新規実装を追加しない。

### 5. 旧リポジトリの参照とclean-room境界

旧2リポジトリは読み取り専用の歴史資料として扱う。旧リポジトリにCommit、Push、Issueコメント追加・編集、PR変更を行わない。

旧資料から利用できるもの:
- 一般化された製品要求、設計上の不変条件、失敗知見
- ADRの判断根拠を再検討するための情報
- 候補実装の比較評価に必要な、明示的に確認された技術的事実

新リポジトリへ無条件に移植しないもの:
- 旧Git履歴、Commit、Issue/PR履歴、旧識別子
- 旧リポジトリ固有の非公開情報、秘密情報、資格情報
- 旧コード、旧ファイル構成、旧モデル、旧API、旧実行経路
- 出所と利用条件を確認できない内容

旧PoCのコードを採用する場合は、別途provenance、作成経緯、利用条件、設計適合性、安全性、保守性および受入証拠を確認し、採用判断を記録する。旧PoCの存在だけを理由に直接移植しない。旧Taxionの実装も同じ基準で評価する。

### 6. 内部Actor通信と外部Agent相互運用

Taxion内部のHUMAN / CONTROLLER / ORCHESTRATOR / PLANNER / CODER / REVIEWERの通信は、Taxion固有のJob中心ドメイン契約として定義する。外部Agentとの相互運用はA2Aを唯一の必須プロトコルとする。A2Aは内部JobMessage、JobAggregate、認証済みActor identity、認可または承認を置き換えない。

MCP、ACP系方式、Provider固有SDK等は初期必須構成に含めない。具体的な製品要件が確認された場合に限り、任意Adapterとして別途判断する。A2Aだけで全製品シナリオを満たせるかは、検証完了まで設計仮説として扱う。

### 7. 情報の正本

| 情報 | 正本 |
|---|---|
| 現在採用している設計判断 | ADR |
| 現在の実装仕様・操作方法 | リポジトリの現行ドキュメント |
| 設計・実装の変更履歴 | Git Commit |
| 作業経過・未達・調査記録 | GitHub Issue |
| 差分・レビュー・統合判断 | Pull Request |
| CI・テスト・配布結果 | GitHub Actions等の実行記録 |
| TODO・開発計画・ロードマップ・実験計画 | 既存Dropbox文書 |

Dropboxのフォルダ名、文書名、保存先、参照先は指示なく変更しない。

## Consequences

- 新Taxionは旧2リポジトリの履歴を引き継がず、新しい設計判断と実装履歴を持つ。
- 旧Taxionと旧Taxion-PoCの関係を、混同・統合・単純移植として説明しない。
- ADRの正しさは文書の存在や相互リンクではなく、根拠・適用範囲・他ADRとの整合性で評価する。
- 実装開始前に、製品目的、ドメインモデル、協調、認可、安全、Provider、永続化、A2A、証拠・受入れのガードレールを確定する。

## Security and operational impact

秘密情報、認証・認可、Job境界、データ整合性、provenance、Providerの不確実な実行状態を妥協しない。自然言語の内容、Agentの自己申告、過去の履歴だけで権限を付与しない。未確認の事実・実行結果を確定事項として扱わない。

## Alternatives considered

- **旧ADRをそのままコピーする:** 旧Taxion固有の履歴・前提・番号・矛盾を新しい正本へ持ち込むため採用しない。
- **旧2リポジトリを一つの履歴として統合する:** 両者の成立経緯と責務を失わせるため採用しない。
- **旧PoCを使い捨て扱いする:** 独立した候補実装として得られた技術的証拠を不当に捨てるため採用しない。
- **旧PoCを無条件に製品実装へ昇格する:** 比較評価と製品受入れ証拠を欠くため採用しない。

## Relationship to other ADRs

本ADRはADR体系、情報の正本、旧リポジトリ参照境界、clean-room、内部JobMessageと外部A2Aの区別を定める。製品目的、Jobドメイン、認可、Provider、永続化等の具体的な不変条件は後続ADRで定義する。本ADRだけで実装完了を意味しない。
