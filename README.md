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
- Basic 認証、公開リスナー、TLS PEM 証明書に対応した WUI/API サーバ
- 録画済み/録画中/チャンネルの M2TS、MP4、XSPF、プレビュー配信
- 旧 API 互換の予約、録画、ルール、設定、ログ、ストレージ系エンドポイント
- Node.js や npm に依存しないネイティブ Web UI

互換性の詳細と残リスクは
`MIGRATION_COMPATIBILITY.md` と `UNIMPLEMENTED_REMAINING.md` を確認して
ください。現時点で既知の必須ランタイム未実装項目はありませんが、CLI 表示
の細部、JSON の未知フィールド順、JavaScript 正規表現や dateformat の
細かい差分は oracle test レベルの残リスクとして扱っています。

## 必要なもの

- Go 1.22 以上
- Mirakurun
- MP4 再生、プレビュー、変換配信を使う場合は `ffmpeg` と `ffprobe`

通常の実行に Node.js、npm、webpack は不要です。旧 Node 時代の
`installer`、`updater`、`test <app>`、`ircbot` 相当の自動処理は Go
ランタイムでは意図的に実行しません。

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

`config.json`、`rules.json`、`data/` がある PVR 作業ディレクトリで
バイナリを実行します。Strata PVR は Mirakurun の設定やチューナー設定を
置き換えません。

```sh
./strata-pvr update
./strata-pvr reserves
./strata-pvr service scheduler execute
./strata-pvr service operator execute
./strata-pvr service wui execute
```

`service ... execute` は、`config.json` または `rules.json` がない場合に
同梱の `config.sample.json`、`rules.sample.json` をコピーし、`data/` と
`log/` を作成します。

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

`./strata-pvr service wui execute` で Go 製 WUI/API サーバを起動します。
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

## 互換性チェックと移行補助

本番ディレクトリで置き換えを検討する前に、まず互換性チェックを実行して
ください。

```sh
./strata-pvr compat check
./strata-pvr compat doctor
```

`compat doctor` は `compat check` の内容に加えて、Mirakurun URL、録画
ディレクトリ、WUI リスナー、ストレージ方針、状態ファイル件数、推奨される
次の確認コマンドを表示します。録画中データがある場合は、ラッパーや
サービスの置き換えを避けるよう警告します。

状態ファイルのバックアップ、差分確認、既存コマンドを Go バイナリへ転送
する安全なシェルラッパー生成も用意しています。

```sh
./strata-pvr compat backup
./strata-pvr compat diff
./strata-pvr compat wrapper > strata-pvr-wrapper
```

init script は標準出力に生成されます。既存サービスを直接上書きせず、別名で
保存して内容を確認してください。

```sh
./strata-pvr service scheduler initscript > strata-pvr-scheduler
./strata-pvr service operator initscript > strata-pvr-operator
./strata-pvr service wui initscript > strata-pvr-wui
```

## 推奨する初回検証手順

```sh
./strata-pvr compat backup
./strata-pvr compat doctor
./strata-pvr update -s
./strata-pvr reserves
./strata-pvr service wui execute
./strata-pvr service operator execute
```

WUI と operator は最初は別ターミナルで手動起動し、次のファイルが期待どおり
更新されることを確認してください。

- `log/wui`
- `log/operator`
- `data/reserves.json`
- `data/recording.json`
- `data/recorded.json`

問題がないことを確認してから、生成した init script や互換ラッパーを
導入してください。

## 設定上の注意

- `config.sample.json` の `wuiUsers` はサンプル資格情報です。公開前に必ず
  変更してください。
- `wuiOpenServer` は未認証の公開リスナーです。信頼できるネットワークに
  バインドするか、不要なら無効化してください。
- `wuiAllowCountries` の GeoIP 国フィルタ、`wuiMdnsAdvertisement` の mDNS
  広告は Go ランタイムでは意図的に実装していません。必要な制限は
  firewall、reverse proxy、VPN、Basic 認証で行ってください。
- TLS は PEM の key/cert/CA を対象にしています。PFX/P12 は PEM へ変換して
  使用してください。
- 旧 Twitter/Tweeter 通知フィールドは読み込みと保存互換のため保持しますが、
  旧 Twitter API が利用できないため送信機能としては扱いません。

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
