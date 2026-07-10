# Strata PVR

Strata PVR は、Mirakurun を録画バックエンドとして使う
PVR 実装です。Go 製の単体バイナリとして動作し、既存の
`config.json`、`rules.json`、`data/*.json`、録画ファイル形式との互換性を
重視しています。

このリポジトリは、旧 JavaScript 版を自動で上書きする移行ツールでは
ありません。既存の PVR 作業ディレクトリで Go バイナリを検証し、必要に
応じてラッパーや init script を置き換えるための実装です。

## 現在の状態

主要な実行経路は Go ランタイムで実装済みです。

- Mirakurun からの番組表取得、スケジューリング、予約生成
- 自動予約ルールの評価、手動予約、スキップ、録画停止
- オペレータによる録画実行、録画中/録画済み状態の更新
- 既存形式の JSON 状態ファイル読み書き
- `http`、`http+unix`、旧形式 `http://unix:` の Mirakurun URL
- Basic 認証をON/OFFできる単一リスナーのWUI/APIサーバ
- 録画済み/録画中/チャンネルの M2TS、MP4、XSPF、プレビュー配信
- 旧 API 互換の予約、録画、ルール、設定、ログ、ストレージ系エンドポイント
- Node.js や npm に依存しないネイティブ Web UI

互換性の詳細と残リスクは
`MIGRATION_COMPATIBILITY.md` と `UNIMPLEMENTED_REMAINING.md` を確認して
ください。現時点で既知の必須ランタイム未実装項目はありませんが、CLI 表示
の細部、JSON の未知フィールド順、JavaScript 正規表現や dateformat の
細かい差分は oracle test レベルの残リスクとして扱っています。

## 必要なもの

- Mirakurun
- MP4 再生、プレビュー、変換配信を使う場合は `ffmpeg` と `ffprobe`

配布済みバイナリの実行にGo、SQLite、Cコンパイラは不要です。ソースから
ビルドする場合だけGo 1.25以上が必要です。通常の実行にNode.js、npm、
webpackは不要です。

この Windows 開発環境では Go が次の場所にあります。`go` が `PATH` に
ない場合は直接指定してください。

```powershell
& "C:\Program Files\Go\bin\go.exe" test ./...
& "C:\Program Files\Go\bin\go.exe" build -o strata-pvr.exe ./cmd/strata-pvr
```

Linux などでは通常どおり実行できます。

```sh
go test ./...
go build -o strata-pvr ./cmd/strata-pvr
```

## 基本的な使い方

新規インストールでは、最初にStrata形式の設定とSQLiteデータベースを
初期化します。

```sh
./strata-pvr init
```

このコマンドは次のファイルを生成します。

```text
data/config.json
data/strata.db
```

`data/config.json`は`strata/config`スキーマのバージョン付き設定です。
`data/strata.db`はWALモードで作成され、今後の番組表、予約、録画履歴の
Strata形式ストレージとして使用します。Strata環境のルールはSQLiteを
正本とし、予約データもSQLiteへ保存します。CLI、WUI、スケジューラ、
オペレータから同じリポジトリを使用します。番組表キャッシュはチャンネルと
番組を別テーブルへ格納し、番組ID、チャンネル、放送時間で索引化します。
WUIとオペレータはプロセス内でSQLite接続を共有し、スケジューラも1回の
実行中は同じ接続を再利用します。
録画中状態と録画済みライブラリのメタデータもSQLiteを正本とします。録画
ファイル本体は従来どおり設定された録画ディレクトリに保存されます。
旧ChinachuのJSONデータは移行時にSQLiteへ取り込み、原本を`backup/`へ
保管します。Strataの`data/`には互換JSONコピーを作成しません。
ルートに旧形式の`config.json`が
存在する場合、`init`は上書きせずエラーにします。

録画済みプレビューは初回生成後に`data/.cache/previews/`へUUID名で保存し、
`previewCache.maxAgeDays`（最終参照からの日数）と`previewCache.maxSizeMB`（LRU上限）で保持量を制御できます。`0`は該当制限を無効にします。
元ファイル、生成条件、キャッシュファイル名を`strata.db`で管理します。同じ
条件ではFFmpegを再実行せず、元ファイルのサイズまたは更新時刻が変わると
自動的に再生成します。録画中プレビューは最新状態を優先するためキャッシュ
しません。

### Chinachuからの移行

旧環境のファイルを次の配置でコピーしてから移行します。

```text
migrate/
  config.json
  rules.json
  data/
    reserves.json
    recording.json
    recorded.json
    schedule.json
```

```sh
./strata-pvr migrate
```

`recordings.json`も`recording.json`の入力別名として受け付けます。コマンドは
全JSONを検証し、一時領域にStrata設定、互換JSON、`strata.db`を生成してから
`data/`へ配置します。旧ファイルは成功時にだけ`backup/chinachu-<日時>/`へ
元の構成のまま移動され、移行レポートが`backup/`へ保存されます。既存の
`data/`がある場合や検証に失敗した場合は移行しません。

移行レポートは`strata/migration-report` version 3形式です。ルール、予約、
番組表チャンネル、番組、録画中、録画済みの取込件数に加え、移行元の各
ファイルについてSHA-256とバイト数を記録します。バックアップ移動後に
再計算し、移動前と一致しない場合は移行を中止します。移行後はレポートの
件数とCLIの一覧を照合してください。

```sh
./strata-pvr rules
./strata-pvr reserves
./strata-pvr recording
./strata-pvr recorded
```

### 移行の切り戻し

