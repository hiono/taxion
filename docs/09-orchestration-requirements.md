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
| ORC-015 | 次の受け渡し保留と現在の実行中断を分け、版付き指示案と Human が観測した対象を発行・開始時に照合する。発行済み履歴と停止証拠を維持する | #73・#53・#54・#55 |
| ORC-016 | 本流を変更せず状況・根拠を確認し、確認済み・仮定・未確認と観測時点を区別する。明示的な反映だけを対象・証拠・Human 指示とともに本流へ保存する | #74・#67・#54・#62・#63 |

ORC-015・ORC-016 は [ADR-0024](adr/0024-human-handoff-control-and-side-questions.md)
で採択する。質問だけでは作業や承認を変更せず、既存の安全停止条件は維持する。

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
M12 は、受け渡し保留、実行中断、計画・コード・レビューへの対象付き補足、
状況・根拠の質問から不足確認・明示反映・続行までを実 SDK で検証する。
M13 は保留・指示案・反映記録の再起動と発行・開始との競合を照合する。
M15 は同じ操作と権限を CLI・ACP・Web で利用し、端末間で状態を共有する。
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
| Task・Dispatch | 版付き Task Graph、依存・concern ID 検査、scope out の保持、元の依頼の保存、契約 hash と設定の明示承認、停止確認後の後続契約の再承認、発行済み指示と変更関係・結果の保持 | 自然言語からの契約の全自動生成、参加者の本人認証 |
| Runtime | local・proc と Go Copilot SDK、役割別 Session と system prompt、明示的な上限付き自動 dispatch・結果回収、実効認証・停止証拠 | M13 の再起動・外部更新の復旧、M15 の利用者別所有権 |
| Review | controller の Git・検証結果照合、読み取り専用の独立 Reviewer、指摘からの修正と新 SHA の再レビュー、古い結果の再送拒否 | M13 のレビュー中の復旧、M15 の組合せ別受入れ |
| Memory | SQLite、階層・出典・版と Task の連携、Coder・Reviewer 指示の保存時の参照、計画の再構成、更新・失効時の検査、検索・export | M14 の共有保存・移行・仕様検索連携 |
| 復旧・外部記録 | Ledger 保存、別の SQLite store・outbox 部品 | 実行と Session の復旧、整合した outbox 更新、Issue・PR の自動記録 |
| CLI・ACP・Web | CLI 操作、ACP の Ledger 操作、Web forms・API proxy | 共通の会話 controller、Human の選択、実行の進行・再開と利用者別の所有権 |
| Human の途中介入・質問 | 観測時刻・hash と対象付き補足、保留・解除と中断要求・停止証拠の分離、版付き指示案の改訂・承認・発行、変更契約の再承認、本流を変えない質問、固定 SHA の読み取り専用調査、選択した発見事項の明示反映、Memory に基づく Planner 助言と非実行判断の保存、CLI と実 SDK の受入れ | M13 の復旧・競合照合、M15 の ACP・Web 共通操作と本人認証 |
| 仕様書 | connector なし | 許可された検索・取得、出典付きの受け渡し |
| 設定・認証主体 | 固定 Global・Project・環境参照と出典、設定の追跡・ignore 検査、repository と Copilot の主体・credential 参照・契約参照の分離、許可組合せと Dispatch・Session の照合、実効認証・権限の観測、SDK の管理 stage・commit と直接の index・秘密検査、signed-in SDK から独立レビューまでの受入れ | 共有 Memory の配置、承認者の本人認証、法的な契約適格性の保証、M13 の開発基盤別評価 |

現在の挙動は [Ledger schema](05-ledger-schema.md)、
[Runtime contract](03-runtime-contract.md)、
[Memory reference](08-memory-model.md)、
[Task Graph reference](10-task-graph.md) を参照する。
SDK の確認結果は次節の検証範囲に限る。cache の命中保証、
法的な契約適格性、仕様書検索の接続可否は個別の検証・判断を必要とする。

