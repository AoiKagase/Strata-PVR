# Chinachu 相当バックグラウンド安定性改善 設計書

## 目的

Strata PVR の scheduler/operator について、Chinachu と比較して確認された以下の問題を解消する。

- scheduler と operator のプロセス間二重起動
- WUI の scheduler force による同一プロセス内の重複実行
- scheduler の更新間隔が 30 分であること
- operator が短い周期で SQLite の全コレクションを JSON ごと読み込むこと
- 録画中止監視が毎回 recording 全件を読み込むこと
- SQLite ハンドル競合を原因として疑う operator テストの不安定さ

録画の開始条件、競合判定、ファイル名、HTTP API の既存レスポンス形式は変更しない。

## 背景と確認結果

Chinachu は scheduler の PID を確認して実行中の scheduler を再起動せず、operator は最大 10 分周期で scheduler を起動する。

現行実装では scheduler/operator が PID ファイルを単純に上書きしており、PID の存在確認と排他的なプロセス間ロックがない。また、WUI の `/api/scheduler/force` はリクエストごとに goroutine を起動する。

operator は通常 5 秒周期で reserves、recording、recorded を全件読み込み、録画ごとの abort 監視でも 500ms 周期に recording 全件を再読込している。SQLite の database handle は process-owned のものを再利用できる構造になっているため、取得対象と読み込み頻度を絞ることが主な改善点になる。

## 採用するアプローチ

### 1. 共通プロセスロック

`internal/system` に `ProcessLock` を追加する。ロックファイルを開いたまま OS の advisory/exclusive lock を保持し、プロセス終了時は OS がロックを解放する方式とする。

- Unix 系は `flock(LOCK_EX|LOCK_NB)` を使用する。
- Windows は `LockFileEx` 相当の排他的ロックを使用する。
- OS 固有処理は `process_lock_unix.go` と `process_lock_windows.go` に分離する。
- 共通 API は `AcquireProcessLock(path string) (*ProcessLock, error)` と `(*ProcessLock).Close() error` とする。
- 二つ目の取得は `ErrProcessAlreadyRunning` を返す。
- PID ファイルとは別に `<pid-file>.lock` を使用する。異常終了で lock ファイル自体が残っても、次回は OS ロックを再取得できる。

scheduler/operator の `Run` は、設定読み込みや runtime state の初期化より前にロックを取得する。ロック取得後に現在 PID を PID ファイルへ書き込み、終了時は自プロセスの PID のみを削除してからロックを解放する。これにより stale PID ファイルを信頼せず、実際のロック状態を正とする。

PID パスが空の場合は、既存のテスト・埋め込み呼び出しとの互換性のため PID ファイルとロックを無効にする。通常の CLI、systemd、WUI の実行経路では PID パスが必ず設定されることをテストで保証する。

### 2. WUI force の coalesce

`server` に scheduler 実行用 mutex を追加し、`handleSchedulerForce` は `TryLock` に成功した要求だけを goroutine として実行する。実行中の追加要求は既存の `202 Accepted` レスポンスを返して捨てる。scheduler 自体の `ProcessLock` は別プロセスまたは CLI/systemd との競合を防ぐために残す。

テスト用の `s.paths.Scheduler` callback 経路でも同じ mutex を通るため、WUI 内部の重複実行を保証できる。

### 3. scheduler 周期

`contrib/systemd/strata-pvr-scheduler.timer` の `OnUnitActiveSec` を `10min` に変更する。`OnBootSec=2min`、`AccuracySec=1min`、`Persistent=true` は維持する。

### 4. operator の選択的読み込み

既存の SQLite の時間・collection インデックスを利用し、JSON 全件取得を必要な箇所だけに限定する。database 層に次の責務を追加する。

- 指定時刻時点で開始可能性がある reservation の取得
- collection 内の program ID 一覧の取得
- collection 内の指定 ID の program 取得

operator の長時間ループは次の順序に変更する。

1. 開始マージンを含む due reservation のみ取得する。
2. recording collection は ID 一覧だけ取得し、`shouldStart` の重複判定に使う。
3. skip された recording の abort 更新は、対象 ID に絞った取得・更新にする。
4. low-storage threshold が無効な通常時は recorded 全件を読み込まない。threshold が有効な場合だけ、low-storage 処理に必要な recording/recorded の内容を取得する。
5. 録画ごとの abort watcher は対象 program ID の 1 行だけを 500ms 周期で取得する。

`RunOnce` の外部から見える一回実行 semantics は維持する。ただし database path が指定されている場合は一つの `*sql.DB` を開いて context に付与し、その呼び出し中の repository 操作が同一 handle を共有するようにする。これにより短命な SQLite handle の競合を減らし、長時間 operator と同じ接続再利用方針に統一する。

### 5. データ競合とエラー処理

- process lock の取得失敗は scheduler/operator の通常エラーとして呼び出し元へ返す。
- `ErrProcessAlreadyRunning` は重複起動を示す明示的な sentinel error とする。
- WUI force は既存 API 互換のため、実行中・coalesce 時とも HTTP 202 を返す。
- lock を取得できない場合、PID ファイルを更新・削除しない。
- abort watcher の一時的な DB 読み取りエラーは録画全体を異常終了させず、次回 tick で再試行する既存方針を維持する。
- collection の選択的読み込みで `sql.ErrNoRows` が発生した場合は、対象がすでに終了した状態として扱い、別の録画を削除・再作成しない。
- SQL の due 条件で候補を絞った後も、最終的な開始可否は既存の `shouldStart` で判定する。クエリ条件の変更だけで録画条件を変更しない。

## テスト計画

### system

- 同一 lock を二度取得すると `ErrProcessAlreadyRunning` になる。
- 最初の lock を Close した後は再取得できる。
- stale な PID/lock ファイルが存在しても、ロックが解放されていれば取得できる。
- scheduler/operator の Run が二重起動時に PID ファイルを上書きしない。

### database

- due reservation は開始マージン内かつ終了前の行だけ返す。
- collection ID query は collection を跨いだ同名 ID を混同しない。
- 指定 ID query は存在しない場合に明確な未存在結果を返す。

### operator / WUI

- 同時刻の overlapping reservation は従来どおり並列録画される。
- 二つの operator Run は同一 PID path で同時に動作しない。
- abort watcher は対象 program の変更だけを検知し、別 program の abort では停止しない。
- scheduler force を同時に呼んでも callback は一回だけ実行される。
- context cancellation 時の録画完了処理を反復実行しても SQLite 読み取りエラーを隠さず安定して終了する。

### 検証コマンド

実装後に最低限、以下を実行する。

```text
go test ./...
go test -count=10 ./internal/system ./internal/database ./internal/operator ./internal/wui
go vet ./...
go build ./...
```

race detector が環境上実行できない場合は、そのエラーを成功扱いにせず、通常テストと静的検査の結果を分けて報告する。

## 非対象

- scheduler の EPG 取得アルゴリズムや reserve の競合ルールの再設計
- operator の録画ストリーム方式の変更
- systemd 以外の init system の全面的な再設計
- WUI API のレスポンス schema 変更
- event-driven な常駐 operator への全面刷新

## 成功条件

- scheduler/operator が同一パスから二重起動されない。
- WUI force の同時要求で scheduler callback が重複実行されない。
- scheduler timer が 10 分周期になる。
- operator の通常周期で reserves/recording/recorded の全 JSON を毎回読み込まない。
- 対象 abort 監視が program 単位の読み込みになる。
- 既存テストを含む検証コマンドが、環境依存の race detector 以外で再現性をもって成功する。
