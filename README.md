# Strata PVR

Strata PVR は、Mirakurun を録画バックエンドとして利用する、Go 製の
シンプルな PVR です。

Chinachu を使ってきたユーザーが、これまでの操作感や設定の考え方を大きく
変えずに移行できることを目指しています。  
Node.js などの実行環境を別途用意せず、単一のバイナリを配置するだけで導入・稼働します。
 (Mirakurunを利用する場合はどうしても必要になるかとは思いますが、古いNodeJSと共存させる事は回避出来るはずです。)

## 開発の背景と方針

- Chinachu の利用者が次に移行できる受け皿になればいい
- Chinachu の操作方法や設定の感覚を、できる限りそのまま残したい
- Chinachu の機能で個人的に使わない機能は外し、録画・予約・番組表・管理に必要な機能へ絞る
- Node.js、npm など外部ランタイムの影響を避ける
- 配布物は単一バイナリを中心にし、導入と運用を簡単にする

Chinachu の完全な後継や全機能互換を目指すものではありません。  
既存データを移行しやすくし、日常の録画運用で必要最低限の機能を提供することを優先しています。

## 主な機能
- 基本的にはChinachuと変わりません。
- Chinachu のJSON系データファイルはSQLiteで管理されます。

## 必要なもの

- Mirakurun / mirakc (未検証)
- 配布済みバイナリを使う場合: 実行対象 OS 用の Strata PVR バイナリ
- 視聴やプレビューを使う場合: `ffmpeg` / `ffprobe`
- ソースからビルドする場合: Go 1.25 以上

## ビルド

```sh
go test ./...
sh ./scripts/build.sh
```

Windows での稼働は推奨していませんが、ビルドして動作させることは可能です。  
`go` が PATH にない場合は、Go のインストール先を直接指定できます。

```powershell
& "C:\Program Files\Go\bin\go.exe" test ./...
& .\scripts\build.ps1
```

ビルド時のバージョンは `0.1.0-dev.<コミット数>+<短縮SHA>` の形式で自動生成されます。

## 初回セットアップ

### 新規環境
※Chinachuから移行する場合はこの手順はスキップして、`Chinachu から移行する場合`を参照ください。

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

移行後はWeb UIで、移行レポートの件数と番組表・予約・録画の内容を確認して
ください。

移行の互換範囲は [MIGRATION_COMPATIBILITY.md](MIGRATION_COMPATIBILITY.md) に
まとめています。旧設定のうち TLS、GeoIP、mDNS、Twitter 通知、外部フック
などは移行対象外です。

## 起動

初期化または移行を完了した作業ディレクトリで、必要なプロセスを起動します。

```sh
./strata-pvr run scheduler
./strata-pvr run operator
./strata-pvr run wui
```

- `scheduler`: Mirakurun から番組表を取得し、予約を更新します
- `operator`: 予約に従って録画を実行します
- `wui`: Web UI と API を提供します

最初は別々のターミナルで起動し、動作を確認してから systemd などのサービス
へ組み込むことを推奨します。

## Web UI

```sh
./strata-pvr run wui
```

Web UI では、次の操作を行えます。

- ダッシュボード、番組表、予約、録画、ルールの確認
- 番組検索（旧Chinachuの `#!/search/top/.../` 互換URLを含む）
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

視聴ルートは必要なときに `ffmpeg` を起動します。実行ファイルが
見つからない場合は、該当する機能だけが `503 Service Unavailable` になります。

番組検索APIはStrata形式の拡張子なしルート `GET /api/search` です。`title`、
`description`（旧名 `desc`）、`category`（旧名 `cat`）、`type`、`programID`
（旧名 `pgid`）、`channelID`（旧名 `chid`）、`startHour` / `endHour`
（旧名 `start` / `end`）で検索できます。レスポンスは開始時刻順の番組JSON配列です。

## サービスとしての運用

systemd 用の unit ファイル例を `contrib/systemd/` に用意しています。
`operator` と `wui` は常駐サービス、`scheduler` は timer から起動する
one-shot サービスとして運用できます。詳しくは
[contrib/systemd/README.md](contrib/systemd/README.md) を参照してください。

## データとバックアップ

- `data/config.json`: Strata の設定
- `data/strata.db`: 番組表、予約、録画状態、録画履歴の正本
- 録画ファイル: 設定した録画ディレクトリ
- `data/.cache/previews/`: 録画済みプレビューのキャッシュ
- `log/`: 1 行 1 レコードの構造化ログ（`timestamp|level=...|event=...|key=value`）。時刻はOSのローカルタイムゾーンを使用します。
  既存の本文は `message` フィールドに保持されるため、Web UI ではそのまま確認できます。
- `backup/`: Chinachu からの移行時に保存される旧データとレポート

SQLite は WAL モードを使用するため、稼働中に `strata.db` だけをコピーする
のは避けてください。バックアップや復旧は、停止中に `data/` ディレクトリ
全体を対象に行ってください。

## ディレクトリ構成

```text
cmd/strata-pvr/     バイナリのエントリポイント
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

----
本アプリは開発中です。  
本アプリによって生じた損害に関して、一切の責任を負いかねますのでご了承ください。
