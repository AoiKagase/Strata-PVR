# Strata PVR UI 技術監査

2026-09-30（日本時間）。対象コミット: `6c2e749232d83265249b306061b2d88ce8bb38f5`。依頼: `$impeccable audit`。製品コードの修正は行っていません。

## 実装の一貫性

**合格（局所的な不整合あり）**。番組表、録画一覧、設定という録画運用に対応した構成で、共通の色トークン、状態表示、フォーカス表示があります。固定ヘッダと現在時刻線も製品の目的に沿っています。ただしホバー時の文字色に別用途のトークンを使用し、ログイン画面は独立した固定色・レイアウトを使用しています。

## 総評

| 項目 | 点数（0–4） | 主な根拠 |
| --- | --- | --- |
| アクセシビリティ | 2 | ラベル・フォーカス対応あり。ダークテーマのホバーでコントラスト不足 |
| パフォーマンス | 3 | 画面別データ取得・画像の遅延読み込み。再生用 JS は全画面で読み込む |
| レスポンシブ | 2 | 通常画面は狭幅に対応。設定・ログインのはみ出し、小さいページ送り |
| テーマ | 3 | ライト／ダークのトークンを広範に使用。ホバー色とログインの固定色が弱点 |
| 実装の整合性 | 3 | 製品固有の構成は一貫。共通ボタンの状態定義に局所的不整合 |
| **合計** | **13/20** | **改善が必要（Acceptable）** |

指摘は **6件（P0:0、P1:3、P2:3、P3:0）**。同じ不具合をカテゴリごとに重複集計していません。実 API、録画・視聴の成功、全状態の WCAG 適合を保証する点数ではありません。

## 優先度別の指摘

### 1. [P1] ダークテーマのボタンホバーで文字が読みにくい

- 場所: `web/styles.css:885`（`button:hover`）、同ファイル `:34`、`:154`。
- カテゴリ: アクセシビリティ／テーマ。
- 実測: ダーク・1440px の `#refreshButton` をホバー。文字 `oklch(0.97 0.004 245)`、背景 `oklch(0.82 0.075 220)`。canvas で sRGB に変換したコントラスト **1.566:1**。ライトでは **8.007:1**。
- 影響: 明るい背景に明るい文字が重なり、操作名が読みにくくなります。共通のホバールールを使うボタンへ波及します。
- 基準: WCAG 2.2 **1.4.3（AA）**。通常サイズの文字に必要な 4.5:1 未満。
- 推奨: ホバー背景と対応する文字色を状態トークンとして定義するか、ダークテーマで適切な `--accent-ink` を使用。secondary／danger／filter ボタンも状態ごとに確認。
- コマンド: `$impeccable harden`。
- 証拠: `confirm.json` の `dark/dashboard`、`dark-dashboard.png`。

### 2. [P1] 設定のエンコーダー選択が狭幅を押し広げる

- 場所: `web/index.html:750`（`#strataMP4VideoEncoder`）、`web/styles.css:860` と設定フォームの縮小制約。
- カテゴリ: レスポンシブ。
- 実測: 320px／375px のタッチ viewport で `/#settings` を開くと、ドキュメント幅 **394px**。select 自体は **363px** 幅、左端31px／右端394px、`min-width:auto`。
- 再現条件: H.264 エンコーダーがない状態の長い日本語 option。テスト用 `/api/encoders` は空配列。
- 影響: 設定画面全体が横に広がり、縮小表示・横移動が必要になります。
- 基準: WCAG 2.2 **1.4.10（AA）**。設定フォームは二次元表示の例外ではありません。
- 推奨: grid/flex 子要素と select に `min-width:0`、`width:100%`、`max-width:100%` を適用し、長い選択肢でも親幅を超えないようにする。
- コマンド: `$impeccable adapt`。
- 証拠: `confirm.json` の `narrow/settings`、`mobile-settings.png`。

### 3. [P1] ログイン画面が狭幅で横にはみ出す

- 場所: `web/login.html:10`（`main`）。
- カテゴリ: レスポンシブ。
- 実測: 375px viewport でドキュメント幅 **392px**、320px で **337px**。スクリーンショットでもパネルが viewport を超えます。
- 原因: `width:min(100% - 2rem,25rem)` に左右 `padding:2rem` と border が追加され、border-box の指定がありません。入力欄の intrinsic sizing も縮小の下限になります。
- 影響: 認証の入力・送信前に横移動や縮小表示を強いられます。
- 基準: WCAG 2.2 **1.4.10（AA）**。
- 推奨: border-box を適用し、main／form／input の縮小を許容。320px とブラウザ拡大表示相当の狭幅で再確認。
- コマンド: `$impeccable adapt`。
- 証拠: `results.json` の `mobile/login` と `narrow/login`、`mobile-login.png`。

### 4. [P2] タッチ画面のページ送りが高さ32px

- 場所: `web/index.html:261` 以降の検索ページング、`web/styles.css:872`。
- カテゴリ: レスポンシブ／アクセシビリティ。
- 実測: 375px の番組検索で `#searchFirstPage`、`#searchPrevPage`、`#searchNextPage`、`#searchLastPage` および下部ボタンが **44×32px**。
- 影響: 繰り返し使うページ送りのタッチ範囲が小さく、誤操作しやすくなります。
- 基準: 44×44px は監査のタッチ目標および WCAG **2.5.5（AAA）**。寸法だけを根拠に24px基準の **2.5.8（AA）違反とは判定していません**。
- 推奨: coarse pointer では最低44px高に揃え、対象間の間隔を維持。
- コマンド: `$impeccable adapt`。
- 証拠: `results.json` の `mobile/search`。smallTargets 候補には画面外の skip link や checkbox 本体も含むため、候補数をそのまま不具合数にはしていません。

