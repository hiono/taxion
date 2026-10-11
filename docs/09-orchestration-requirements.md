# 09 — Taxion の要件と到達条件

Taxion は、ユーザーとの会話から独立した担当へ作業を渡すオーケストレータである。
階層化した Memory に判断と経験を保存し、Task Graph と実行契約に関連づける。
目的は、指示・結果の手動コピーをなくすことに加え、
ユーザーが同じ目的、制約、保留作業を説明し直さずに作業を進められるようにすることである。

この文書は採択した要件と検証条件を定義する。設計の理由は
[ADR-0020](adr/0020-layered-memory-and-role-sessions-for-orchestration.md) と
[ADR-0021](adr/0021-durable-task-graph-and-dispatch-change-control.md)、
段階と作業の一覧は [roadmap](04-roadmap.md) を参照する。
以下の条件を定義したことは、その実装や検証が完了したことを意味しない。

## 情報の保持と実行の要件

| ID | 要件 | 実装・検証の追跡 |
| --- | --- | --- |
| ORC-001 | 依頼から Task を構成し、許可された scope と acceptance に従って Coder・独立 Reviewer へ指示と結果を渡す | #48・#52〜#54 |
| ORC-002 | log、lesson/decision、承認済み guardrail を分け、出典、版、更新・失効、適用範囲を保持する。guideline 候補を未承認のまま強制しない | #66・#67 |
| ORC-003 | 重要な懸念に Task ID を付与する。scope out は追跡解除ではなく、保留理由、状態、再開条件を伴う判断として保存する | #47・#67 |
| ORC-004 | Task ID と Dispatch ID、依存と必要な状態、active 指示の変更関係を保持する。古い結果や途中の質問で指示を暗黙に差し替えない | #47・#48・#54 |
| ORC-005 | Go Copilot SDK で役割別 Session と system prompt・履歴を分離し、継続利用する。SDK 型は core に持ち込まない | #49〜#51 |
| ORC-006 | モデル選択は Auto とする。利用条件と取得可能な消費量・cache 指標を確認し、取得不能や未測定の指標を区別する | #49・#51 |
| ORC-007 | repository、baseline、対象 SHA、worktree、scope、検証結果を controller が照合する。修正後は新しい SHA を独立レビューする | #52〜#54 |
| ORC-008 | 判断と証拠は Session・cache と別に永続化する。再起動時に状態を照合し、重複実行や証拠不足の完了を防ぐ | #55・#57・#66・#67 |
| ORC-009 | 必要な Issue を実行前に登録し、Task・PR・SHA・証拠を関連づける。外部更新の許可、失敗、未同期状態を管理する | #56・#57 |
| ORC-010 | 仕様書は出典、版、取得情報、利用許可を伴って渡す。閲覧権限だけで別サービスへの送信を許可しない | #58〜#60 |
| ORC-011 | CLI・ACP・Web が同じ controller と制約を使う。利用者の認証と Session の所有権を検査する | #54・#62・#63 |
| ORC-012 | project 利用者、開発基盤の操作主体、AI 認証主体、利用契約・費用帰属、worker を独立して管理し、承認された組合せを Dispatch・Session に結びつける。開始・再開・担当交代時に照合する | #69・#55・#71 |
| ORC-013 | repository と作業管理の接続先を provider・host・path・オブジェクト ID で識別する。GitHub・GitLab・Bitbucket の能力差を adapter で扱い、承認・証拠・外部更新の共通契約を保持する | #56・#70・#63 |
| ORC-014 | 固定 Global／Project 設定と環境変数を用い、ローカル `.taxion/` と実 `.env` の追跡・ignore 状態を検査する。commit 前の index、公開前の全未公開コミットと外部記録で秘密情報の混入を検査する | #69・#72・#53・#56・#70 |

ORC-012〜ORC-014 と形式に依存しない設定契約の採択判断は
[ADR-0023](adr/0023-independent-project-forge-runtime-and-configuration-contracts.md)
に記録する。Issue はその実装・検証を追跡する。
通常設定の優先順位は、承認済みの権限・契約・送信先の範囲内で適用する。
開始時の主体・契約・送信先と承認を保存し、
再開時の変更・範囲逸脱は停止と再認可として扱う。

project/global は Memory の階層とは別の適用範囲である。
Task Graph は現在の作業と関係、Memory は判断と経験の根拠、
Dispatch は特定の実行指示を管理する。参照 ID と版で関連づけ、
会話の要約を唯一の正本にしない。
必須情報を検索順位や context 上限のために落とした場合も、
「見つからなかった」として推測で続行せず、欠落を明示する。

Planner の Session を扱える設計は ORC-005 に含めるが、
実行フローへの採用と起動条件は #64 で判断する。
Rust は #65 の後続評価であり、最初の到達条件の前提にはしない。
tmux の画面操作、別モデル API の契約、Session ごとの専用 cache を暗黙の前提にしない。

## 段階別の完了判定

各段階は必要な前提 Issue の完了後に実施する。
次の表は成果の判定条件であり、Milestone の close 数を代用しない。

