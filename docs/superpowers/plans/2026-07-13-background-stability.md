# Chinachu 相当バックグラウンド安定性改善 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** scheduler/operator の二重起動、WUI force の重複実行、過剰な SQLite ポーリング、scheduler 周期、operator テストの不安定性を解消し、Chinachu 相当のバックグラウンド安定性を確保する。

**Architecture:** `internal/system` に OS 固有の排他的 lock を隠蔽する `ProcessLock` を追加し、scheduler/operator が PID ファイルより先に同じ実行単位の lock を取得する。database 層には due reservation、collection ID、program 単体を読む小さな API を追加し、operator の長時間ループだけが選択的読み込みを使用する。WUI は process-local mutex で force 要求を coalesce し、systemd timer は 10 分周期に揃える。

**Tech Stack:** Go 1.25.0、`database/sql`、modernc.org/sqlite、既存の `golang.org/x/sys`、systemd timer、Go testing/httptest。

## Global Constraints

- 録画の開始条件、競合判定、ファイル名、HTTP API の既存レスポンス形式は変更しない。
- Unix 系は `flock(LOCK_EX|LOCK_NB)`、Windows は `LockFileEx` 相当の排他的ロックを使用する。
- PID ファイルとは別に `<pid-file>.lock` を使用する。
- PID パスが空の場合は、既存のテスト・埋め込み呼び出しとの互換性のため PID ファイルとロックを無効にする。
- WUI force は実行中・coalesce 時とも HTTP 202 を返す。
- SQL の due 条件で候補を絞った後も、最終的な開始可否は既存の `shouldStart` で判定する。
- `RunOnce` の外部から見える一回実行 semantics は維持する。
- `go test -race` が環境上実行できない場合、そのエラーを成功扱いにしない。
- shell コマンドはプロジェクト指示に従い `rtk` を先頭に付ける。

## File Map

- `internal/system/process_lock.go`: 共通 lock 型、sentinel error、取得/解放 API。
- `internal/system/process_lock_unix.go`: Unix の `flock` 実装。
- `internal/system/process_lock_windows.go`: Windows の `LockFileEx` 実装。
- `internal/system/process_lock_test.go`: lock の再取得、競合、stale file テスト。
- `internal/scheduler/scheduler.go`: scheduler の lock/PID ライフサイクル。
- `internal/scheduler/scheduler_test.go`: scheduler lock/PID 回帰テスト。
- `internal/operator/operator.go`: operator の lock、共有 DB、選択的 polling、対象 program abort 監視。
- `internal/operator/operator_test.go`: operator の lock、選択的処理、cancel 回帰テスト。
- `internal/database/reservations.go`: due reservation query。
- `internal/database/reservations_test.go`: due query の境界テスト。
- `internal/database/program_collections.go`: collection ID/program 単体 query。
- `internal/database/program_collections_test.go`: collection query の未存在・collection 分離テスト。
- `internal/reservationstore/store.go`: due reservation の domain decode wrapper。
- `internal/programstore/store.go`: ID 一覧と program 単体の domain decode wrapper。
- `internal/wui/server.go`: scheduler force の process-local coalesce。
- `internal/wui/server_test.go`: force 同時要求の回帰テスト。
- `internal/cli/cli_test.go`: systemd timer 設定の回帰テスト。
- `contrib/systemd/strata-pvr-scheduler.timer`: 10 分周期への変更。

---

### Task 1: OS 対応 ProcessLock を追加する

**Files:**

- Create: `internal/system/process_lock.go`
- Create: `internal/system/process_lock_unix.go`
- Create: `internal/system/process_lock_windows.go`
- Test: `internal/system/process_lock_test.go`

**Interfaces:**

- Produces `system.ErrProcessAlreadyRunning`。
- Produces `system.AcquireProcessLock(path string) (*system.ProcessLock, error)`。
- Produces `(*system.ProcessLock).Close() error`。
- Empty path は no-op lock として扱い、`Close` は nil を返す。

- [ ] **Step 1: 競合と解放を検証する failing test を書く**

