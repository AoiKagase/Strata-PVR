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
| `updater` | intentionally changed | Accepted, but automatic git/service/installer operations are not performed. This avoids Node/npm runtime assumptions and destructive service changes; users should update repository/binary explicitly. |
| `service <operator|wui> <initscript|execute>` | partially compatible | Initscript generation implemented for Go binary shape. `operator execute` runs the Go operator loop; `wui execute` starts the Go WUI/API server. |
| `update [-s|--simulation]` | partially compatible | Fetches Mirakurun services/programs/tuners, writes schedule/reserves, applies rules/manual/skip/conflict logic, maintains `data/scheduler.pid`, runs scheduler/EPG/conflict hooks, and logs result counters. Exact logging remains incomplete. |
| `search` | partially compatible | Filters `data/schedule.json` with rule-style options plus `-id`, `-now`, `-today`, `-tomorrow`, `-simple`, `-detail`, and `-n/--num`. Output is tabular but not yet byte-for-byte `easy-table`; `config.normalizationForm` matching remains incomplete. |
| `reserve <pgid> [-s|--simulation] [--1seg]` | partially compatible | Reads schedule and writes reserves; exact table/output still incomplete. |
| `unreserve <pgid>` | partially compatible | Data side effect implemented in CLI package. |
| `skip <pgid>` | partially compatible | Data side effect implemented in CLI package. |
| `unskip <pgid>` | partially compatible | Data side effect implemented in CLI package. |
| `stop <pgid>` | partially compatible | Marks recording entry with `abort:true` and sets the matching auto reserve to `isSkip:true` like the Node CLI. |
| `rule` | partially compatible | Adds/updates/removes rules with core matching fields. Supports Node-style deletion markers such as `-title null` and `-start -1`; table output still needs more work. |
| `enrule <rule#>` | partially compatible | Alias for `rule -n <rule#> --enable`. |
| `disrule <rule#>` | partially compatible | Alias for `rule -n <rule#> --disable`. |
| `rmrule <rule#>` | partially compatible | Alias for `rule -n <rule#> --remove`. |
| `rules` | partially compatible | JSON/table formatting incomplete. |
| `reserves` | partially compatible | Basic list scaffold only. |
| `recording` | partially compatible | Basic list scaffold only. |
| `recorded` | partially compatible | Basic list scaffold only. |
| `cleanup` | partially compatible | Removes missing recorded entries; prompt not yet matched. |
| `compat check`, `compat doctor` | implemented | New Go-only safety checks; does not alter legacy command behavior. |
| `ircbot` | intentionally changed | Command is accepted, but the experimental Node-era IRC bot is not implemented; use WUI/API or an external bot against the Go API. |
| `test <app> [options]` | intentionally changed | Accepted with usage validation and Go-runtime guidance, but Node-era `usr/bin/<app>` execution is not performed. |
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
| `excludeServices` | Mirakurun service IDs excluded from schedule import. | implemented |
| `serviceOrder` | Service IDs moved to the front in schedule order. | implemented |
| `wuiUsers` | Basic auth users as `user:pass`. | implemented for the authenticated listener. |
| `wuiAllowCountries` | GeoIP country allow list. | not started |
| `wuiPort`, `wuiHost` | Deprecated authenticated listener. | partially compatible; starts a separate authenticated HTTP/HTTPS server when `wuiPort` is set. |
| `wuiTlsKeyPath`, `wuiTlsCertPath`, `wuiTlsPassphrase`, `wuiTlsRequestCert`, `wuiTlsRejectUnauthorized`, `wuiTlsCaPath` | TLS listener settings. | partially compatible; cert/key listener, client certificate request/verification, and CA pool loading are implemented. Encrypted key passphrase handling remains incomplete. |
| `wuiOpenServer`, `wuiOpenHost`, `wuiOpenPort` | Unauthenticated LAN listener. | partially compatible; starts a separate HTTP server without Basic auth and selects a private IPv4 when `wuiOpenHost` is unset. mDNS remains incomplete. |
| `wuiXFF` | Trust first `X-Forwarded-For` IP. | partially compatible; access logging uses the first forwarded address and normalizes IPv4-mapped IPv6. GeoIP country filtering is still not implemented. |
| `wuiMdnsAdvertisement` | mDNS advertisement. | not started |
| `normalizationForm` | Unicode normalization form used by title/detail matching. | partially compatible |
| `recordedFormat` | Filename template. | partially compatible |
| `recordingPriority`, `conflictedPriority` | Mirakurun stream priorities. | partially compatible; Go sets `X-Mirakurun-Priority` before program stream requests. Conflict recordings remain limited because Go currently skips conflict reserves. |
| `storageLowSpaceThresholdMB`, `storageLowSpaceAction`, `storageLowSpaceNotifyTo`, `storageLowSpaceCommand` | Low disk behavior. | partially compatible; `remove`, `stop`, hook command, sendmail notification, and three-hour notification throttling are implemented. |
| `schedulerStartCommand`, `schedulerEndCommand`, `epgStartCommand`, `epgEndCommand`, `conflictCommand`, `recordedCommand` | Hook subprocesses. Scheduler and operator hooks are implemented. Difference: Go waits for all scheduler hook commands to exit; Node started `epgEndCommand`, `conflictCommand`, and `schedulerEndCommand` asynchronously. |
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

Rule matching status: partially compatible. Type/channel/category/hour/duration/title/detail/flag checks are implemented in Go. JavaScript RegExp semantics are approximated with Go regexp and need oracle tests for edge cases. CLI rule add/update/enable/disable/remove is implemented for core fields, including Node-style `null`/`-1` deletion markers.

## Data Files And Schemas

