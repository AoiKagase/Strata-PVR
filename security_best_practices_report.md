# セキュリティ・ベストプラクティス監査報告

監査日: 2026-07-28  
対象: Strata PVR（Go `net/http`、フレームワークなしの JavaScript）

## エグゼクティブサマリー（完了）

認証は Argon2id、暗号学的にランダムなセッション／トークン、`HttpOnly`・`SameSite=Strict` Cookie、same-origin 検証を使用しており、API トークンをハッシュのみで保存するなど、重要な基盤は良好です。本報告書の指摘はすべて修正または緩和済みです。

2026-07-28 に Go 1.26.5 で `govulncheck ./...` を再実行し、到達可能な脆弱性は **0 件**でした。CI も同じ検査を継続実行します。

## 高

### SBP-001: HTTP サーバーに接続全体のタイムアウトとヘッダー上限がない

**状態: 修正済み**

- **ルール ID:** GO-HTTP-001
- **場所:** `internal/wui/server.go:588-593`、`buildHTTPServers`
- **修正後:** `internal/wui/server.go` は `ReadTimeout: 30s`、`IdleTimeout: 2m`、`MaxHeaderBytes: 1 MiB` を設定しています。回帰テストも追加しました。
- **影響:** 接続を遅く維持するリクエストや過大なヘッダーにより、公開 WUI の接続・メモリ資源を消費され、サービス不能となるおそれがあります。
- **残余判断:** 長時間のメディア配信を中断しないよう `WriteTimeout` は意図的に未設定です。将来通常 API とストリーミングを別リスナーに分離した場合、前者には書込みタイムアウトを設定してください。

### SBP-002: Go 標準ライブラリの到達可能な既知脆弱性

**状態: 修正済み**

- **ルール ID:** GO-DEPLOY-001
- **場所:** 実行環境の `crypto/tls@go1.26.4`。到達経路は `internal/wui/server.go:164` の `ListenAndServe` および `internal/mirakurun/client.go:99` の `http.Client.Do`。
- **証拠:** `govulncheck@latest ./...` は **GO-2026-5856**（Encrypted Client Hello のプライバシー漏えい）を検出し、修正版を `crypto/tls@go1.26.5` と報告しました。
- **影響:** ECH を利用する TLS 接続でプライバシー情報が漏えいする可能性があります。
- **修正後:** `go.mod` と CI を Go 1.26.5 に固定し、更新後の `govulncheck ./...` は到達可能な脆弱性 0 件でした。
- **誤検知に関する注記:** 実際に ECH を使用しない配備では露出は限定的です。ただし検査で到達可能と判定されているため、更新を推奨します。

## 中

### SBP-003: 転送ヘッダーを無条件に信頼している

**状態: 修正済み**

- **ルール ID:** GO-HTTP-003
- **場所:** `internal/wui/auth.go:193-208` の `requestUsesHTTPS`／`requestOrigin`、`internal/wui/server.go:4464-4475` の `xspfTarget`
- **証拠:** `X-Forwarded-Proto` と `X-Forwarded-Host` を送信元プロキシの検証なしで、Cookie の `Secure` 属性、same-origin 判定、生成 URL に用いています。
- **影響:** アプリが直接到達可能、またはリバースプロキシがクライアント由来の同ヘッダーを除去しない場合、攻撃者がスキーム・ホスト判定を偽装できます。
- **修正後:** `web.trustForwardedHeaders` を追加し、既定では `false` としました。有効化しない限り、Cookie、same-origin 判定、生成 URL は `X-Forwarded-*` を参照しません。TLS 終端プロキシで利用する場合だけ、プロキシがヘッダーを削除・再設定することを確認した上で `true` を指定してください。

### SBP-004: 脆弱性スキャンが CI に組み込まれていない

**状態: 修正済み**

- **ルール ID:** GO-DEPLOY-001
- **場所:** `.github/workflows/ci.yml:18-33`
- **証拠:** CI は `go test ./...` を実行しますが、`govulncheck` の実行手順がありません。
- **影響:** 新たに導入・到達可能となった既知脆弱性を継続的に検出できません。
- **修正後:** `.github/workflows/ci.yml` のテストジョブで `govulncheck ./...` を実行します。

## 低（インフラでの確認が必要）

### SBP-005: CSP がアプリコードからは確認できない

**状態: 緩和済み**

- **ルール ID:** GO-HTTP-004
- **場所:** `internal/wui/server.go:620-631` の `withCommonHeaders`
- **修正後:** `internal/wui/server.go` で CSP を設定し、`default-src`、`connect-src`、`object-src`、`base-uri`、`form-action`、`frame-ancestors` を制限しました。
- **影響:** 将来の DOM XSS の影響を抑える防御層が不足します。
- **残余判断:** `login.html` と `player.html` の既存インライン資産との互換性のため、`script-src`／`style-src` には `unsafe-inline` を残しています。インライン資産を外部化または nonce／hash 化するまでの互換性優先の緩和です。

## 確認済みの良い点

- セッション ID・再生チケット・API トークンは `crypto/rand` 由来で、API トークンの秘密値は保存せず SHA-256 ハッシュだけを保存しています（`internal/wui/auth.go`、`internal/wui/server.go`）。
- パスワードは Argon2id を使用し、リクエスト本文は認証・設定 API で `http.MaxBytesReader` により制限されています。
- セッション Cookie は `HttpOnly` と `SameSite=Strict` を設定し、Cookie 認証の状態変更操作には same-origin 検証があります。
- 監査時点で、`json.NewDecoder(r.Body)`、`io.ReadAll(r.Body)`、無制限の multipart パースの利用は `internal/` に検出されませんでした。

## 実施範囲と制限

- Go 標準ライブラリと、ブラウザ JavaScript の一般的なセキュリティ指針を用いてソース、CI、依存関係を確認しました。
- インフラ（TLS 終端、リバースプロキシ、ネットワーク公開範囲）の実際の設定はこのリポジトリ外です。`trustForwardedHeaders` を有効化する配備では、プロキシがクライアント提供の転送ヘッダーを除去・再設定することを運用確認してください。