```go
func TestAcquireProcessLockRejectsSecondHolderAndAllowsReacquire(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scheduler.pid.lock")
	first, err := AcquireProcessLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	if _, err := AcquireProcessLock(path); !errors.Is(err, ErrProcessAlreadyRunning) {
		t.Fatalf("second acquire error = %v, want ErrProcessAlreadyRunning", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := AcquireProcessLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := third.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAcquireProcessLockReusesStaleLockFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operator.pid.lock")
	if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	lock, err := AcquireProcessLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: テストが期待どおり RED になることを確認する**

Run: `rtk go test ./internal/system -run "TestAcquireProcessLock" -count=1`

Expected: `FAIL`。`AcquireProcessLock` または `ErrProcessAlreadyRunning` が未定義でコンパイルに失敗する。

- [ ] **Step 3: 共通 API を実装する**

`internal/system/process_lock.go` に、lock file の親ディレクトリを作成し、OS 固有の `lockFile` を呼ぶ実装を追加する。

```go
var ErrProcessAlreadyRunning = errors.New("process already running")

type ProcessLock struct {
	file   *os.File
	unlock func() error
}

func AcquireProcessLock(path string) (*ProcessLock, error) {
	if path == "" {
		return &ProcessLock{}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	unlock, err := lockFile(file)
	if err != nil {
		_ = file.Close()
		if isLockUnavailable(err) {
			return nil, ErrProcessAlreadyRunning
		}
		return nil, err
	}
	return &ProcessLock{file: file, unlock: unlock}, nil
}

func (lock *ProcessLock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	var result error
	if lock.unlock != nil {
		result = lock.unlock()
	}
	if err := lock.file.Close(); result == nil {
		result = err
	}
	lock.file = nil
	lock.unlock = nil
	return result
}
```

- [ ] **Step 4: Unix/Windows の最小実装を追加して GREEN にする**

Unix では `syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)` を使い、`EAGAIN`/`EWOULDBLOCK` を `isLockUnavailable` で判定する。Windows では `golang.org/x/sys/windows` の `LockFileEx` に `LOCKFILE_EXCLUSIVE_LOCK|LOCKFILE_FAIL_IMMEDIATELY` を渡し、`Overlapped` を unlock closure が保持する。どちらも成功時は `func() error` の解放関数を返し、lock 取得失敗時は開いた file を閉じる。

Run: `rtk go test ./internal/system -run "TestAcquireProcessLock" -count=1`

Expected: `ok   strata-pvr/internal/system`。

- [ ] **Step 5: lock package の全テストを実行する**

Run: `rtk go test ./internal/system -count=1`

Expected: `ok`、fail 0。

- [ ] **Step 6: コミットする**

```text
rtk git add internal/system/process_lock.go internal/system/process_lock_unix.go internal/system/process_lock_windows.go internal/system/process_lock_test.go
rtk git commit -m "feat: add cross-process runtime lock"
```

### Task 2: scheduler/operator の PID と ProcessLock を統合する

**Files:**

- Modify: `internal/scheduler/scheduler.go`
- Test: `internal/scheduler/scheduler_test.go`
- Modify: `internal/operator/operator.go`
- Test: `internal/operator/operator_test.go`

**Interfaces:**

- Consumes `system.AcquireProcessLock` and `system.ErrProcessAlreadyRunning` from Task 1.
- Produces `acquireProcessLock(pidPath string) (*system.ProcessLock, error)` in each package, returning nil for an empty PID path.
- `Run` in both packages acquires the lock before PID write and releases it after PID cleanup.

- [ ] **Step 1: package integration の failing test を書く**

各 package の test に、実行 lock の二重取得と empty PID の互換性を追加する。

```go
func TestAcquireProcessLockUsesPIDLockPath(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "data", "scheduler.pid")
	first, err := acquireProcessLock(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := acquireProcessLock(pidPath); !errors.Is(err, system.ErrProcessAlreadyRunning) {
		t.Fatalf("second scheduler lock error = %v", err)
	}
}

