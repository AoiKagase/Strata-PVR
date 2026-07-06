# Chinachu Go Migration Compatibility

This document tracks compatibility against the gamma branch implementation in
`../Chinachu`. It must be updated whenever behavior is implemented or changed.

## Audit Source

- Wrapper: `chinachu`
- CLI: `app-cli.js`
- Scheduler: `app-scheduler.js`
- Operator: `app-operator.js`
- WUI/API: `app-wui.js`, `api/resource-*.json`, `api/script-*.vm.js`
- Shared behavior: `common/lib/chinachu-common.js`
- Samples: `config.sample.json`, `rules.sample.json`
- Static WUI: `web/`

## CLI Commands And Options

Top-level commands accepted by `./chinachu`:

| Command | Current Go status | Notes |
| --- | --- | --- |
| `installer` | partially compatible | Accepted. Node/npm installation is intentionally not performed. |
| `updater` | not started | Existing command uses git, prompts, and optional installer. |
| `service <operator|wui> <initscript|execute>` | partially compatible | Initscript generation implemented for Go binary shape. `operator execute` runs the Go operator loop; `wui execute` starts the Go WUI/API server. |
| `update [-s|--simulation]` | partially compatible | Fetches Mirakurun services/programs/tuners, writes schedule/reserves, applies rules/manual/skip/conflict logic. Logging/hooks/PID are incomplete. |
| `search` | partially compatible | Basic filtering/listing scaffold only. |
| `reserve <pgid> [-s|--simulation] [--1seg]` | partially compatible | Reads schedule and writes reserves; exact table/output still incomplete. |
| `unreserve <pgid>` | partially compatible | Data side effect implemented in CLI package. |
| `skip <pgid>` | partially compatible | Data side effect implemented in CLI package. |
| `unskip <pgid>` | partially compatible | Data side effect implemented in CLI package. |
| `stop <pgid>` | partially compatible | Marks recording entry with `abort:true`. |
| `rule` | partially compatible | Adds/updates/removes rules with core matching fields. Table output and `null` field deletion compatibility need more work. |
| `enrule <rule#>` | partially compatible | Alias for `rule -n <rule#> --enable`. |
| `disrule <rule#>` | partially compatible | Alias for `rule -n <rule#> --disable`. |
| `rmrule <rule#>` | partially compatible | Alias for `rule -n <rule#> --remove`. |
| `rules` | partially compatible | JSON/table formatting incomplete. |
| `reserves` | partially compatible | Basic list scaffold only. |
| `recording` | partially compatible | Basic list scaffold only. |
| `recorded` | partially compatible | Basic list scaffold only. |
| `cleanup` | partially compatible | Removes missing recorded entries; prompt not yet matched. |
| `compat check`, `compat doctor` | implemented | New Go-only safety checks; does not alter legacy command behavior. |
| `ircbot` | not started | Experimental IRC bot. |
| `test <app> [options]` | intentionally changed | Node-era `usr/bin` app runner is not a runtime requirement. |
| default/help | implemented | Help command shape is present. |

Options accepted by `app-cli.js`:

- `-mode`, `--mode`
- `-s`, `--simulation`
- `-en`, `--enable`
- `-dis`, `--disable`
- `-rm`, `--remove`
- `-simple`, `--simple`
- `-detail`, `--detail`
- `-n`, `--num`
- `-now`, `--now`
- `-today`, `--today`
- `-tomorrow`, `--tomorrow`
- `-id`, `--id`
- `-type`, `--type`
- `-ch`, `--channel`
- `-^ch`, `--ignore-channels`
- `-sid`, `--service-id`
- `-cat`, `--category`
- `-start`, `--start`
- `-end`, `--end`
- `-mini`, `--minimum`
- `-maxi`, `--maximum`
- `-title`, `--titles`
- `-^title`, `--ignore-titles`
- `-desc`, `--descriptions`
- `-^desc`, `--ignore-descriptions`
- `-flag`, `--flags`
- `-^flag`, `--ignore-flags`
- `-host`, `--host`
- `-port`, `--port`
- `-nick`, `--nick`
- `-1seg`, `--1seg`

## `config.json` Fields

Fields from `config.sample.json` and JS references:

| Field | Semantics | Status |
| --- | --- | --- |
| `uid`, `gid` | Drop privileges when started as root. | not started |
| `mirakurunPath` | Mirakurun base URL; supports HTTP and `http+unix`. | partially compatible |
| `schedulerMirakurunPath` | Legacy fallback for Mirakurun URL. | partially compatible |
| `recordedDir` | Directory prefix for recorded files. | partially compatible |
| `vaapiEnabled`, `vaapiDevice` | WUI transcode/preview support. | not started |
| `excludeServices` | Mirakurun service IDs excluded from schedule import. | not started |
| `serviceOrder` | Service IDs moved to the front in schedule order. | not started |
| `wuiUsers` | Basic auth users as `user:pass`. | not started |
| `wuiAllowCountries` | GeoIP country allow list. | not started |
| `wuiPort`, `wuiHost` | Deprecated authenticated listener. | not started |
| `wuiTlsKeyPath`, `wuiTlsCertPath`, `wuiTlsPassphrase`, `wuiTlsRequestCert`, `wuiTlsRejectUnauthorized`, `wuiTlsCaPath` | TLS listener settings. | not started |
| `wuiOpenServer`, `wuiOpenHost`, `wuiOpenPort` | Unauthenticated LAN listener. | not started |
| `wuiXFF` | Trust first `X-Forwarded-For` IP. | not started |
| `wuiMdnsAdvertisement` | mDNS advertisement. | not started |
| `normalizationForm` | Unicode normalization form used by title/detail matching. | partially compatible |
| `recordedFormat` | Filename template. | partially compatible |
| `recordingPriority`, `conflictedPriority` | Mirakurun stream priorities. | not started |
| `storageLowSpaceThresholdMB`, `storageLowSpaceAction`, `storageLowSpaceNotifyTo`, `storageLowSpaceCommand` | Low disk behavior. | not started |
| `schedulerStartCommand`, `schedulerEndCommand`, `epgStartCommand`, `epgEndCommand`, `conflictCommand`, `recordedCommand` | Hook subprocesses. | not started |
| `operTweeter`, `operTweeterAuth`, `operTweeterFormat` | Experimental Twitter notifications. | not started |

Unknown fields are preserved by the config loader.

## `rules.json` Fields

Known rule fields:

- `isDisabled`
- `sid`
- `types`
- `channels`
- `ignore_channels`
- `category`
- `categories`
- `hour.start`, `hour.end`
- `duration.min`, `duration.max`
- `reserve_titles`
- `ignore_titles`
- `reserve_descriptions`
- `ignore_descriptions`
- `reserve_flags`
- `ignore_flags`
- `recorded_format`

Rule matching status: partially compatible. Type/channel/category/hour/duration/title/detail/flag checks are implemented in Go. JavaScript RegExp semantics are approximated with Go regexp and need oracle tests for edge cases. CLI rule add/update/enable/disable/remove is partially implemented.

## Data Files And Schemas

| File | Schema | Writer(s) | Status |
| --- | --- | --- | --- |
| `config.json` | JSON object, unknown fields allowed. | API config PUT | partially compatible |
| `rules.json` | Array of rule objects. Pretty printed by rule/API writes. | CLI/API | partially compatible |
| `data/schedule.json` | Array of channel objects with `programs`. | scheduler | partially compatible |
| `data/reserves.json` | Array of program objects. | scheduler/CLI/API/operator | partially compatible |
| `data/recording.json` | Array of recording program objects; `abort:true` requests stop. | operator/CLI/API | partially compatible |
| `data/recorded.json` | Array of recorded program objects with `recorded` path. | operator/cleanup/API | partially compatible |
| `data/scheduler.pid` | Scheduler process id text. | scheduler | not started |
| `log/scheduler` | Scheduler log stream. | wrapper/operator | not started |
| `log/operator` | Operator log stream. | wrapper/WUI | not started |
| `log/wui` | WUI log stream. | wrapper/WUI | not started |

Writes in Go use temp-file-and-rename atomic JSON helpers.

## API Routes

Routes discovered from `api/resource-*.json`:

- `/api/channel/:chid/watch.{xspf,m2ts,mp4}` GET
- `/api/channel/:chid/logo.png` GET
- `/api/config.json` GET, PUT
- `/api/log/:name.txt` GET
- `/api/log/:name/stream.txt` GET
- `/api/program/:id.json` GET, PUT
- `/api/recorded.json` GET, PUT
- `/api/recorded/:id.json` GET, DELETE
- `/api/recorded/:id/file.{json,m2ts}` GET, DELETE
- `/api/recorded/:id/preview.{png,jpg,txt}` GET
- `/api/recorded/:id/watch.{mp4,xspf,m2ts}` GET
- `/api/recording.json` GET
- `/api/recording/:id.json` GET, DELETE
- `/api/recording/:id/preview.{png,jpg,txt}` GET
- `/api/recording/:id/watch.{xspf,m2ts,mp4}` GET
- `/api/reserves.json` GET
- `/api/reserves/:id.json` GET, DELETE
- `/api/reserves/:id/:action.json` PUT
- `/api/rules.json` GET, POST
- `/api/rules/:num.json` GET, PUT, DELETE
- `/api/rules/:num/:action.json` PUT
- `/api/schedule.json` HEAD, GET
- `/api/schedule/:chid.json` GET
- `/api/schedule/programs.json` GET
- `/api/schedule/broadcasting.json` GET
- `/api/schedule/:chid/programs.json` GET
- `/api/schedule/:chid/broadcasting.json` GET
- `/api/scheduler.{json,txt}` GET, PUT
- `/api/scheduler/force.json` PUT
- `/api/status.json` GET
- `/api/storage.json` GET

