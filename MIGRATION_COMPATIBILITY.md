# Chinachu からの移行

このドキュメントでは、Chinachu の作業ディレクトリから Strata PVR へ
移行するときの入力形式、変換範囲、安全性、互換性の考え方を説明します。

Strata PVR は Node.js 版 Chinachu をそのまま置き換える互換ランタイムでは
ありません。録画、予約、番組表、ルール管理に必要なデータを移行し、日常の
操作感をできるだけ維持することを目的にしています。

## 移行後の実行環境

- 新規環境では `strata-pvr init` を実行します
- Chinachu から移行する場合は、先に `migrate/` を用意して
  `strata-pvr migrate` を実行します
- 実行時の設定は `data/config.json` です
- 番組表、予約、録画中、録画済み、ルールの正本は `data/strata.db` です
- 移行元のJSONファイルは `data/` に互換コピーとして残りません
- 配布済みバイナリの実行に Go、Node.js、SQLite、C コンパイラは不要です

録画ファイル本体は移行処理の対象外です。設定された録画ディレクトリにある
ファイルは移動・削除せず、録画済みデータのメタデータだけをデータベースへ
取り込みます。

## 移行元の配置

次の構成で旧環境のファイルをコピーします。

```text
migrate/
  config.json                         必須
  rules.json                          任意
  data/
    reserves.json                     任意
    recording.json                    任意
    recordings.json                   recording.json の別名
    recorded.json                     任意
    schedule.json                     任意
```

`config.json` は必須です。その他のJSONが存在しない場合は空のデータとして
扱われます。存在するファイルはすべて検証されるため、壊れたJSONが一つでも
ある場合は移行を開始しません。

## 移行手順

```sh
./strata-pvr migrate
```

移行コマンドは次の順で処理します。

1. `migrate/` 内の設定とJSONを検証する
2. 一時領域にStrata設定とデータベースを作成する
3. ルール、予約、番組表、録画中、録画済みのデータをSQLiteへ取り込む
4. 移行元を `backup/chinachu-<日時>/` へコピーする
5. コピー後のファイルについて、移行開始時のSHA-256とバイト数を検証する
6. 検証済みのデータを `data/` として配置する
7. 移行レポートを `backup/chinachu-<日時>-report.json` に保存する

移行レポートは `strata/migration-report` version 3 形式です。取り込み件数、
警告、移行元ファイルのSHA-256、バイト数、完了時刻を記録します。

入力検証、バックアップ、ハッシュ検証、データ配置のいずれかに失敗した場合、
移行元を変更せず、インストール済みの `data/` も作成しません。原因を修正した
あと、同じ `migrate/` を使って再実行できます。

## 設定の変換

| Chinachu の設定 | Strata の設定 |
| --- | --- |
| `mirakurunPath` / `schedulerMirakurunPath` | `mirakurun.url` |
| `recordingPriority`、`conflictedPriority` | `mirakurun.*Priority` |
| `recordedDir`、`recordedFormat` | `recording.directory`、`recording.filenameFormat` |
| `recordingStartMargin`、`recordingEndMargin`（旧Chinachuはミリ秒） | `recording.startMargin`、`recording.endMargin`（秒） |
| 空き容量閾値と `remove` / `stop` | `recording.lowSpace` |
| 認証付きまたは公開WUI | 認証ON/OFFを持つ単一の `web` リスナー |
| `wuiUsers` の平文認証情報 | Argon2id のパスワードハッシュ |
| 除外サービスとサービス順 | `services` |
| 正規化形式 | `advanced.normalizationForm` |

ルール、予約、番組表、録画中、録画済みのデータは、元のJSONドキュメントを
読み込んだうえでSQLiteの各テーブルへ保存します。Strataの運用中はSQLiteを
正本として扱います。

## 移行されない設定

次の設定は検出時に警告されますが、Strataの設定には取り込まれません。

- 組み込みTLSとクライアント証明書
- `X-Forwarded-For` の信頼設定とGeoIPフィルタ
- mDNS、Twitter / Tweeter連携
- VAAPI固有のトランスコード設定
- scheduler、EPG、競合、録画済み、空き容量のコマンドフック
- sendmailによる空き容量通知
- プロセス内部でのUID/GID権限変更

TLS、転送ヘッダーの信頼範囲、ネットワークアクセス制御は、reverse proxy、
ファイアウォール、VPNなどの外側で設定してください。実行ユーザーは、
systemdやコンテナランタイムなどのサービス管理側で設定します。

## 移行後の確認

移行レポートの件数と、Web UIに表示される番組表、予約、録画の内容を照合
してください。

その後、次の動作を確認します。

- scheduler実行後に番組表と予約が表示される
- 手動予約、skip、unskipが再起動後も保持される
- 録画完了後に録画中一覧から録画済み一覧へ移動する
- `cleanup` で存在しない録画ファイルの情報が削除される
- WUIから録画のプレビューと再生が行える

## 切り戻し

問題が見つかった場合は、Strataの scheduler、operator、WUI を停止します。
生成された `data/` を別の場所へ退避し、`backup/chinachu-<日時>/` の内容を
`migrate/` へ戻してください。

旧Chinachuを再開する場合は、バックアップ内の `config.json`、`rules.json`、
`data/` を旧作業ディレクトリへ戻します。録画ファイル本体は移行処理で変更
されていないため、移動や削除は必要ありません。

SQLiteはWALモードを使用します。稼働中に `strata.db` だけをコピーせず、
Strata側の復旧では停止中に `data/` ディレクトリ全体をバックアップから戻して
ください。

## 互換性の範囲

Strataは、番組表、予約、録画、再生、クリーンアップ、ルール管理に必要な
Chinachu形式のデータと主要なWUI/APIを引き継ぎます。

次の完全一致は移行要件に含みません。

- JSONのバイト単位の一致
- JavaScript正規表現やdateformatの細かな境界挙動
- 非公開・到達不能だった旧WUIページ
- 旧来の右クリックメニュー、外部検索、Twitter操作
- Socket.IOクライアントなど、Strataの運用に不要な旧連携