| 段階 | 判定条件 |
| --- | --- |
| M10 | Task ID 未付与、依存・状態の不整合を拒否する。scope out した Task と Memory の判断・出典・版を再読込後に復元できる |
| M11 | 実際の Go SDK で異なる役割の Session に二回以上指示し、system prompt・履歴の分離、継続、cancel と失敗を確認する。Auto と取得可能な利用量を記録する |
| M12 | 実際の SDK と Git repository で、会話→実装→独立レビュー→修正→新しい SHA の再レビュー→報告を手動コピーなしで通す。Memory に保存した判断・指摘を次の指示で利用する。開発基盤と Copilot の実効認証主体・契約参照を別々に確認し、承認済みの組合せを保存する |
| M13 | 実行中、許可後・開始前、結果取得後、レビュー中、判断待ちからの再起動で主体・接続先・契約と実行証拠を照合する。外部更新の失敗・重複・能力差を記録し、GitHub・GitLab・Bitbucket の対応評価と追加実装範囲を確定する |
| M14 | 承認された検索環境で仕様書を取得し、出典・版・利用許可を保持して役割へ渡す。Memory の移行と tools 連携でも権限と証拠を保持する |
| M15 | CLI・ACP・Web から共通の状態を参照し、仕様検索から結果報告・復旧・外部記録まで通す。複数開発者の参加・離脱・担当交代、個人／組織契約、session 所有権を採択した基盤と worker の組合せで検証する |
| M16 | Planner と Rust を別々に比較評価し、採否、制約、必要な後続作業を記録する。評価を機能実装の完成と扱わない |

最初の到達点は M12 とする。
実装・レビューの実行に必要な仕様は、この段階では許可された明示入力でもよい。
これは仕様検索の自動化ではなく、検索からの手動コピーをなくす条件は M14・M15 に残す。
未許可または非対応の検索 connector は blocker として保持し、
手動入力で置き換えて達成扱いにしない。

## 軌道修正と Task lost の検証

情報が保存されているだけでなく、後続の計画・指示で使われることを確認する。
M12 の一連の実行には次のケースを含め、M13 で controller の再起動まで拡張する。

1. ユーザーが scope、acceptance または保守性の懸念を明示する。起点、判断、Task ID を保存する。
2. 今は実行しない作業を scope out し、理由と再開条件を残す。別の作業を進めても元の Task を失わない。
3. Session を再作成し、保存済みの契約と Memory から次の指示を構成する。同じ判断の再説明を必要としない。
4. Reviewer の指摘を Coder に渡して修正し、生成した SHA を再レビューする。指摘と是正の対応を保持する。
5. 新しい SHA の承認後に古い REQUEST_CHANGES を再送する。古い Dispatch の結果として識別し、現在の状態を巻き戻さない。
6. 必須の契約情報を欠落させる。実行を止め、欠けた情報を示す。scope や許可をモデルに推測させない。

実行証拠には Task・Dispatch・Session・role、repository、baseline・対象 SHA、
利用した判断と Memory の版、検証コマンドと exit code、verdict、
状態遷移、利用量と取得不能の指標を含める。
公開できる再現手順と非公開環境に置く証拠を分ける。
mock、echo、Ledger の更新、手動の担当切り替えだけでは M12 の証拠を満たさない。

## Human の判断とデータ利用の境界

controller は、既存の判断と新たに必要な判断を区別する。
質問・コメント・懸念では原則として active Dispatch を維持する。
scope、権限、acceptance、例外、外部公開、接続方式を変更する場合は、
影響、選択肢、推奨理由と必要な許可を示し、判断と適用範囲を保存する。
通常の作業順序と検証を、ユーザーによる繰り返しの調整に依存させない。

仕様書検索の接続候補には M365 Copilot と SharePoint がある。
ブラウザーの RAG と公式の検索・取得 API は同等と決めつけず、
組織の認証、license、資料ごとの権限、出典取得、送信先の許可を確認する。
読み取った資料に命令が含まれていても、role や実行権限を変更しない。
credential、非公開の資料や会話を公開 Issue・PR・ログへ転記しない。

## 現在の実装との差分

次の区別を維持し、設計文書を現在の使用方法の参照にしない。
個別の実装結果は Issue に記録し、機能が変わった時は参照文書を更新する。

| 対象 | 現在の部品 | この到達条件に向けて未実装の内容 |
| --- | --- | --- |
| Task・Dispatch | JSON Ledger、版付き Task Graph の登録・遷移・依存検査、明示された concern の ID 未付与の拒否、scope out の保持 | 会話からの Task 抽出、実行権限の確認、Dispatch の変更管理、実行状態との一貫した更新 |
| Runtime | local・proc adapter | Go Copilot SDK 接続、役割別 Session、会話からの自動 dispatch と結果回収 |
| Review | SHA を Evidence に記録 | Git の事実との照合、独立 Reviewer の実行、修正・再レビュー |
| Memory | SQLite、候補生成、明示承認と限定した条件検査、検索・export | 階層の出典・版と Task の連携、指示への反映、更新・失効、実際の project/global 保存・移行 |
| 復旧・外部記録 | Ledger 保存、別の SQLite store・outbox 部品 | 実行と Session の復旧、整合した outbox 更新、Issue・PR の自動記録 |
| CLI・ACP・Web | CLI 操作、ACP の Ledger 操作、Web forms・API proxy | 共通の会話 controller、Human の選択、実行の進行・再開と利用者別の所有権 |
| 仕様書 | connector なし | 許可された検索・取得、出典付きの受け渡し |
| 設定・認証主体 | `config check` による厳密な設定検査、固定 Global・cwd Project・環境変数の解決と出典、`.taxion/` 全体と cwd の実 `.env` の追跡・ignore 検査、`--task` による出典付き判断の許可組合せとの照合、`git check-index` による実 index の禁止パス検査、path-only hook の明示導入・状態確認・解除 | 共有 Memory の配置、承認者の認証、実効認証・契約の確認、Dispatch・session への接続と再開照合（#69）、controller commit への検査接続、秘密値 scanner と hook への追加（#72） |

現在の挙動は [Ledger schema](05-ledger-schema.md)、
[Runtime contract](03-runtime-contract.md)、
[Memory reference](08-memory-model.md)、
[Task Graph reference](10-task-graph.md) を参照する。
SDK の対応や利用条件、cache の保持・命中、仕様書検索の接続可否は未確認であり、
実装の着手条件または実際の検証結果として追跡する。
