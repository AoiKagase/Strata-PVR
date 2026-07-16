# Tasks

## 録画終了時の外部コマンド実行

### Goal

録画が正常に終了した際、ユーザー設定の外部コマンドを実行できるようにする。録画後のファイル移動、通知、変換などを自動化できるようにする。

### Implementation tasks

- [ ] config に録画終了後コマンドの指定を追加する。
- [ ] 実行時に録画ファイルのパス、番組情報、録画結果を安全に引き渡せるようにする。
- [ ] 録画成功時のみ実行するか、失敗・中断時にも実行できるかの動作を定義する。
- [ ] コマンドの終了コード・標準出力・標準エラーをログに記録する。
- [ ] コマンド実行失敗が録画結果を不正に変更しないようにする。
- [ ] タイムアウトおよび並列実行数の扱いを定義する。
- [ ] `config.sample.json` と `README.md` に設定例・注意事項を追記する。

### Acceptance criteria

- [ ] 録画終了後、設定したコマンドが対象録画ごとに一度だけ実行される。
- [ ] コマンドから録画ファイルと必要な番組情報を参照できる。
- [ ] コマンドの失敗やタイムアウトが明確にログ出力され、録画済みファイルは保持される。
- [ ] 設定未指定時は外部コマンドを実行しない。

## Embedded subtitles in MP4 playback

### Goal

録画再生時に、映像と字幕を同一のタイムラインで扱い、シーク後も字幕位置を正確に同期させる。

### Investigation tasks

- [ ] `watch.mp4` に ARIB 字幕を同時にエンコードして MP4 の字幕ストリームとして格納できるか検証する。
- [ ] Chrome で MP4 内の `mov_text` または WebVTT 字幕を表示・切替できるか検証する。
- [ ] ブラウザ互換性が不足する場合、外部 WebVTT を維持したまま映像とのシーク基準を正確に同期する方法を検討する。
- [ ] 字幕焼き込みを代替手段として評価する（字幕オン・オフ不可になる点を明記する）。

### Acceptance criteria

- [ ] 録画再生の開始・シーク・画質変更後も、字幕と音声・映像のずれが実用上認識できない。
- [ ] Chrome で字幕の表示可否と切替が一貫して動作する。

## Web UI assets distribution

### Goal

配布時は単一バイナリで Web UI が動作し、開発時や上級ユーザー向けには外部 `web/` ディレクトリで上書きできるようにする。

現状は `web/index.html`、`web/styles.css`、`web/app.js` を静的ファイルとして扱うため、配布形態によってはファイル配置漏れや差し替え手順が発生する。標準配布では Go の `embed` を使って Web UI assets をバイナリへ同梱し、必要な場合だけ外部ディレクトリを優先する。

### Proposed behavior

- デフォルトでは埋め込み済み Web UI assets を配信する。
- 設定または起動オプションで外部 Web UI ディレクトリを指定できる。
- 外部ディレクトリが指定された場合は、存在確認後に外部ファイルを優先して配信する。
- 起動ログに利用中の asset source を出す。
  - `web assets: embedded`
  - `web assets: external path=<path>`
- 外部ディレクトリ指定が不正な場合は、明示的にエラーにする。黙って embedded に戻さない。

### Implementation tasks

- [x] `internal/wui` に embedded assets 用の `embed.FS` を追加する。
- [x] `web/` 配下の静的ファイルを Go バイナリへ埋め込む。
- [x] WUI サーバの静的ファイル配信を、embedded FS と外部 directory FS の両方に対応させる。
- [x] config に外部 Web UI ディレクトリ指定を追加する。
  - 候補名: `wuiWebDir`
- [x] CLI option が必要か検討する。
  - 候補: `--web-dir`
  - config と CLI の両方を持つ場合は CLI を優先する。
- [x] 起動ログへ asset source を出力する。
- [x] `README.md` に配布時と開発時の Web UI assets の扱いを追記する。
- [x] `config.sample.json` に `wuiWebDir` の例を追加する。

### Acceptance criteria

- [x] `web/` ディレクトリが無い配布先でも WUI が起動し、`/`、`/index.html`、`/styles.css`、`/app.js` が返る。
- [x] `wuiWebDir` を指定すると、そのディレクトリの `index.html`、`styles.css`、`app.js` が優先される。
- [x] 不正な `wuiWebDir` は起動時または WUI 初期化時に分かるエラーになる。
- [ ] 既存の API ルーティング `/api/...` と静的ファイル配信が衝突しない。
- [ ] 通常のローカル開発では外部 `web/` を使ってリビルド無しに確認できる運用を維持できる。

### Tests

- [ ] `go test ./...`
- [x] embedded assets のみで `/` が HTML を返すテスト。
- [x] embedded assets のみで `/styles.css` と `/app.js` が適切な Content-Type で返るテスト。
- [x] 外部 `wuiWebDir` 指定時に外部ファイルが embedded より優先されるテスト。
- [x] 不正な外部 directory 指定のエラーテスト。

### Notes

- Web UI を修正した場合、embedded 配布ではバイナリ再ビルドが必要になる。
- 外部 `wuiWebDir` を使う開発・カスタム運用では、ブラウザ再読み込みだけで HTML/CSS/JS の変更を確認できる。
- キャッシュ対策は別途検討する。
  - HTML 側で `/app.js?v=<version>`、`/styles.css?v=<version>` を付ける。
  - または静的配信で `ETag` / `Last-Modified` を適切に返す。
