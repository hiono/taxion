# ADR-0044: Binary distribution と協調的 provenance Gate

## 状態

採用。Issue #143 の方針A。

## 背景

source checkout の分離だけでは、ローカルで修正した Tool が正式版として operation store に非正式な schema や記録を保存する誤使用を防げない。version 文字列は、dirty build や異なる source revision を区別する十分な根拠にならない。

## 決定

製品配布単位を GitHub Releases の binary archive とし、mise の GitHub backend を利用する。利用先の Go toolchain と source build を不要にする。GoReleaser と Actions の実行 version を固定し、6 OS/architecture に native installation smoke を設ける。prerelease/snapshot を正式 operation の認可根拠にしない。

product operation は、承認済み manifest hash pin、archive hash、executable bytes hash、実 path、embedded version/source revision/channel、platform が一致する場合だけ開始する。全 CLI invocation の operation dispatch より前に Gate を実行し、HTTP/ACP 起動も保護する。storeに接続する mode は明示 policy に固定し、暗黙 development fallback や環境変数による downgrade を認めない。

development mode は隔離 fixture/store に限定する。product policy を上書きする便宜的な mode switch は作らない。利用者が管理する manifest pin と policy を trust root とし、付属 checksum だけを独立署名と見なさない。

これは local administrator を防御しない方針Aである。検査を削除した source や旧 binary に対する物理的な SQLite 書込み遮断、別 writer service、daemon、OS identity 分離は実装しない。hash 検査の削除や policy 改変が可能な管理者を防御できるとは記載しない。

## 結果と rollback

source build、Go install、dirty/candidate binary は development store でのみ利用可能となる。旧 store の mode は自動推定せず、明示設定なしの operation は拒否する。拒否は DBを開く前に構造化診断を返し、保存状態を変更しない。

既存 tag/asset/manifest は不変とする。rollbackVersion は v0.7.8。旧正式 binary のない platform は rollback `NOT_AVAILABLE` とする。旧版に新 Gate はないため、rollback を保護された product operation 再開と同一視しない。Task、approval、Flow、Dispatch、期限・予算は巻き戻さない。