| File | Schema | Writer(s) | Status |
| --- | --- | --- | --- |
| `config.json` | JSON object, unknown fields allowed. | API config PUT writes the supplied `json` query value after validation. | partially compatible |
| `rules.json` | Array of rule objects. Pretty printed by rule/API writes. | CLI/API | partially compatible |
| `data/schedule.json` | Array of channel objects with `programs`. | scheduler | partially compatible |
| `data/reserves.json` | Array of program objects. | scheduler/CLI/API/operator | partially compatible |
| `data/recording.json` | Array of recording program objects; `abort:true` requests stop. Go operator now polls this file while recording and closes the active stream when abort is set. CLI stop also updates matching auto reserves to skip. | operator/CLI/API | partially compatible |
| `data/recorded.json` | Array of recorded program objects with `recorded` path. | operator/cleanup/API | partially compatible |
| `data/scheduler.pid` | Scheduler process id text written while `update` or WUI scheduler force runs and removed on exit. | scheduler/WUI status | implemented |
| `data/operator.pid` | Operator process id text written by `service operator execute` and removed on exit. | operator/WUI status | implemented |
| `log/scheduler` | Scheduler log stream with `RUNNING SCHEDULER.`, `RESERVE:`, `CONFLICT:`, `SKIP:`, and `MATCHES`/`DUPLICATES`/`CONFLICTS`/`SKIPS`/`RESERVES` result counters. | scheduler/WUI | partially compatible |
| `log/operator` | Operator log stream with `START:` and `FIN:` lines. | operator/WUI | partially compatible |
| `log/wui` | WUI log stream with HTTP/HTTPS server start/close/error lines. | WUI/API | partially compatible |

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

API implementation status: partially compatible. The Go WUI currently implements JSON reads for status/config/rules/schedule/schedule channel/schedule programs/schedule broadcasting/reserves/recording/recorded/program lookup, config PUT with the `json` query parameter, scheduler status/log/update/force, storage usage, log reads, rules create/update/delete/enable/disable, program PUT manual reservation, reserve skip/unskip/delete with manual-only delete semantics, recording abort marking with auto-reserve skip, recorded item delete, recorded file stat/stream/delete, recorded/recording preview routes with `previewer:false` 403 behavior, `status.feature.streamer:true` for WUI watch controls, recorded/recording watch XSPF and m2ts, channel logo, channel watch XSPF, channel watch m2ts proxy, recorded cleanup via PUT, and recorded/reserve/recording item reads. Preview image generation, mp4 transcode, compression, live log tailing, and exact status fields remain incomplete.

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

Current Go client status: partially compatible for HTTP and `http+unix` URL setup plus services/programs/tuners, program stream, service stream, service logo requests, and `X-Mirakurun-Priority`.

## Side Effects

- Wrapper creates `config.json` and `rules.json` from samples during `service ... execute` if missing.
- Wrapper ensures `log/` and `data/`.
- Scheduler writes `data/schedule.json`, `data/reserves.json`, and maintains `data/scheduler.pid` while running.
- Scheduler logs reserve/conflict/skip lines and the Node-style result counters. Difference: conflict lines currently use `CONFLICT:` instead of Node's `!CONFLICT:` so the Go WUI parser can continue reading them.
- Scheduler runs `epgStartCommand`, `epgEndCommand`, `schedulerStartCommand`, `conflictCommand`, and `schedulerEndCommand`, passing the same path/counter/program arguments as the Node scheduler. Go waits for these hooks to finish.
- Operator clears `data/recording.json` on start.
- Operator creates `recordedDir` and nested recorded directories.
- Operator writes `data/recording.json`, `data/recorded.json`, and may remove manual reserves.
- Operator writes recorded files directly to final path with append mode.
- Go operator currently starts due non-skip/non-conflict reserves 15 seconds before start, writes `data/recording.json`, records the Mirakurun decoded program stream, appends `data/recorded.json`, and removes the completed reserve.
- Go operator writes to a temporary `.recording-*` file and renames it after a successful copy. This is an intentional safety improvement and is not byte-for-byte identical to the old direct final-path write behavior.
- Go operator polls `abort:true` during an active stream and runs `recordedCommand` with recorded file path plus program JSON after state writes. Low-storage command plus `remove`/`stop` actions, sendmail notification, and notification throttling are partially implemented; exact operator logs and every signal side effect remain incomplete.
- Cleanup removes missing file entries from `data/recorded.json`.
- WUI/API may rewrite config, rules, reserves, recording, recorded. Config PUT validates the supplied JSON but stores the raw query value to preserve the Node API shape.
- Go WUI recorded file stat preserves the legacy JSON field names, including `ulink`, but platform-specific inode/device/block fields may be zero when unavailable.
- Go WUI `log/:name/stream.txt` currently returns the padding plus current log contents and does not keep a live `tail -f` subprocess open.
- Go WUI scheduler JSON parses `RESERVE:` and `CONFLICT:` lines from `log/scheduler`; exact old shell `tac/sed` behavior is approximated in Go.
- Go WUI status includes operator/scheduler PID values when PID files are present and checks whether the referenced process is alive before setting `alive:true`.
- Go WUI recorded/recording watch supports XSPF and direct m2ts file serving. Recording watch currently serves the current file contents and does not keep a live `tail -f` stream open.
- Old wrapper installer/updater run git, wget, npm, and ffmpeg installation steps. Go runtime intentionally does not require Node/npm; Go updater is a safe no-op guidance command.

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
| Operator/recorder | partially compatible; active `abort:true` polling, `recordedCommand` execution, `data/operator.pid` lifecycle, and low-storage `remove`/`stop`/sendmail core actions with throttling implemented, but exact logs and signal side effects remain incomplete. |
| WUI/API | partially compatible |
| Installer/updater | partially compatible |
| Logging | partially compatible |
| Compat doctor/check | implemented |
| Tests | partially compatible |
