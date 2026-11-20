# ADR-0009: 現行仕様・開発計画・履歴・検証証拠の正本を分離する

Date: 2026-11-20

## Status

Accepted

## Source decision

アーカイブ済み hiono/obsolete-taxion の redesign/job-centered-role-collaboration branch にある旧ADR-0085を原典とする（blob bd25f6aee937ecd3a61cf7a2298aceefec281055）。設計仕様、開発履歴、Issue / PR、CI証拠の責務分離を確認し、新しい履歴へ再確立した。DropboxをTODO・開発計画の正本とする規則は、現在のプロジェクト運用指示として追加する。

## Context

ADR、現行実装仕様、開発計画、作業経過、CI結果を同じ文書へ蓄積すると、過去の実装状態や検証結果が現HEADの仕様・品質を保証するように誤読されます。情報の意味と更新頻度に応じて正本を分離し、保存先ごとの責務を明確にする必要があります。

## Decision

次の保存先を正本とします。

| 情報 | 正本・保存先 |
| --- | --- |
| 現在採用している設計判断 | RepositoryのADR |
| 現在の実装仕様・操作方法 | Repositoryの現行ドキュメント |
| 設計・実装の変更履歴 | Git commit |
| TODO・ロードマップ・開発計画・実験計画 | 既存Dropbox文書 |
| 作業経過・未達事項・判断経緯 | GitHub IssueのNote / comment |
| 変更差分・レビュー・PR状態 | GitHub Pull Request |
| CI・配布・テストの実行結果 | GitHub Actions等の実行記録 |

### 1. ADR

ADRは現在採用している設計判断の正本とします。後続の設計・実装・ドキュメントはAccepted ADRとその階層に従います。設計判断を変更する場合は既存ADRを暗黙に書き換えず、後続ADRと明示的なSupersedes関係を記録します。

### 2. Repositoryの現行ドキュメント

Repositoryのドキュメントは現HEADのコードと設計に一致する現在の仕様・構造・操作方法を説明します。開発履歴、作業ログ、過去HEADの検証結果、時点依存の進捗記録を蓄積しません。

現行実装の利用に必要な再現可能な手順、test specification、API契約は記載できます。過去の実行結果を現行仕様の根拠として転記しません。

### 3. Dropboxの計画正本

TODO、ロードマップ、開発計画、実験計画およびその更新履歴はDropboxを正本とします。

既存Dropboxのフォルダ、文書名、保存先、参照先を指示なく変更しません。計画をGitへ複製して二重管理しません。既存Dropbox文書に旧PoC等の過去の前提が残る場合は、その内容を現在の計画として無条件に実行せず、現在の開発主体と製品目的に照らして整合を確認してから更新します。Dropboxの既存構成を保持し、必要な内容更新と名前・場所の変更を混同しません。

### 4. Git commit、Issue、Pull Request

- Git commitは設計・実装・現行ドキュメントの変更履歴を保持します。
- Issueは作業の時系列記録、未達事項、調査結果、判断経緯、次の作業単位を保持します。
- PRは変更差分、レビュー、修正、マージ判断の記録を保持します。
- 進捗や検証結果をRepositoryの現行仕様文書へ複製しません。
- Issue / PRのNoteには、対象HEAD、実施内容、検査結果、未確認事項を明記します。

### 5. CI・配布・テスト証拠

GitHub Actions等の実行記録を実行結果の正本とします。Issue / PRに結果を記載する場合はRun ID、検証対象の完全なHEAD SHA、workflow名、結果を対応付けます。

CI結果はそのRunが実際に検証したSHAにのみ有効です。後続commitでHEADが更新された場合、以前の成功結果を新しいHEADの検証結果として扱いません。未実施のテスト・Provider実行・配布検証を成功扱いしません。

## Consequences

- 現行仕様文書は現HEADに同期し、過去の進捗やCI結果を含みません。
- TODOと開発計画はDropbox、作業経過はIssue / PR、CI証拠はActionsに分離されます。
- 過去の検証結果を現HEADの保証として扱いません。
- Dropboxの既存保存先・文書名・参照先を維持し、内容の整合確認なしに古い計画を実行しません。

## Relationship to other ADRs

- ADR-0001は製品目的を定義します。
- ADR-0002は全体再構築原則を定義します。
- ADR-0003〜0008は現在のドメイン・権限・通信・並行性の設計判断を定義します。
- 本ADRは設計判断を変更せず、設計、実装、計画、履歴、検証証拠の正本と保存先を定義します。