## M12 の実 SDK 受入れ結果

2026-10-12 に、許可された私有 Git worktree と実 Copilot SDK で M12 の受入れを完了した。
最終成果 SHA は `9db733f809b043d3869266c4e12c9b9873e214bd`。
読み取り専用の独立 Reviewer が全 acceptance を PASS、verdict を APPROVE とし、
controller が固定テストと Git を照合した後に flow と Task を COMPLETED にした。
検証用 branch と生の receipt は私有領域に保持し、push はしていない。

| 確認対象 | 実行証拠 |
| --- | --- |
| 修正と再レビュー | 既存の４独立 SDK Session で、無変更 baseline、実際の REQUEST_CHANGES、修正、新 SHA の APPROVE を確認。古い指摘の再送は現在の Task と Graph を変更しなかった |
| 受付と契約変更 | 元の依頼を DRAFT として保存し、表示した hash・設定・Reviewer の Task ID を明示承認。変更後は停止可能な保留境界を照合し、元の契約・結果・上限を保持して別 Task と flow を承認 |
| Planner と Memory | 実 Planner が保存済み acceptance、結果、Memory、修正上限を参照し、契約変更には HUMAN_REQUIRED を返した。助言の採択だけでは実行せず、未処理判断を保存した |
| 質問と追加調査 | 質問と固定 SHA の scoped 調査は Graph を変更しなかった。Human が選んだソース確認事項を未承認案に保存し、別の明示承認後に対象 Reviewer の instruction hash と発行記録へ接続した |
| 中断と停止確認 | owned SDK Session の RUNNING を観測して中断を要求し、実停止後の `runtime-stop:` 証拠、Dispatch CANCELLED、flow STOPPED を確認。Task 取消や Git 巻き戻しは行わなかった |

最終検証の推論量は 157,870 tokens で、承認した合計 200,000 tokens 以内だった。
11 推論 Session の実測課金は 9.4005324 SDK credits、
既存実験を含む 51 推論 Session の累計は 18.21538485 SDK credits。
これらの推論 receipt はすべて complete である。親 Copilot CLI の課金はこの集計に含まない。
推論前に中断した別の１ SDK Session は、provider の記録で要求数・モデル turn・課金量が０、
課金状態が unavailable だったため、complete の推論 receipt と分けて保存した。
不明な課金を complete や無料保証に置き換えてはいない。

再現には、Copilot CLI のログイン、repository 用の `gh` 等の別認証、
ignore・未追跡の固定設定、許可した worktree・scope・baseline SHA、
Task と設定の明示承認、ローカル Betterleaks を用意する。
`conversation intake → proposal → confirm` の後に、別の上限付き `workflow start` を行う。
`conversation watch` で `/plan → /adopt` または `/investigate → /apply` を選び、
補足を `/approve` して `/step` で独立 ROLE を進める。
`--strict-result-identities`、`--compact-context`、
`--controller-validation-submission`、管理 Git tool と同じ scanner は Session 再接続時にも揃える。
本流の回答を ROLE 間で手動コピーせず、controller の保存記録から次の指示を生成する。

検証中に停止した Session や拒否した回答は履歴として保持し、成功に書き換えなかった。
SUSPENDED の Reviewer 応答も完了とせず、実停止を確認して別契約で同じ SHA を再レビューした。
controller の検証待ちとモデルの提出を分ける指示は opt-in とし、
既存 Session の既定 prompt・configuration hash は維持する。
受入れ時は同じ提出指示を常時適用した移行用 binary を使った。
公開版での再現には上記 opt-in を指定する。過去の Session の prompt や hash は変更していない。
契約案は明示された構造化入力を使い、自然言語からの全自動契約生成、
承認者の本人認証、GitLab など別基盤の実環境受入れ、M13 の復旧を達成したとは扱わない。
