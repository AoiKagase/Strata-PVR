# Strata PVR

Strata PVR は、Mirakurun を録画バックエンドとして利用する、Go 製の
シンプルな PVR です。

Chinachu を使ってきたユーザーが、これまでの操作感や設定の考え方を大きく
変えずに移行できることを目指しています。Node.js などの実行環境を別途
用意せず、単一のバイナリを配置するだけで導入・稼働できることも、重要な
設計方針です。

## 開発の背景と方針

このプロジェクトは、次の考えから開発を始めました。

- Chinachu の利用者が次に移行できる受け皿を用意する
- Chinachu の操作方法や設定の感覚を、できる限りそのまま残す
- すべての機能を再現するのではなく、録画・予約・番組表・管理に必要な
  機能へ絞る
- Node.js、npm、webpack など外部ランタイムの影響を避ける
- 配布物は単一バイナリを中心にし、導入と運用を簡単にする

そのため、Chinachu の完全な後継や全機能互換を目指すものではありません。
既存データを移行しやすくし、日常の録画運用に必要な部分を小さく安定して
提供することを優先しています。

## 主な機能

- Mirakurun からの番組表取得
- 自動予約ルール、手動予約、予約のスキップ・解除・削除
- スケジューラによる予約生成
- 録画の開始・停止と、録画中・録画済み状態の管理
- CLI による番組検索、予約、ルール、録画一覧の操作
- 番組表、予約、録画、ルールを扱う Web UI / API
- 録画済み番組の M2TS、MP4、XSPF、プレビュー配信
- Chinachu の `config.json`、`rules.json`、`data/*.json` からの移行
- Web UI 静的ファイルのバイナリへの埋め込み

実行時に Node.js、npm、webpack、SQLite、C コンパイラは必要ありません。
MP4 再生、プレビュー、変換配信を利用する場合のみ `ffmpeg` と `ffprobe` が
必要です。

## 必要なもの

- Mirakurun
- 配布済みバイナリを使う場合: 実行対象 OS 用の Strata PVR バイナリ
- MP4 系の視聴やプレビューを使う場合: `ffmpeg` / `ffprobe`
- ソースからビルドする場合: Go 1.25 以上

## ビルド

```sh
go test ./...
go build -o strata-pvr ./cmd/strata-pvr
```

Windows で `go` が PATH にない場合は、Go のインストール先を直接指定でき
ます。

```powershell
& "C:\Program Files\Go\bin\go.exe" test ./...
& "C:\Program Files\Go\bin\go.exe" build -o strata-pvr.exe ./cmd/strata-pvr
```

## 初回セットアップ

### 新規環境

作業ディレクトリで次を実行します。

```sh
./strata-pvr init
```

`init` は、Strata 用の設定と SQLite データベースを作成します。

```text
data/config.json
data/strata.db
```

Strata 環境では `data/strata.db` が番組表、予約、録画状態、録画履歴の正本
です。録画ファイル本体は、設定した録画ディレクトリに保存されます。

### Chinachu から移行する場合

まず、旧環境のファイルを次のように `migrate/` へコピーします。

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

その後、移行を実行します。

```sh
./strata-pvr migrate
```

`recordings.json` は `recording.json` の別名としても利用できます。移行に
成功すると、旧ファイルは `backup/chinachu-<日時>/` へ移動され、移行レポート
が `backup/` に保存されます。検証に失敗した場合や、移行先の `data/` が既に
存在する場合は、旧ファイルを変更しません。

移行後は、件数や内容を CLI で確認してください。

```sh
./strata-pvr rules
./strata-pvr reserves
./strata-pvr recording
./strata-pvr recorded
```

移行の互換範囲は [MIGRATION_COMPATIBILITY.md](MIGRATION_COMPATIBILITY.md) に
まとめています。旧設定のうち TLS、GeoIP、mDNS、Twitter 通知、外部フック
などは移行対象外です。

## 起動

初期化または移行を完了した作業ディレクトリで、必要なプロセスを起動します。

