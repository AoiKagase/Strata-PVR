# セキュリティ・ベストプラクティス監査報告

監査日: 2026-07-28  
対象: Strata PVR（Go `net/http`、フレームワークなしの JavaScript）

## エグゼクティブサマリー

認証は Argon2id、暗号学的にランダムなセッション／トークン、`HttpOnly`・`SameSite=Strict` Cookie、same-origin 検証を使用しており、API トークンをハッシュのみで保存するなど、重要な基盤は良好です。一方で、既知の Go 標準ライブラリ脆弱性、HTTP リソース上限、および転送ヘッダーの信頼境界に対応が必要です。

`govulncheck`（Go 1.26.4）では、到達可能な既知脆弱性を 1 件検出しました。デプロイ環境が別の Go パッチ版なら、そこで再確認してください。

## 高

### SBP-001: HTTP サーバーに接続全体のタイムアウトとヘッダー上限がない

- **ルール ID:** GO-HTTP-001
- **場所:** `internal/wui/server.go:588-593`、`buildHTTPServers`
- **証拠:** `http.Server` には `ReadHeaderTimeout: 10 * time.Second` のみが設定され、`ReadTimeout`、`WriteTimeout`、`IdleTimeout`、`MaxHeaderBytes` は未設定です。
- **影響:** 接続を遅く維持するリクエストや過大なヘッダーにより、公開 WUI の接続・メモリ資源を消費され、サービス不能となるおそれがあります。
- **修正:** ストリーミング要件に合わせて明示的な `ReadTimeout`、`WriteTimeout`、`IdleTimeout`、`MaxHeaderBytes` を設定してください。HLS 等の長時間応答は、通常 API と別のサーバー／値にすることを検討してください。

### SBP-002: Go 標準ライブラリの到達可能な既知脆弱性

- **ルール ID:** GO-DEPLOY-001
- **場所:** 実行環境の `crypto/tls@go1.26.4`。到達経路は `internal/wui/server.go:164` の `ListenAndServe` および `internal/mirakurun/client.go:99` の `http.Client.Do`。
- **証拠:** `govulncheck@latest ./...` は **GO-2026-5856**（Encrypted Client Hello のプライバシー漏えい）を検出し、修正版を `crypto/tls@go1.26.5` と報告しました。
- **影響:** ECH を利用する TLS 接続でプライバシー情報が漏えいする可能性があります。
- **修正:** ビルド・実行環境を Go 1.26.5 以降（または脆弱性のないサポート対象パッチ版）へ更新し、更新後に `govulncheck ./...` を再実行してください。
- **誤検知に関する注記:** 実際に ECH を使用しない配備では露出は限定的です。ただし検査で到達可能と判定されているため、更新を推奨します。

## 中

### SBP-003: 転送ヘッダーを無条件に信頼している

- **ルール ID:** GO-HTTP-003
- **場所:** `internal/wui/auth.go:193-208` の `requestUsesHTTPS`／`requestOrigin`、`internal/wui/server.go:4464-4475` の `xspfTarget`
- **証拠:** `X-Forwarded-Proto` と `X-Forwarded-Host` を送信元プロキシの検証なしで、Cookie の `Secure` 属性、same-origin 判定、生成 URL に用いています。
- **影響:** アプリが直接到達可能、またはリバースプロキシがクライアント由来の同ヘッダーを除去しない場合、攻撃者がスキーム・ホスト判定を偽装できます。
- **修正:** アプリを信頼済みプロキシからのみ到達可能にし、プロキシでクライアント提供の `X-Forwarded-*` を必ず削除・再設定してください。アプリ側では信頼プロキシ判定を導入するか、正規オリジンを設定値から使用してください。
- **誤検知に関する注記:** インフラ側で上記のヘッダーを確実にサニタイズしている場合はリスクを軽減できます。リポジトリ内にはその設定がありません。

### SBP-004: 脆弱性スキャンが CI に組み込まれていない

- **ルール ID:** GO-DEPLOY-001
- **場所:** `.github/workflows/ci.yml:18-33`
- **証拠:** CI は `go test ./...` を実行しますが、`govulncheck` の実行手順がありません。
- **影響:** 新たに導入・到達可能となった既知脆弱性を継続的に検出できません。
- **修正:** 依存関係取得後に `govulncheck ./...` を実行する CI ステップを追加し、結果を修正計画に結び付けてください。

## 低（インフラでの確認が必要）

### SBP-005: CSP がアプリコードからは確認できない

- **ルール ID:** GO-HTTP-004
- **場所:** `internal/wui/server.go:620-631` の `withCommonHeaders`
- **証拠:** `X-Content-Type-Options` と `X-Frame-Options` は設定されていますが、`Content-Security-Policy` はありません。
- **影響:** 将来の DOM XSS の影響を抑える防御層が不足します。
- **修正:** リバースプロキシまたはアプリで、実際の静的アセット・プレイヤー要件を検証した上で CSP を段階的に導入してください。最初は `script-src` を明示する方針が適しています。
- **誤検知に関する注記:** CDN／リバースプロキシで CSP を付与している場合は対応済みです。アプリコード内では確認できません。

## 確認済みの良い点

- セッション ID・再生チケット・API トークンは `crypto/rand` 由来で、API トークンの秘密値は保存せず SHA-256 ハッシュだけを保存しています（`internal/wui/auth.go`、`internal/wui/server.go`）。
- パスワードは Argon2id を使用し、リクエスト本文は認証・設定 API で `http.MaxBytesReader` により制限されています。
- セッション Cookie は `HttpOnly` と `SameSite=Strict` を設定し、Cookie 認証の状態変更操作には same-origin 検証があります。
- 監査時点で、`json.NewDecoder(r.Body)`、`io.ReadAll(r.Body)`、無制限の multipart パースの利用は `internal/` に検出されませんでした。

## 実施範囲と制限

- Go 標準ライブラリと、ブラウザ JavaScript の一般的なセキュリティ指針を用いてソース、CI、依存関係を確認しました。
- インフラ（TLS 終端、リバースプロキシ、ネットワーク公開範囲、CSP）の実際の設定はこのリポジトリ外のため、実行環境で検証が必要です。