func TestAcquireProcessLockAllowsEmptyPIDPath(t *testing.T) {
	lock, err := acquireProcessLock("")
	if err != nil {
		t.Fatal(err)
	}
	if lock != nil {
		defer lock.Close()
	}
}
```

operator 側は同じ二つの振る舞いを `operator` package 内で検証する。

- [ ] **Step 2: integration test が RED になることを確認する**

Run: `rtk go test ./internal/scheduler ./internal/operator -run "TestAcquireProcessLock" -count=1`

Expected: `FAIL`。package-local helper が未定義でコンパイルに失敗する。

- [ ] **Step 3: scheduler の lock 順序を実装する**

`scheduler.Run` の冒頭で `acquireProcessLock(paths.PID)` を呼び、成功後に既存の `writePIDFile` を実行する。defer の順序は PID 削除、lock 解放の順にする。`acquireProcessLock` は `filepath.Clean(paths.PID) + ".lock"` ではなく、`paths.PID + ".lock"` をそのまま利用し、既存 PID パスの相対/絶対表現を壊さない。

既存の `removePIDFile` は PID ファイルの内容を読み、自分の PID と一致した場合だけ削除する。これにより、異常終了後の stale PID や別実行の PID を誤って削除しない。

- [ ] **Step 4: operator の lock 順序を実装する**

`operator.Run` も `initializeRuntimeState` より前に同じ lock を取得する。二重起動時は config/database/runtime state に触れず、`system.ErrProcessAlreadyRunning` を返す。正常終了時は scheduler と同じく PID cleanup 後に lock を Close する。

- [ ] **Step 5: integration test が GREEN になることを確認する**

Run: `rtk go test ./internal/scheduler ./internal/operator -run "TestAcquireProcessLock|TestPIDFileLifecycle" -count=1`

Expected: `ok`、PID lifecycle と二重 lock の fail 0。

- [ ] **Step 6: package 回帰テストを実行する**

Run: `rtk go test ./internal/scheduler ./internal/operator -count=1`

Expected: `ok`。既存の scheduler reserve test と overlapping recording test が通る。

- [ ] **Step 7: コミットする**

```text
rtk git add internal/scheduler/scheduler.go internal/scheduler/scheduler_test.go internal/operator/operator.go internal/operator/operator_test.go
rtk git commit -m "feat: prevent duplicate scheduler and operator runs"
```

### Task 3: database/store に選択的読み込み API を追加する

**Files:**

- Modify: `internal/database/reservations.go`
- Test: `internal/database/reservations_test.go`
- Modify: `internal/database/program_collections.go`
- Test: `internal/database/program_collections_test.go`
- Modify: `internal/reservationstore/store.go`
- Modify: `internal/programstore/store.go`

**Interfaces:**

- Produces `database.ReadReservationsDue(ctx, db, startBefore, endAfter) ([]json.RawMessage, error)`。
- Produces `database.ReadProgramIDs(ctx, db, collection) ([]string, error)`。
- Produces `database.ReadProgramByID(ctx, db, collection, programID) (json.RawMessage, bool, error)`。
- Produces `reservationstore.ReadDue(ctx, databasePath string, startBefore, endAfter int64) ([]legacy.Program, error)`。
- Produces `programstore.ReadIDs(ctx, databasePath, collection string) ([]string, error)`。
- Produces `programstore.ReadByID(ctx, databasePath, collection, programID string) (legacy.Program, bool, error)`。

- [ ] **Step 1: database query の failing test を書く**

既存の database test helper で一時 DB を作り、次の境界を検証する。

```go
func TestReadReservationsDueFiltersByStartAndEnd(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "strata.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	reservations := []ReservationDocument{
		{ProgramID: "past", Start: 100, End: 199, Document: json.RawMessage(`{"id":"past"}`)},
		{ProgramID: "due", Start: 900, End: 1100, Document: json.RawMessage(`{"id":"due"}`)},
		{ProgramID: "future", Start: 1200, End: 1300, Document: json.RawMessage(`{"id":"future"}`)},
	}
	if err := ReplaceReservations(ctx, db, reservations); err != nil {
		t.Fatal(err)
	}
	documents, err := ReadReservationsDue(ctx, db, 1010, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(documents[0]); got != `{"id":"due"}` {
		t.Fatalf("due document = %s", got)
	}
}