### 5. [P2] ログインの5xxを資格情報の誤りとして表示

- 場所: `web/login.html:38–47`。
- カテゴリ: 実装の整合性／エラー対応。
- 根拠: ソース確認。`response.ok` が偽のすべての応答に「ユーザー名またはパスワードが正しくありません。」を表示。通信例外は `err.message` がそのまま表示されます。
- 影響: サーバー障害でもパスワードの再入力を促してしまいます。ブラウザ由来の英語エラーも日本語 UI と不一致です。
- 基準: 特定の WCAG 違反とは判定せず。PRODUCT.md の通信・5xx時の再試行案内方針と不整合。
- 推奨: 認証失敗、429、5xx、通信例外を分け、日本語の原因と再試行案内を表示。既存の送信ボタン再有効化を維持。
- コマンド: `$impeccable harden` → `$impeccable clarify`。
- 証拠の限界: 実認証サーバーでの障害注入は未実施。

### 6. [P2] 視聴しない画面でも再生ライブラリを常時ロード

- 場所: `web/index.html:847`（通常 script）、`web/mpegts.js`。
- カテゴリ: パフォーマンス。
- 根拠: HTML は再生ライブラリを無条件ロード。ファイルサイズ **272,955 bytes**（圧縮前）。app.js は275,885 bytes、styles.css は81,493 bytes。
- 影響: 検索・設定だけを使う初回アクセスでも再生用コードの転送・解析費用が発生。
- 推奨: 視聴開始時に共有 Promise で遅延ロードし、初回待ち状態・失敗・再試行を実装。実配信の圧縮・キャッシュを測定して効果を評価。
- コマンド: `$impeccable optimize`。
- 証拠の限界: 低速回線、CPU throttling、大量データ、実WUIのキャッシュは未測定。実際の遅延やFPS低下は断定しません。

## 検出器の検証

Impeccable `detect --json` を全5対象に実行。保存した `detector.json` の警告は **30件**（side-tab:1、low-contrast:28、cramped-padding:1）。

- 反復する contrast 警告はダークホバーを実測して指摘1に統合。28件すべてを別の違反と扱っていません。
- `border-bottom:3px` はナビゲーションの選択状態や上部の区切りとして意味のある表示。不要なカード装飾とは判断せず除外。
- dialog 余白の警告は、子要素で余白を持たせる構成を考慮。開いたダイアログの実画面は今回未検証であり、確定指摘に採用していません。

## 良い実装

- 今回描画されたフォームで、関連付けラベル・aria-label のない入力は0件。
- `:focus-visible`、skip link、aria-live、状態付きナビゲーション。
- ソース上で native dialog、フォーカストラップと復帰、番組カードの矢印キーとEnter操作。
- OKLCHによるライト／ダークトークン、対象を絞った reduced-motion。全要素の時間を極端に短縮する方式を避けています。
- ロゴ・録画プレビューの遅延読み込み、現在表示する画面に応じたデータ取得。
- 今回の表示確認で未処理JS例外なし。通常画面や番組表にドキュメント全体の横はみ出しなし。

## 方法と検証範囲

Playwright Chromium headless を使用。現行webファイルを localhost の一時サーバーで配信し、APIは架空番組、空の予約・録画・ルール、sample configに固定。本番設定・DB・録画・認証情報は変更していません。

- 一次確認: 1440×900ライト／ダーク、375×812タッチ、320×740タッチ × 11画面の **44ケース**。dashboard、schedule、search、reserves、recorded、rules、status、settings、logs、login、player。
- 確認パス: 同4条件 × dashboard、schedule、search、settings、login の **20ケース**。ホバー色とはみ出す要素の寸法を追加測定。
- desktop/mobile/dark の5画面と narrow login の画像を取得。主要な根拠3枚を保存。
- ラベル、寸法、操作対象の矩形、pageerrorを測定。smallTargets は候補であり、実際のヒット領域や label を考慮せず一律に違反と扱わないこと。
- 知識グラフを先に参照。build SHA がHEADと違うため、指摘は現行ソースとブラウザ測定で裏付け。
- 実認証・録画・メディア再生、キーボード操作の一連の完遂、支援技術、大量データ、実サーバー性能は未検証。player はメディア未指定状態のみ。
- UI修正のない監査のため、Go／機能テストは未実施。

再現: `audit.cjs` はリポジトリ内の現行webを読み、結果を一時フォルダへ保存します。Playwrightの導入済み環境で実行してください。

```powershell
$env:STRATA_AUDIT_PLAYWRIGHT = 'Playwright パッケージの絶対パス'
rtk proxy node .\docs\ui-audit\2026-09-30\audit.cjs
```

## 次の推奨作業

1. **[P1] `$impeccable harden`**: ボタンのダークホバー文字色を修正し、各状態を再測定。
2. **[P1/P2] `$impeccable adapt`**: ログイン・設定のはみ出しとページ送りのタッチ寸法。
3. **[P2] `$impeccable harden` → `$impeccable clarify`**: ログイン障害の原因と日本語の再試行案内。
4. **[P2] `$impeccable optimize`**: 再生ライブラリの遅延ロードと実配信性能測定。
5. **`$impeccable polish`**: 修正後の状態差と操作を最終確認。
6. **`$impeccable audit`**: 修正後の再監査。

個別でもまとめても、任意の順序でも依頼できます。

補足: PRODUCT.md は現行Impeccableのschema・記録セクションより古い形式です。ローダーの報告した旧Registerは今回の判断に使用していません。更新する場合は `$impeccable init` で確認済み情報を引き継げます。今回の監査では変更していません。