移行後に問題が見つかった場合は、Strataのscheduler、operator、WUIを停止して
から切り戻します。生成された`data/`を別の場所へ退避し、
`backup/chinachu-<日時>/`を`migrate/`へ戻してください。旧Chinachuを再開する
場合は、バックアップ内の`config.json`、`rules.json`、`data/`を旧作業
ディレクトリへ戻します。録画ファイル本体は移行処理の対象外なので、削除や
移動は行われません。

`strata.db`を手動でJSONへ変換して復旧する運用は推奨しません。Strata側の
復旧では、停止中に`data/`ディレクトリ全体をバックアップから戻してください。
SQLiteのWAL利用中に`strata.db`だけをコピーすると未反映データを失う可能性が
あるため、稼働中の単一ファイルコピーは避けてください。

### Strataの実行

新規環境では`init`、旧Chinachu環境では`migrate`を完了してから実行します。
ルート直下の旧`config.json`や`data/*.json`を直接使用する互換モードは
ありません。

```sh
./strata-pvr update
./strata-pvr reserves
./strata-pvr run scheduler
./strata-pvr run operator
./strata-pvr run wui
```

運用コマンドは`data/config.json`と`data/strata.db`がない場合、`init`または
`migrate`を要求して終了します。`service ... initscript`は初期化前でも生成
できます。

## 主な CLI

```sh
./strata-pvr update [-s]
./strata-pvr search [options]
./strata-pvr reserve <program-id>
./strata-pvr unreserve <program-id>
./strata-pvr skip <program-id>
./strata-pvr unskip <program-id>
./strata-pvr stop <program-id>
./strata-pvr rules
./strata-pvr reserves
./strata-pvr recording
./strata-pvr recorded
./strata-pvr cleanup [-s]
./strata-pvr rule [options]
./strata-pvr enrule <rule-number>
./strata-pvr disrule <rule-number>
./strata-pvr rmrule <rule-number>
```

`-s` / `--simulation` は対応コマンドで状態ファイルを書き換えずに結果を確認
します。番組検索や一覧コマンドは、従来のテーブル表示に近い形式で
出力します。

## Web UI

`./strata-pvr run wui` で Go 製 WUI/API サーバを起動します。
同梱の `web/` はビルド不要の HTML/CSS/JavaScript です。

Web UI では次の操作ができます。

- ダッシュボードで予約数、録画中、録画済み、チャンネル、ルールを確認
- 時間軸とチャンネル軸の番組表を表示
- 番組詳細から手動予約、チャンネル番組一覧、ルール作成へ移動
- 予約の確認、スキップ、スキップ解除、手動予約削除
- 録画中番組の停止、プレビュー、ライブ視聴
- 録画済み番組の MP4 視聴、M2TS ダウンロード、削除
- 自動予約ルールの追加、編集、有効化、無効化、削除
- `config.json` の主要項目フォーム編集と raw JSON 編集
- スケジューラ強制実行、ログ、ストレージ、非秘密設定の確認

MP4 系の視聴ルートはオンデマンドで `ffmpeg` を起動します。WUI プロセスの
`PATH` から `ffmpeg` が見つからない場合、該当ルートは
`503 Service Unavailable` を返し、`log/wui` に実行ファイル未検出のログを
残します。

init script は標準出力に生成されます。既存サービスを直接上書きせず、別名で
保存して内容を確認してください。

```sh
./strata-pvr service scheduler initscript > strata-pvr-scheduler
./strata-pvr service operator initscript > strata-pvr-operator
./strata-pvr service wui initscript > strata-pvr-wui
```

systemd を使う Linux 環境向けには、`contrib/systemd/` にユニットファイル例を
用意しています。`operator` と `wui` は常駐 service、`scheduler` は timer
付きの one-shot service として実行します。

## 推奨する初回検証手順

```sh
./strata-pvr update -s
./strata-pvr reserves
./strata-pvr run wui
./strata-pvr run operator
```

WUI と operator は最初は別ターミナルで手動起動し、次のファイルが期待どおり
更新されることを確認してください。

- `log/wui`
- `log/operator`
- `data/reserves.json`
- `data/recording.json`
- `data/recorded.json`

Strata形式では状態JSONの代わりに`data/strata.db`が正本です。次の項目をCLIと
WUIの両方で確認してください。

- scheduler実行後に番組表と予約が表示される
- 手動予約、skip、unskipが再起動後も保持される
- 録画完了後に録画中一覧から録画済み一覧へ移動する
- `cleanup`で存在しない録画ファイルの行と関連プレビューが削除される
- `data/.cache/previews/`の録画済みプレビューが再利用される

問題がないことを確認してから、生成した init script を導入してください。

## 設定上の注意

- StrataのWUIは単一のHTTPリスナーを使用し、Basic認証をON/OFFできます。
- インターネットへ公開する場合はreverse proxyまたはVPNを使用し、TLS、
  転送元IPの信頼範囲、アクセス制限をStrataの外側で管理してください。
- 容量不足時の動作は、古い録画の削除または録画停止に限定しています。
- 旧設定のTLS、GeoIP、mDNS、Twitter通知、外部フック、UID/GIDは移行されず、
  `migrate`実行時に警告されます。

## ディレクトリ構成

```text
cmd/strata-pvr/     バイナリのエントリポイント
internal/cli/       CLI、互換チェック、service コマンド
internal/config/    設定読み込みと既定値
internal/legacy/    旧形式の型、ルール評価、録画ファイル名整形
internal/mirakurun/ Mirakurun クライアント
internal/scheduler/ 番組表取得と予約生成
internal/operator/  録画実行と状態更新
internal/wui/       WUI/API サーバ
internal/storage/   JSON の atomic 書き込み
web/                ネイティブ Web UI
testdata/           Mirakurun などのテスト fixture
```