```sh
./strata-pvr update
./strata-pvr run scheduler
./strata-pvr run operator
./strata-pvr run wui
```

- `scheduler`: Mirakurun から番組表を取得し、予約を更新します
- `operator`: 予約に従って録画を実行します
- `wui`: Web UI と API を提供します

最初は別々のターミナルで起動し、動作を確認してから systemd などのサービス
へ組み込むことを推奨します。

## CLI

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

`-s` / `--simulation` は、対応するコマンドの結果を確認しながら状態を
変更しないためのオプションです。

## Web UI

```sh
./strata-pvr run wui
```

Web UI では、次の操作を行えます。

- ダッシュボード、番組表、予約、録画、ルールの確認
- 番組詳細からの手動予約とルール作成
- 予約のスキップ、解除、削除
- 録画中番組の停止、プレビュー、ライブ視聴
- 録画済み番組の MP4 視聴、M2TS ダウンロード、削除
- 自動予約ルールの追加、編集、有効化、無効化、削除
- 設定の主要項目編集と raw JSON 編集
- スケジューラ実行、ログ、ストレージの確認

HTML、CSS、JavaScript はバイナリに埋め込まれています。通常の配布では
`web/` ディレクトリを別途配置する必要はありません。開発時に外部ファイル
を使う場合は、`wuiWebDir` または `run wui --web-dir <path>` を指定します。

MP4 系の視聴ルートは必要なときに `ffmpeg` を起動します。実行ファイルが
見つからない場合は、該当する機能だけが `503 Service Unavailable` になります。

## サービスとしての運用

systemd 用の unit ファイル例を `contrib/systemd/` に用意しています。
`operator` と `wui` は常駐サービス、`scheduler` は timer から起動する
one-shot サービスとして運用できます。詳しくは
[contrib/systemd/README.md](contrib/systemd/README.md) を参照してください。

バイナリから init script を出力することもできます。

```sh
./strata-pvr service scheduler initscript > strata-pvr-scheduler
./strata-pvr service operator initscript > strata-pvr-operator
./strata-pvr service wui initscript > strata-pvr-wui
```

生成したスクリプトは内容を確認してから導入してください。

## データとバックアップ

- `data/config.json`: Strata の設定
- `data/strata.db`: 番組表、予約、録画状態、録画履歴の正本
- 録画ファイル: 設定した録画ディレクトリ
- `data/.cache/previews/`: 録画済みプレビューのキャッシュ
- `log/`: 互換形式のログ
- `backup/`: Chinachu からの移行時に保存される旧データとレポート

SQLite は WAL モードを使用するため、稼働中に `strata.db` だけをコピーする
のは避けてください。バックアップや復旧は、停止中に `data/` ディレクトリ
全体を対象に行ってください。

## ディレクトリ構成

```text
cmd/strata-pvr/     バイナリのエントリポイント
internal/cli/       CLI、互換チェック、service コマンド
internal/config/    設定の読み込みと既定値
internal/database/  SQLite ストレージ
internal/legacy/    旧形式の型、ルール評価、録画ファイル名整形
internal/mirakurun/ Mirakurun クライアント
internal/scheduler/ 番組表取得と予約生成
internal/operator/  録画実行と状態更新
internal/wui/       Web UI / API サーバ
web/                埋め込み対象の Web UI
testdata/           Mirakurun などのテスト fixture
```

## 互換性と注意点

- Mirakurun の URL は `http`、`http+unix`、旧形式の `http://unix:` に対応
- WUI は単一の HTTP リスナーを使用し、Basic 認証を設定できます
- インターネットへ直接公開せず、TLS、VPN、アクセス制限は reverse proxy
  などの外側で管理してください
- 容量不足時の動作は、古い録画の削除または録画停止に限定しています
- 完全な Chinachu 互換を保証するものではありません

Chinachuからの移行条件と設定変換の詳細は、
[MIGRATION_COMPATIBILITY.md](MIGRATION_COMPATIBILITY.md) を参照してください。
