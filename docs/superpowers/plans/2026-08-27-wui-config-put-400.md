# WUI設定保存時のHTTP 400修正 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 設定画面が既存のWebセキュリティ設定を欠落させて400になる問題を修正し、設定保存時のフィールド消失も防ぐ。

**Architecture:** 設定画面は編集対象でない設定も現在値から完全な更新ドキュメントへコピーして `PUT /api/config` に送る。WUIの更新APIは、送信された `wuiWebDir` を `config.Document` に渡し、サーバー側の再検証を通過したドキュメントを既存どおりatomic writeする。これにより、非ループバック待受の必須条件を維持したまま保存できる。

**Tech Stack:** Go、`net/http`、既存のStrata config parser、vanilla JavaScript、Go testing/httptest、Playwright。

**Spec:** `docs/superpowers/specs/2026-08-27-wui-config-put-400.md`

## Global Constraints

- `web.trustForwardedHeaders` と `web.trustedProxies` は設定画面保存時に現在値を保持する。
- `wuiWebDir` は設定画面保存時に現在値を保持し、空文字を明示した場合は空文字として保存する。
- `config.ParseDocument` による再検証、パスワードハッシュ保持、APIトークン保持、atomic writeを変更しない。
- 不正JSONや無効な設定は従来どおりHTTP 400で拒否する。
- 実ブラウザ確認はPlaywrightで行い、Goのビルド・テスト成功だけをUI受入完了の根拠にしない。
- shellコマンドはプロジェクト指示に従い `rtk` を先頭に付ける。Goキャッシュのアクセス拒否時は一時GOCACHEを使用する。

## File Map

- `docs/superpowers/specs/2026-08-27-wui-config-put-400.md`: 現象、根拠、受入条件。
- `docs/superpowers/plans/2026-08-27-wui-config-put-400.md`: 実装・検証手順。
- `web/app.js`: 設定フォームの更新payloadへ既存のWebセキュリティ設定と外部Webディレクトリを含める。
- `internal/wui/server.go`: `wuiWebDir`を更新payloadから設定ドキュメントへ渡す。
- `internal/wui/server_test.go`: payloadのフィールド保持とフロントエンド送信契約の回帰テスト。

---

### Task 1: 400再現と設定フィールド保持の failing test を追加する

**Files:**

- Modify: `internal/wui/server_test.go:1426-1450`（設定画面の静的契約）
- Modify: `internal/wui/server_test.go:5476-5533`（設定PUTの保存契約）

**Interfaces:**

- Consumes: `publicStrataConfig`, `config.DefaultDocument`, `newTestHandler`。
- Produces: 設定画面payloadが `trustForwardedHeaders`、`trustedProxies`、`wuiWebDir` を欠落させないこと、およびAPIが `wuiWebDir` を保存することを固定する回帰テスト。

- [ ] **Step 1: フロントエンド送信項目を要求するテストアサーションを書く**

`TestNativeDashboardStrataConfigForm` の `web/app.js` 用 `wants` に次の3項目を追加する。

```go
`trustForwardedHeaders: web.trustForwardedHeaders === true`,
`trustedProxies: Array.isArray(web.trustedProxies) ? web.trustedProxies.slice() : []`,
`wuiWebDir: cfg.wuiWebDir || ""`,
```

- [ ] **Step 2: `wuiWebDir`保持のAPIテストをREDで確認する**

`TestAPIStrataConfigPutHashesAndPreservesPasswords` の `doc` 作成直後に外部Webディレクトリを設定し、保存後のドキュメントで同値を要求する。

```go
doc.WUIWebDir = "external-web"
```

```go
if saved.WUIWebDir != doc.WUIWebDir {
	t.Fatalf("wuiWebDir was not preserved: got %q, want %q", saved.WUIWebDir, doc.WUIWebDir)
}
```

- [ ] **Step 3: 変更したテストが原因どおり失敗することを確認する**

Run:

```text
rtk powershell -NoProfile -Command '[System.Environment]::SetEnvironmentVariable("GOCACHE","C:\Users\SandS\AppData\Local\Temp\strata-pvr-go-test-cache","Process"); & "C:\Program Files\Go\bin\go.exe" test ./internal/wui -run "TestNativeDashboardStrataConfigForm|TestAPIStrataConfigPutHashesAndPreservesPasswords" -count=1'
```

Expected: `TestNativeDashboardStrataConfigForm` または `wuiWebDir was not preserved` でFAIL。テストが既存実装の別エラーで停止した場合は、テスト記述を修正してから次へ進む。

### Task 2: フォームpayloadとサーバー再構成を修正する

**Files:**

- Modify: `web/app.js:5300-5330,5760-5790`
- Modify: `internal/wui/server.go:1759-1781,1835-1842`

**Interfaces:**

- Consumes: `state.config.web.trustForwardedHeaders`、`state.config.web.trustedProxies`、`state.config.wuiWebDir`。
- Produces: `PUT /api/config` が非ループバック待受に必要な既存設定を含み、サーバーが `config.Document.WUIWebDir` へ渡す完全な更新ドキュメント。

- [ ] **Step 1: 現在のWeb設定をフォーム描画関数内で扱う**

`renderStrataConfigForm` と同じく `readStrataConfigForm` でも `var cfg = state.config || {};` と `var web = cfg.web || {};` を使い、既存配列を変更しないため `slice()` でコピーする。

- [ ] **Step 2: 設定フォームの戻り値へ欠落していた値を追加する**

`readStrataConfigForm` の戻り値を次の形にする。

```javascript
return {
  schema: cfg.schema,
  version: cfg.version,
  mirakurun: { url: mirakurunURL, recordingPriority: Number(controlString("strataRecordingPriority")), conflictedPriority: Number(controlString("strataConflictedPriority")) },
  recording: { directory: directory, filenameFormat: filenameFormat, startMargin: startMargin, endMargin: endMargin, lowSpace: { thresholdMB: threshold, action: controlString("strataLowSpaceAction") }, postProcess: { commands: postProcessCommands, timeoutSeconds: postProcessTimeout, maxConcurrentRuns: postProcessMaxConcurrent } },
  previewCache: { maxAgeDays: previewMaxAge, maxSizeMB: previewMaxSize },
  web: {
    listenAddress: listenAddress,
    port: port,
    trustForwardedHeaders: web.trustForwardedHeaders === true,
    trustedProxies: Array.isArray(web.trustedProxies) ? web.trustedProxies.slice() : [],
    authentication: { enabled: enabled, users: users }
  },
  wuiWebDir: cfg.wuiWebDir || "",
  services: { excluded: excluded, order: order },
  advanced: { normalizationForm: controlString("strataNormalizationForm"), mp4VideoEncoder: controlString("strataMP4VideoEncoder") }
};
```

- [ ] **Step 3: サーバー更新型へ `wuiWebDir` を追加する**

`strataConfigUpdate` に次のフィールドを追加する。

```go
WUIWebDir string `json:"wuiWebDir"`
```

- [ ] **Step 4: 再構成する `config.Document` に `WUIWebDir` を渡す**

`updateStrataConfig` の `config.Document` リテラルに次を追加する。

```go
WUIWebDir: update.WUIWebDir,
```

既存の `TrustForwardedHeaders` と `TrustedProxies` の代入は残し、フォームから受け取った現在値が `config.ParseDocument` の非ループバック検証へ届くようにする。

- [ ] **Step 5: targeted Go testsをGREENで確認する**

Run:

```text
rtk powershell -NoProfile -Command '[System.Environment]::SetEnvironmentVariable("GOCACHE","C:\Users\SandS\AppData\Local\Temp\strata-pvr-go-test-cache","Process"); & "C:\Program Files\Go\bin\go.exe" test ./internal/wui -run "TestNativeDashboardStrataConfigForm|TestAPIStrataConfigPutHashesAndPreservesPasswords|TestAPIStrataConfigRedactsPasswordHashAndRejectsInvalidPut|TestAPIConfigPutRequiresValidJSON" -count=1'
```