func TestReadProgramByIDDoesNotCrossCollections(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "strata.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, collection := range []string{"recording", "recorded"} {
		if err := ReplaceProgramCollection(ctx, db, collection, []ProgramDocument{{ProgramID: "same", Document: json.RawMessage(`{"collection":"` + collection + `"}`)}}); err != nil {
			t.Fatal(err)
		}
	}
	document, found, err := ReadProgramByID(ctx, db, "recording", "same")
	if err != nil || !found || !strings.Contains(string(document), `"recording"`) {
		t.Fatalf("recording lookup = %s, %v, %v", document, found, err)
	}
	_, found, err = ReadProgramByID(ctx, db, "recording", "missing")
	if err != nil || found {
		t.Fatalf("missing lookup = found=%v err=%v", found, err)
	}
}
```

- [ ] **Step 2: query test が RED になることを確認する**

Run: `rtk go test ./internal/database -run "TestReadReservationsDue|TestReadProgramByID" -count=1`

Expected: `FAIL`。新しい関数が未定義でコンパイルに失敗する。

- [ ] **Step 3: reservation due query を実装する**

`ReadReservationsDue` は `WHERE start_at <= ? AND end_at > ? ORDER BY position` を使用する。`startBefore` には operator から `now + recordStartMargin`、`endAfter` には `now` を渡す。rows の scan/rows.Err を既存 `ReadReservations` と同じエラー形式で処理する。

- [ ] **Step 4: program collection query と store wrapper を実装する**

`ReadProgramIDs` は `SELECT program_id FROM program_collections WHERE collection = ? ORDER BY position`、`ReadProgramByID` は `SELECT document_json ... WHERE collection = ? AND program_id = ?` を使う。`sql.ErrNoRows` は `(nil, false, nil)` に変換し、その他のエラーは collection と ID を含めて返す。store wrapper は JSON decode して domain 型に変換し、`found=false` をそのまま返す。

- [ ] **Step 5: database/store テストを GREEN にする**

Run: `rtk go test ./internal/database ./internal/programstore ./internal/reservationstore -count=1`

Expected: `ok`、due 境界、collection 分離、未存在結果、既存 replace/read test がすべて通る。

- [ ] **Step 6: コミットする**

```text
rtk git add internal/database/reservations.go internal/database/reservations_test.go internal/database/program_collections.go internal/database/program_collections_test.go internal/reservationstore/store.go internal/programstore/store.go
rtk git commit -m "feat: add selective reservation and program queries"
```

### Task 4: operator の長時間 polling と cancel 経路を軽量化する

**Files:**

- Modify: `internal/operator/operator.go`
- Test: `internal/operator/operator_test.go`

**Interfaces:**

- Consumes `reservationstore.ReadDue`, `programstore.ReadIDs`, and `programstore.ReadByID` from Task 3.
- `startPendingRecordings` は due reservation と recording ID set を使う。
- `watchAbortFlag` は `programstore.ReadByID` で対象 ID だけを監視する。
- `RunOnce` は database path がある場合に一つの `*sql.DB` を context に付与して既存 semantics を維持する。

- [ ] **Step 1: due/ID/対象 abort の振る舞いを検証する failing test を書く**

既存の overlapping recording と abort テストを保持し、対象 ID だけの abort 判定を追加する。

```go
func TestAbortSkippedRecordingsDoesNotStopManualOrUnrelatedRecording(t *testing.T) {
	dir := t.TempDir()
	paths := seedOperatorTestDatabase(t, testPaths{
		Reserves: filepath.Join(dir, "data", "reserves.json"),
		Recording: filepath.Join(dir, "data", "recording.json"),
		Recorded: filepath.Join(dir, "data", "recorded.json"),
	})
	reserves := []legacy.Program{
		{ID: "skip", IsSkip: true},
		{ID: "manual", IsSkip: true, IsManualReserved: true},
	}
	recording := []legacy.Program{
		{ID: "skip"},
		{ID: "manual", IsManualReserved: true},
		{ID: "other"},
	}
	if err := programstore.Write(context.Background(), paths.Database, programstore.Recording, recording); err != nil {
		t.Fatal(err)
	}
	updated, err := abortSkippedRecordings(context.Background(), paths.Database, reserves, []string{"skip", "manual", "other"})
	if err != nil || updated != 1 {
		t.Fatalf("updated=%d err=%v", updated, err)
	}
	if err := readOperatorTestPrograms(paths, programstore.Recording, &recording); err != nil {
		t.Fatal(err)
	}
	if !recording[0].Abort || recording[1].Abort || recording[2].Abort {
		t.Fatalf("recording abort states = %#v", recording)
	}
}
```

既存の `TestAbortSkippedRecordingsStopsOnlySkippedAutoReserves` は、recording slice を `[]string{"skip", "keep", "manual"}` の ID slice に置き換え、結果確認だけは従来どおり collection を読み取って行う。

既存の `TestRunOnceFinalizesActiveRecordingWhenContextIsCancelled` は、goroutine 内で `testing.T` を操作しないようにし、worker goroutine からは結果だけを channel に返す。最終確認の各 DB read error は無視せず `t.Fatal` で報告する。

- [ ] **Step 2: 新しい operator test が RED になることを確認する**

Run: `rtk go test ./internal/operator -run "TestAbortSkippedRecordingsDoesNotStopManualOrUnrelatedRecording" -count=1`

Expected: `FAIL`。新しい `abortSkippedRecordings` の ID set 署名と実装がまだ存在しないためコンパイルに失敗する。

- [ ] **Step 3: due reservation と recording ID set に置き換える**

`startPendingRecordings` の冒頭を次の形にする。

```go
startBefore := now.Add(recordStartMargin).UnixMilli()
endAfter := now.UnixMilli()
reserves, err := reservationstore.ReadDue(ctx, paths.Database, startBefore, endAfter)
if err != nil {
	return err
}
recordingIDs, err := programstore.ReadIDs(ctx, paths.Database, programstore.Recording)
if err != nil {
	return err
}
if stopped, err := abortSkippedRecordings(ctx, paths.Database, reserves, recordingIDs); err != nil {
	return err
} else if stopped > 0 {
	if err := logging.AppendLine(paths.Log, "ABORT: skipped recordings=%d", stopped); err != nil {
		return err
	}
}
```

`shouldStart` の重複判定用に `map[string]struct{}` を作り、reserve を DB に upsert した直後に map へ追加する。候補取得後も既存の `shouldStart` の skip/end/start margin 判定を実行する。

- [ ] **Step 4: abortSkippedRecordings を対象 ID のみ読む実装にする**

各 due reserve について、`IsSkip`、`IsManualReserved`、recording ID set の条件を先に確認する。対象だけ `programstore.ReadByID` し、found かつ未 abort の program に `Abort=true` を設定して upsert する。missing program はすでに終了したものとして無視し、別 program の全件読み込みは行わない。

- [ ] **Step 5: low-storage 読み込みを threshold 有効時だけに限定する**

`cfg.StorageLowSpaceThresholdMB <= 0` の通常時は recording/recorded の full read をしない。threshold が正の場合だけ既存の `handleLowStorage` が必要とする full slice を読み込む。`RunOnce` は既存の全件 semantics を維持し、開始時に共有 DB handle を context に付与する。

`RunOnce` の先頭では `strata-pvr/internal/database` を import し、database path が空でない場合だけ次を実行して、処理末尾まで同じ handle を使う。

```go
if paths.Database != "" {
	db, err := database.Open(ctx, paths.Database)
	if err != nil {
		return Result{}, err
	}
	defer db.Close()
	ctx = database.WithHandle(ctx, db)
}
```

- [ ] **Step 6: watchAbortFlag を program 単体 query に置き換える**

500ms ticker の処理を `programstore.ReadByID(ctx, databasePath, programstore.Recording, programID)` に変更する。`found && program.Abort` のときだけ `aborted.Store(true)`、cancel、stream close を実行する。DB read error と not found は次の tick に進み、現在の「録画全体を異常終了させない」動作を維持する。

- [ ] **Step 7: operator 回帰テストを GREEN にする**

Run: `rtk go test ./internal/operator -run "TestStartPendingRecordings|TestAbortSkippedRecordings|TestRunOnceFinalizesActiveRecordingWhenContextIsCancelled|TestRunOnceStopsWhenRecordingAbortIsSet" -count=1`

Expected: `ok`。overlapping recording、対象 abort、context cancellation の各テストが通る。

- [ ] **Step 8: operator テストを反復実行する**

Run: `rtk go test ./internal/operator -count=10`

Expected: `ok` が 10 回分続き、SQLite read error や timeout がない。

- [ ] **Step 9: コミットする**

```text
rtk git add internal/operator/operator.go internal/operator/operator_test.go
rtk git commit -m "perf: reduce operator background database polling"
```

### Task 5: WUI force の coalesce と scheduler timer を適用する

**Files:**

- Modify: `internal/wui/server.go`
- Test: `internal/wui/server_test.go`
- Test: `internal/cli/cli_test.go`
- Modify: `contrib/systemd/strata-pvr-scheduler.timer`

**Interfaces:**

- `server` に `schedulerMu sync.Mutex` を追加する。
- `handleSchedulerForce` は `TryLock` 失敗時も HTTP 202 と `{}` を返す。
- `strata-pvr-scheduler.timer` の `OnUnitActiveSec` は `10min` になる。

- [ ] **Step 1: 同時 force 要求の failing test を書く**

既存の `TestAPISchedulerNoLogAndForce` を blocking callback に拡張する。

```go
started := make(chan struct{})
release := make(chan struct{})
var calls atomic.Int32
paths.Scheduler = func(_ context.Context, _ bool) error {
	if calls.Add(1) == 1 {
		close(started)
		<-release
	}
	return nil
}
first := httptest.NewRecorder()
firstDone := make(chan struct{})
go func() {
	defer close(firstDone)
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodPut, "/api/scheduler/force", nil))
}()
select {
case <-started:
case <-time.After(time.Second):
	t.Fatal("first scheduler force did not start")
}
second := httptest.NewRecorder()
handler.ServeHTTP(second, httptest.NewRequest(http.MethodPut, "/api/scheduler/force", nil))
if second.Code != http.StatusAccepted || second.Body.String() != `{}` {
	t.Fatalf("coalesced force = %d %q", second.Code, second.Body.String())
}
if got := calls.Load(); got != 1 {
	t.Fatalf("scheduler callback calls=%d, want 1", got)
}
close(release)
select {
case <-firstDone:
case <-time.After(time.Second):
	t.Fatal("first scheduler force did not finish")
}
if first.Code != http.StatusAccepted || first.Body.String() != `{}` {
	t.Fatalf("first force = %d %q", first.Code, first.Body.String())
}
```

`atomic.Int32` を使うため test import に `sync/atomic` を追加する。

- [ ] **Step 2: WUI test が RED になることを確認する**

Run: `rtk go test ./internal/wui -run "TestAPISchedulerNoLogAndForce" -count=1`

Expected: `FAIL`。現状は二つの force goroutine が callback を二回実行する。

- [ ] **Step 3: server に process-local mutex を追加する**

`server` struct に `schedulerMu sync.Mutex` を追加し、force handler の method check 後に次を実行する。

```go
if !s.schedulerMu.TryLock() {
	writeCompactJSON(w, http.StatusAccepted, map[string]any{})
	return
}
go func() {
	defer s.schedulerMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	_ = s.runScheduler(ctx, false)
}()
writeCompactJSON(w, http.StatusAccepted, map[string]any{})
```

通常の scheduler.Run が持つ process lock は変更せず、WUI 内部と外部 process の両方を防ぐ。

- [ ] **Step 4: WUI test を GREEN にする**

Run: `rtk go test ./internal/wui -run "TestAPISchedulerNoLogAndForce" -count=1`

Expected: `ok`。callback calls は 1、二つの HTTP 応答はともに `202` と `{}`。

- [ ] **Step 5: timer を 10 分へ変更し、設定テストを追加する**

`contrib/systemd/strata-pvr-scheduler.timer` の一行を次のように変更する。

```ini
OnUnitActiveSec=10min
```

既存の CLI/systemd テストの近くに timer file を読み、`OnBootSec=2min`、`OnUnitActiveSec=10min`、`AccuracySec=1min`、`Persistent=true`、`Unit=strata-pvr-scheduler.service` を確認するテストを追加する。テストは repository root からの相対パスに依存せず、`runtime.Caller` からプロジェクト root を求める既存 test helper があればそれを使う。

- [ ] **Step 6: WUI と timer の回帰テストを実行する**

Run: `rtk go test ./internal/wui ./internal/cli -count=1`

Expected: `ok`。既存 scheduler API の同期 PUT の動作は変わらず、force だけが coalesce される。

- [ ] **Step 7: コミットする**

```text
rtk git add internal/wui/server.go internal/wui/server_test.go contrib/systemd/strata-pvr-scheduler.timer
rtk git commit -m "fix: coalesce scheduler force and shorten timer"
```

### Task 6: 全体検証と差分レビューを行う

**Files:**

- Modify: none unless verification exposes a test defect.
- Inspect: all files changed by Tasks 1–5.

**Interfaces:**

- Verifies every success condition in `docs/superpowers/specs/2026-07-13-background-stability-design.md`.

- [ ] **Step 1: format と差分整合性を確認する**

Run: `rtk gofmt -w internal/system/process_lock.go internal/system/process_lock_unix.go internal/system/process_lock_windows.go internal/scheduler/scheduler.go internal/operator/operator.go internal/database/reservations.go internal/database/program_collections.go internal/reservationstore/store.go internal/programstore/store.go internal/wui/server.go`

Then run: `rtk git diff --check`。

Expected: format command succeeds and `git diff --check` has no output.

- [ ] **Step 2: code-review graph を更新して影響範囲を確認する**

Call `build_or_update_graph_tool` with repository `H:\sourcecode\Chinachu-Go`, then call `detect_changes` with `detail_level="minimal"` and `get_affected_flows` for the changed files. Confirm scheduler, operator, WUI force, and database query flows are included; unrelated UI flows may be impacted by shared WUI server structure but must have no behavior changes.

- [ ] **Step 3: 全テストを実行する**

Run: `rtk go test ./...`

Expected: exit code 0、fail 0。

- [ ] **Step 4: 高反復バックグラウンドテストを実行する**

Run: `rtk go test -count=10 ./internal/system ./internal/database ./internal/operator ./internal/wui`

Expected: exit code 0、SQLite read error、timeout、race-like intermittent failure がない。

- [ ] **Step 5: static/build verification を実行する**

Run: `rtk go vet ./...`

Expected: exit code 0、診断なし。

Run: `rtk go build ./...`

Expected: exit code 0、全 package が build される。

- [ ] **Step 6: race detector の可否を記録する**

Run: `rtk go test -race ./internal/system ./internal/database ./internal/operator ./internal/wui`

Expected: 成功なら race 検証済みと記録する。環境エラーならそのエラー全文を記録し、race が通ったとは報告しない。

- [ ] **Step 7: 最終差分をレビューする**

Run: `rtk git status --short` and `rtk git diff HEAD~5 --stat`。

Expected: 変更は設計書の File Map と一致し、未追跡の一時 lock/DB/log/test artifact がない。不要な依存追加や HTTP schema 変更がない。