API implementation status: partially compatible. The Go WUI currently implements JSON reads for status/config/rules/schedule/schedule programs/reserves/recording/recorded/program lookup, scheduler status/log/update/force, storage usage, log reads, rules create/update/delete/enable/disable, program PUT manual reservation, reserve skip/unskip/delete with manual-only delete semantics, recording abort marking with auto-reserve skip, recorded item delete, recorded file stat/stream/delete, recorded/recording watch XSPF and m2ts, channel logo, channel watch XSPF, channel watch m2ts proxy, recorded cleanup via PUT, and recorded/reserve/recording item reads. Preview, mp4 transcode, compression, live log tailing, and exact status fields remain incomplete.

## WUI / Static Assets

The old WUI serves `web/` directly with static files, range support, cache headers for icons/images, and API dispatch under `/api/`. The Go implementation serves static files from `web/` when present and can fall back to `../Chinachu/web` during development. Current status: partially compatible; Node-based frontend builds are not required.

## Mirakurun Endpoints Used

The JS Mirakurun client calls:

- services list: used by scheduler.
- programs list: used by scheduler.
- tuners list: used by scheduler.
- program stream by Mirakurun program ID, decoded=true: used by operator recording.
- service/channel stream: used by WUI watch routes.
- service logo: used by channel logo route.

Current Go client status: partially compatible for HTTP and `http+unix` URL setup plus services/programs/tuners, program stream, service stream, and service logo requests.

## Side Effects

- Wrapper creates `config.json` and `rules.json` from samples during `service ... execute` if missing.
- Wrapper ensures `log/` and `data/`.
- Scheduler writes `data/schedule.json`, `data/reserves.json`; `data/scheduler.pid` is not implemented yet.
- Scheduler runs hook commands with paths and counters.
- Operator clears `data/recording.json` on start.
- Operator creates `recordedDir` and nested recorded directories.
- Operator writes `data/recording.json`, `data/recorded.json`, and may remove manual reserves.
- Operator writes recorded files directly to final path with append mode.
- Go operator currently starts due non-skip/non-conflict reserves 15 seconds before start, writes `data/recording.json`, records the Mirakurun decoded program stream, appends `data/recorded.json`, and removes the completed reserve.
- Go operator writes to a temporary `.recording-*` file and renames it after a successful copy. This is an intentional safety improvement and is not byte-for-byte identical to the old direct final-path write behavior.
- Go operator does not yet poll `abort:true` during an active stream, run `recordedCommand`, apply low-storage actions, or mirror every signal/log side effect.
- Cleanup removes missing file entries from `data/recorded.json`.
- WUI/API may rewrite config, rules, reserves, recording, recorded.
- Go WUI recorded file stat preserves the legacy JSON field names, including `ulink`, but platform-specific inode/device/block fields may be zero when unavailable.
- Go WUI `log/:name/stream.txt` currently returns the padding plus current log contents and does not keep a live `tail -f` subprocess open.
- Go WUI scheduler JSON parses `RESERVE:` and `CONFLICT:` lines from `log/scheduler`; exact old shell `tac/sed` behavior is approximated in Go.
- Go WUI status includes operator/scheduler PID values when PID files are present, but does not yet verify process liveness beyond PID file presence.
- Go WUI recorded/recording watch supports XSPF and direct m2ts file serving. Recording watch currently serves the current file contents and does not keep a live `tail -f` stream open.
- Old wrapper installer/updater run git, wget, npm, and ffmpeg installation steps. Go runtime intentionally does not require Node/npm.

## Compatibility Status Matrix

| Area | Status |
| --- | --- |
| Audit | partially compatible |
| Go module skeleton | implemented |
| Config loading | partially compatible |
| Atomic JSON state | implemented |
| CLI command acceptance | partially compatible |
| Rule engine | partially compatible |
| Recorded filename format | partially compatible |
| Mirakurun client | partially compatible |
| Scheduler | partially compatible |
| Operator/recorder | partially compatible |
| WUI/API | partially compatible |
| Installer/updater | partially compatible |
| Logging | not started |
| Compat doctor/check | implemented |
| Tests | partially compatible |