Expected: 対象テストがすべてPASSし、HTTP 400を要求する不正入力テストもPASSする。

### Task 3: 実ブラウザで非ループバック設定保存を検証する

**Files:**

- No source changes.
- Evidence: 一時WUIのPlaywright操作結果とWUI access log。

**Interfaces:**

- Consumes: Task 2 の `web/app.js` と `internal/wui/server.go`。
- Produces: ログイン済み設定画面での保存成功と、`PUT /api/config` HTTP 200の実動作証拠。

- [ ] **Step 1: 有効な非ループバック構成で一時WUIを起動する**

設定は `web.listenAddress=0.0.0.0`、`web.authentication.enabled=true`、`web.trustForwardedHeaders=true`、`web.trustedProxies=["127.0.0.1"]`、ログインユーザー1件とする。既存の実データや設定ファイルは使わず、隔離した一時ディレクトリで起動する。

- [ ] **Step 2: Playwrightでログインして設定画面を開く**

Playwrightで次の操作を行う。

```text
goto http://127.0.0.1:20772/login
fill #username with admin
fill #password with the temporary test password
click #submit
click summary[aria-label="管理"]
click a[data-view-link="settings"]
```

期待値は `#strataListenAddress` が `0.0.0.0`、`#strataAuthEnabled` がchecked、ユーザー行が1件である。

- [ ] **Step 3: 任意の設定を変更して保存する**

`#strataRecordingEndMargin` を現在値から1秒変更し、`#saveStrataConfigButton`、続けて `#confirmDialogOK` をクリックする。画面に `設定を保存しました` が表示され、`#strataConfigForm` がエラー状態にならないことを確認する。

- [ ] **Step 4: HTTPと永続化を確認する**

WUI access logに次の記録があることを確認する。

```text
status=200|method="PUT"|path="/api/config"
```

その後ページを再読み込みしてログインし、変更した録画終了マージンが保持されること、待受アドレスが `0.0.0.0` のままであることを確認する。HTTP 400、設定エラー表示、プロキシ設定の消失があれば未受入とする。

### Task 4: 全体検証とコミット

**Files:**

- Verify: `web/app.js`, `internal/wui/server.go`, `internal/wui/server_test.go`

- [ ] **Step 1: フォーマットと全Goテストを実行する**

```text
rtk powershell -NoProfile -Command '& "C:\Program Files\Go\bin\gofmt.exe" -w internal/wui/server.go internal/wui/server_test.go'
rtk powershell -NoProfile -Command '[System.Environment]::SetEnvironmentVariable("GOCACHE","C:\Users\SandS\AppData\Local\Temp\strata-pvr-go-test-cache","Process"); & "C:\Program Files\Go\bin\go.exe" test ./... -count=1'
```

Expected: gofmtによる差分が安定し、全GoテストがPASSする。

- [ ] **Step 2: バイナリビルドを一時出力先で確認する**

```text
rtk powershell -NoProfile -Command '[System.Environment]::SetEnvironmentVariable("GOCACHE","C:\Users\SandS\AppData\Local\Temp\strata-pvr-go-build-cache","Process"); & "C:\Program Files\Go\bin\go.exe" build -o C:\Users\SandS\AppData\Local\Temp\strata-pvr-build-verify.exe ./cmd/strata-pvr'
```

Expected: exit code 0。既定のリポジトリ直下へexeを出力しない。

- [ ] **Step 3: 差分の空白エラーを確認する**

```text
rtk git diff --check
```

Expected: 出力なし。

- [ ] **Step 4: 修正をコミットする**

```text
rtk git add web/app.js internal/wui/server.go internal/wui/server_test.go
rtk git commit -m "fix: preserve WUI settings during config save"
```

