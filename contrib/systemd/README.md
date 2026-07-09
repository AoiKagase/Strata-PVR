# systemd units for Strata PVR

These units assume Strata PVR is installed in `/opt/strata-pvr` and the binary
is `/opt/strata-pvr/strata-pvr`.

`strata-pvr service ... execute` uses the current working directory for
`config.json`, `rules.json`, `data/`, `log/`, and `web/`, so keep
`WorkingDirectory` pointed at the directory that contains those files.

## Install

```sh
sudo install -d -m 0755 /opt/strata-pvr
sudo install -m 0755 strata-pvr /opt/strata-pvr/strata-pvr
sudo cp -a web config.sample.json rules.sample.json /opt/strata-pvr/

sudo install -m 0644 contrib/systemd/strata-pvr-operator.service /etc/systemd/system/
sudo install -m 0644 contrib/systemd/strata-pvr-wui.service /etc/systemd/system/
sudo install -m 0644 contrib/systemd/strata-pvr-scheduler.service /etc/systemd/system/
sudo install -m 0644 contrib/systemd/strata-pvr-scheduler.timer /etc/systemd/system/

sudo systemctl daemon-reload
sudo systemctl enable --now strata-pvr-operator.service
sudo systemctl enable --now strata-pvr-wui.service
sudo systemctl enable --now strata-pvr-scheduler.timer
```

Run the scheduler once immediately if needed:

```sh
sudo systemctl start strata-pvr-scheduler.service
```

## Adjust paths or user

If you install somewhere other than `/opt/strata-pvr`, edit `WorkingDirectory`
and `ExecStart` in each unit.

The services run without a systemd `User=` by default. This preserves the
runtime behavior where Strata PVR reads `config.json` and then applies `uid` and
`gid` from the config where supported. If you want systemd to manage the user
instead, add `User=` and `Group=` to the service files and make sure the working
directory, `data/`, `log/`, and recorded directory are writable by that user.

## Logs

Use journalctl for service output:

```sh
journalctl -u strata-pvr-wui.service -f
journalctl -u strata-pvr-operator.service -f
journalctl -u strata-pvr-scheduler.service
```

Strata PVR also writes legacy-compatible logs under `/opt/strata-pvr/log/`.

---

# Strata PVR systemd インストールガイド

このディレクトリには、Linux の systemd で Strata PVR を起動するための
ユニットファイル例があります。

標準では次の配置を想定しています。

- 作業ディレクトリ: `/opt/strata-pvr`
- 実行ファイル: `/opt/strata-pvr/strata-pvr`
- 設定ファイル: `/opt/strata-pvr/config.json`
- ルールファイル: `/opt/strata-pvr/rules.json`
- 状態ファイル: `/opt/strata-pvr/data/`
- ログファイル: `/opt/strata-pvr/log/`
- Web UI 静的ファイル: `/opt/strata-pvr/web/`

`strata-pvr service ... execute` はカレントディレクトリを基準に
`config.json`、`rules.json`、`data/`、`log/`、`web/` を扱います。そのため
各 unit の `WorkingDirectory` は、これらのファイルを置くディレクトリに
合わせてください。

## 1. バイナリをビルドする

リポジトリのルートで Linux 用バイナリをビルドします。

```sh
go build -o strata-pvr ./cmd/strata-pvr
```

別マシンでクロスビルドする場合は、実行先の Linux に合う `GOOS` / `GOARCH`
を指定してください。

例:

```sh
GOOS=linux GOARCH=amd64 go build -o strata-pvr ./cmd/strata-pvr
```

## 2. ファイルを配置する

`/opt/strata-pvr` にバイナリ、Web UI、サンプル設定を配置します。

```sh
sudo install -d -m 0755 /opt/strata-pvr
sudo install -m 0755 strata-pvr /opt/strata-pvr/strata-pvr
sudo cp -a web config.sample.json rules.sample.json /opt/strata-pvr/
```

既存の `config.json` や `rules.json` を使う場合は、このタイミングで
`/opt/strata-pvr/` にコピーしてください。

```sh
sudo cp config.json rules.json /opt/strata-pvr/
```

初回起動時に `config.json` または `rules.json` が存在しない場合は、
`service ... execute` が `config.sample.json` と `rules.sample.json` から
自動作成します。

## 3. systemd unit をインストールする

```sh
sudo install -m 0644 contrib/systemd/strata-pvr-operator.service /etc/systemd/system/
sudo install -m 0644 contrib/systemd/strata-pvr-wui.service /etc/systemd/system/
sudo install -m 0644 contrib/systemd/strata-pvr-scheduler.service /etc/systemd/system/
sudo install -m 0644 contrib/systemd/strata-pvr-scheduler.timer /etc/systemd/system/
sudo systemctl daemon-reload
```

各 unit は `/opt/strata-pvr` を前提にしています。別の場所にインストールする
場合は、インストール後に次の項目を編集してください。

- `WorkingDirectory`
- `ExecStart`

例:

```sh
sudo systemctl edit --full strata-pvr-wui.service
```

## 4. 起動する

`operator` と `wui` は常駐 service として起動します。

```sh
sudo systemctl enable --now strata-pvr-operator.service
sudo systemctl enable --now strata-pvr-wui.service
```

`scheduler` は短時間で終了する処理なので、timer で定期実行します。

```sh
sudo systemctl enable --now strata-pvr-scheduler.timer
```

すぐに一度だけ scheduler を実行したい場合:

```sh
sudo systemctl start strata-pvr-scheduler.service
```

## 5. 状態を確認する

```sh
systemctl status strata-pvr-operator.service
systemctl status strata-pvr-wui.service
systemctl status strata-pvr-scheduler.timer
```

timer の実行予定は次のコマンドで確認できます。

```sh
systemctl list-timers 'strata-pvr-*'
```

## 6. ログを確認する

systemd journal:

```sh
journalctl -u strata-pvr-operator.service -f
journalctl -u strata-pvr-wui.service -f
journalctl -u strata-pvr-scheduler.service
```

Strata PVR の互換ログ:

```sh
sudo tail -f /opt/strata-pvr/log/operator
sudo tail -f /opt/strata-pvr/log/wui
sudo tail -f /opt/strata-pvr/log/scheduler
```

## 7. 実行ユーザーを変更する場合

同梱の unit は systemd の `User=` / `Group=` を指定していません。これは
Strata PVR が `config.json` の `uid` / `gid` を読み込んで、対応する処理で
権限を落とす挙動を保つためです。

systemd 側で実行ユーザーを固定したい場合は、各 service に `User=` と
`Group=` を追加してください。

例:

```ini
User=strata-pvr
Group=strata-pvr
```

その場合は、次の場所をそのユーザーが読み書きできるようにしてください。

- `/opt/strata-pvr`
- `/opt/strata-pvr/data`
- `/opt/strata-pvr/log`
- `config.json` の録画保存先ディレクトリ

## 8. 停止・再起動

```sh
sudo systemctl restart strata-pvr-operator.service
sudo systemctl restart strata-pvr-wui.service
sudo systemctl stop strata-pvr-scheduler.timer
```

完全に自動起動を無効化する場合:

```sh
sudo systemctl disable --now strata-pvr-operator.service
sudo systemctl disable --now strata-pvr-wui.service
sudo systemctl disable --now strata-pvr-scheduler.timer
```
