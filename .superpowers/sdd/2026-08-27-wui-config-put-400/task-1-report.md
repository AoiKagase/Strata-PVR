# Task 1 Report

## 実装内容

- `internal/wui/server_test.go` の `TestNativeDashboardStrataConfigForm` に、静的フロントエンド契約として `trustForwardedHeaders`、`trustedProxies`、`wuiWebDir` の3項目を要求する文字列アサーションを追加。
- `internal/wui/server_test.go` の `TestAPIStrataConfigPutHashesAndPreservesPasswords` に、初期ドキュメントの `WUIWebDir` を `external-web` に設定し、`PUT /api/config` 後も同値で保存されることを要求する回帰アサーションを追加。

## REDコマンド

```powershell
rtk powershell -NoProfile -Command '[System.Environment]::SetEnvironmentVariable("GOCACHE","C:\Users\SandS\AppData\Local\Temp\strata-pvr-go-test-cache","Process"); & "C:\Program Files\Go\bin\go.exe" test ./internal/wui -run "TestNativeDashboardStrataConfigForm|TestAPIStrataConfigPutHashesAndPreservesPasswords" -count=1'
```

## 失敗出力

```text
--- FAIL: TestNativeDashboardStrataConfigForm (0.00s)
    server_test.go:1460: ..\..\web\app.js missing "trustForwardedHeaders: web.trustForwardedHeaders === true"
--- FAIL: TestAPIStrataConfigPutHashesAndPreservesPasswords (0.53s)
    server_test.go:5535: wuiWebDir was not preserved: got "", want "external-web"
FAIL
FAIL    strata-pvr/internal/wui  2.905s
FAIL
```

## 変更ファイル

- `internal/wui/server_test.go`
- `.superpowers/sdd/2026-08-27-wui-config-put-400/task-1-report.md`

## TDD証拠

- 先に `server_test.go` のみを編集し、実装コード `web/app.js` と `internal/wui/server.go` は未変更のまま targeted test を実行した。
- 追加した2つの期待が既存実装の欠落に直接対応して RED で失敗したことを確認した。

## Self-review

- brief で指定された追加アサーションだけに限定し、既存テストの意味や対象エンドポイントは変更していない。
- `git diff --check -- internal/wui/server_test.go` は空で、テストファイルの書式破損はない。
- 失敗理由は静的 payload 契約欠落と `wuiWebDir` 未保存で、別要因のノイズ失敗ではない。

## 懸念

- コードグラフはこの worktree で未構築だったため、AGENTS 指示確認後にファイル断面の直接読取へフォールバックした。
- このコミットは RED 固定用であり、実装コード未修正のため targeted test は失敗したまま。
