# WUI設定保存時のHTTP 400修正仕様

## 問題

WUIの設定画面で設定を変更して保存すると、設定が有効なStrata構成でも
`PUT /api/config` がHTTP 400になることがある。再現条件は、
`web.listenAddress` が `0.0.0.0` などの非ループバック、認証が有効、
`web.trustForwardedHeaders` が有効、`web.trustedProxies` が1件以上の構成で、
設定画面から任意の項目を保存する場合である。

## 根拠

- `web/app.js` の `readStrataConfigForm()` は `web.listenAddress`、`web.port`、
  `web.authentication` だけを送信し、既存の `trustForwardedHeaders` と
  `trustedProxies` を送信していない。
- `internal/wui/server.go` の `updateStrataConfig()` は欠落した値を
  `false` と空スライスとして設定ドキュメントへ再構成する。
- `internal/config/config.go` は非ループバック待受に認証、転送ヘッダー信頼、
  信頼プロキシを要求するため、再構成後のドキュメントを400として拒否する。
- 同じ更新処理は現在の `wuiWebDir` も再構成時に引き継がないため、保存成功時に
  外部Webアセット設定を空にする別のフィールド消失も発生する。

## 要件

1. 設定画面で既存の有効な非ループバック構成の任意項目を保存すると、HTTP 200
   になり、`trustForwardedHeaders` と `trustedProxies` が保持される。
2. 設定画面で設定を変更しても、`wuiWebDir` が保持される。
3. ループバック待受、認証ユーザーのパスワード保持、無効なJSONの400など、既存の
   設定更新契約を変更しない。
4. 実ブラウザのPlaywright操作で、ログイン、設定画面表示、項目変更、保存確認まで
   成功することを確認する。

