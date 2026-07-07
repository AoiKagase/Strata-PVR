# Strata PVR Migration Compatibility

This document tracks Strata PVR compatibility against the legacy gamma branch
implementation in the sibling source tree. It must be updated whenever behavior
is implemented or changed.

Formal name: Strata PVR

Description: A Chinachu-compatible PVR for Mirakurun, written in Go.

## Audit Source

- Wrapper: `chinachu`
- CLI: `app-cli.js`
- Scheduler: `app-scheduler.js`
- Operator: `app-operator.js`
- WUI/API: `app-wui.js`, `api/resource-*.json`, `api/script-*.vm.js`
- Shared behavior: `common/lib/chinachu-common.js`
- Samples: `config.sample.json`, `rules.sample.json` are included in the Go tree and covered by parse tests.
- Static WUI: `web/`

## CLI Commands And Options

Top-level commands accepted by `./chinachu`:

The Go CLI also accepts the legacy internal `app-cli.js` shape `-mode <command>`,
`--mode <command>`, and `--mode=<command>` before dispatching to the same command
handlers.
For reservation/recording mutation commands, program IDs are accepted as either
positional arguments or legacy `-id`/`--id` options, with boolean flags allowed
before or after the ID.

| Command | Current Go status | Notes |
| --- | --- | --- |
| `installer` | intentionally changed | Accepted. Node/npm and Node-era dependency installation are intentionally not performed; build or install the Go binary directly. |
| `updater` | intentionally changed | Accepted, but automatic git/service/installer operations are not performed. This avoids Node/npm runtime assumptions and destructive service changes; users should update repository/binary explicitly. |
| `service <operator|wui> <initscript|execute>` | partially compatible | Initscript generation uses the `strata-pvr` binary, `strata-pvr-*` service names, legacy-compatible `/var/run/chinachu-*.pid` PID files, start/stop/restart/status handling, `su $USER` launch, process-group `SIGQUIT` stop behavior, and an explicit `STRATA_PVR_DIR` working directory. `execute` creates missing `config.json`/`rules.json` from samples and ensures `log/` and `data/`; `operator execute` runs the Go operator loop; `wui execute` starts the Go WUI/API server. |
| `update [-s|--simulation]` | partially compatible | Fetches Mirakurun services/programs/tuners, writes schedule/reserves, applies rules/manual/skip/conflict logic, maintains `data/scheduler.pid`, runs scheduler/EPG/conflict hooks, and logs legacy Mirakurun fetch/error/reserve/conflict/skip/write/tuner/duplicate/result lines. |
| `search` | partially compatible | Filters `data/schedule.json` with rule-style options plus `-id`, `-now`, `-today`, `-tomorrow`, `-simple`, `-detail`, and `-n/--num`. CLI search/list title and detail matching now honors `config.normalizationForm` for NFC/NFD/NFKC/NFKD; output uses an `easy-table`-style padded table including simple/detail column behavior. |
| `reserve <pgid> [-s|--simulation] [--1seg]` | partially compatible | Reads schedule and writes reserves, supports simulation output, the `1seg` flag, legacy `-id/--id` program ID options, and the legacy schedule-before-duplicate error order; known program/channel JSON fields now emit in legacy struct order, but unknown-field insertion order and obscure spacing edge cases remain incomplete. |
| `unreserve <pgid> [-s|--simulation]` | partially compatible | Data side effect, simulation output, and legacy `-id/--id` program ID options implemented; known program/channel JSON fields now emit in legacy struct order, but unknown-field insertion order and obscure spacing edge cases remain incomplete. |
| `skip <pgid> [-s|--simulation]` | partially compatible | Data side effect, simulation output, target JSON output, and legacy `-id/--id` program ID options implemented; known program/channel JSON fields now emit in legacy struct order, but unknown-field insertion order and obscure spacing edge cases remain incomplete. |
| `unskip <pgid> [-s|--simulation]` | partially compatible | Data side effect, simulation output, legacy `skip:` output label, legacy pre-update target JSON output, `isSkip` property removal on write, and legacy `-id/--id` program ID options implemented; known program/channel JSON fields now emit in legacy struct order, but unknown-field insertion order and obscure spacing edge cases remain incomplete. |
| `stop <pgid> [-s|--simulation]` | partially compatible | Marks recording entry with `abort:true`, sets the matching auto reserve to `isSkip:true`, supports simulation/JSON output like the Node CLI, and accepts legacy `-id/--id` program ID options. |
| `rule` | partially compatible | Adds/updates/removes rules with core matching fields. Supports Node-style deletion markers such as `-title null` and `-start -1`; known rule JSON fields now emit in legacy-oriented order with `isDisabled` last, but unknown/insertion-order edge cases still differ from Node. |
| `enrule <rule#>` | partially compatible | Alias for `rule -n <rule#> --enable`. |
| `disrule <rule#>` | partially compatible | Alias for `rule -n <rule#> --disable`. |
| `rmrule <rule#>` | partially compatible | Alias for `rule -n <rule#> --remove`. |
| `rules` | partially compatible | Prints an `easy-table`-style padded rule table with `-n`, `-detail`, and transposed single-row output. Remaining risk is byte-for-byte differences in obscure `easy-table` truncation/alignment edge cases. |
| `reserves` | partially compatible | Prints an `easy-table`-style padded program table with filtering/sort support. |
| `recording` | partially compatible | Prints an `easy-table`-style padded program table with filtering/sort support. |
| `recorded` | partially compatible | Prints an `easy-table`-style padded program table with filtering/sort support. |
| `cleanup [-s|--simulation]` | partially compatible | Prints an `easy-table`-style padded action table and removes missing recorded entries unless simulation is set. Before destructive writes, Go creates `data/recorded.json.bak-YYYYMMDDHHMMSS`; when nothing is removed it leaves `data/recorded.json` untouched. |
| `compat check`, `compat doctor`, `compat diff`, `compat backup`, `compat wrapper` | implemented | New Go-only safety checks for required JSON state files, included sample files, and expected JSON shapes, `data/`, writable `log/`, writable `recordedDir`, WUI static assets, available disk space lookup, `ffmpeg`/`ffprobe` command availability, Mirakurun services/programs/tuners reachability, Node.js runtime non-requirement, warnings for intentionally omitted personal-use-overkill integrations, `compat doctor` non-secret config summary, state-count summary, conservative next-step output for backup/update/reserve review/manual WUI and operator execution, active-recording migration warning, and local wrapper-target binary warning output, dry-run JSON rewrite difference reporting, timestamped JSON state backups under `backup/`, plus a safe shell wrapper generator that prints to stdout and never overwrites existing files. |
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
| `uid`, `gid` | Drop privileges when started as root. | partially compatible; operator and WUI call `setgid` then `setuid` on Unix, defaulting missing `gid` to `video` and requiring `uid` when running as root. |
| `mirakurunPath` | Mirakurun base URL; supports HTTP, `http+unix`, and legacy `http://unix:` socket URLs. | partially compatible |
| `schedulerMirakurunPath` | Legacy fallback for Mirakurun URL. | partially compatible |
| `recordedDir` | Directory prefix for recorded files. | partially compatible; operator startup creates it when missing and logs legacy `MKDIR:`. |
| `vaapiEnabled`, `vaapiDevice` | WUI transcode/preview support. | partially compatible; fields are parsed from existing config and used for WUI mp4 watch/transcode ffmpeg arguments. MP4 watch is browser-oriented fragmented MP4 generated on demand by `ffmpeg`; if `ffmpeg` is not on the WUI process `PATH`, these routes return 503 and log the spawn error. Preview image generation follows the legacy non-VAAPI ffmpeg command. |
| `excludeServices` | Mirakurun service IDs excluded from schedule import. | implemented |
| `serviceOrder` | Service IDs moved to the front in schedule order. | implemented |
| `wuiUsers` | Basic auth users as `user:pass`. | implemented for the authenticated listener, including the legacy `WWW-Authenticate: Basic realm="Authentication."` challenge. `compat check` warns when the sample `strata:yoshikawa` or legacy `chinachu:yoshikawa` credential is still configured. |
| `wuiAllowCountries` | GeoIP country allow list. | intentionally changed; the existing config field is parsed explicitly and `compat check` warns when set, but runtime GeoIP filtering is not implemented because the Node version depends on the `geoip-lite` database and personal deployments should restrict access at firewall/reverse proxy if needed. The Go sample leaves this disabled by default. |
| `wuiPort`, `wuiHost` | Deprecated authenticated listener. | partially compatible; starts a separate authenticated HTTP/HTTPS server when `wuiPort` is set. |
| `wuiTlsKeyPath`, `wuiTlsCertPath`, `wuiTlsPassphrase`, `wuiTlsRequestCert`, `wuiTlsRejectUnauthorized`, `wuiTlsCaPath` | TLS listener settings. | partially compatible; cert/key listener, encrypted PEM key passphrase handling, client certificate request/verification, and CA pool loading are implemented. PFX-style key material is not implemented. |
| `wuiOpenServer`, `wuiOpenHost`, `wuiOpenPort` | Unauthenticated LAN listener. | partially compatible; starts a separate HTTP server without Basic auth and selects a private IPv4 when `wuiOpenHost` is unset. `compat check` warns when this unauthenticated listener is enabled. |
| `wuiXFF` | Trust first `X-Forwarded-For` IP. | partially compatible; access logging uses the first forwarded address and normalizes IPv4-mapped IPv6. GeoIP country filtering is still not implemented. |
| `wuiMdnsAdvertisement` | mDNS advertisement. | intentionally changed; the existing config field is parsed explicitly and `compat check` warns when set, but mDNS service advertisement is not implemented to avoid an extra runtime dependency for personal deployments. The Go sample leaves this disabled by default. |
| `normalizationForm` | Unicode normalization form used by title/detail matching. | partially compatible; NFC/NFD/NFKC/NFKD are implemented for CLI search/list filters and scheduler rule matching. Unknown values are treated as no normalization. |
| `recordedFormat` | Filename template. | partially compatible; supports legacy date masks/tokens including `UTC:` prefix, named `dateformat` masks, ordinal/timezone/millisecond tokens, plus id/type/channel/channel-id/channel-sid/channel-name/tuner/title/fulltitle/subtitle/episode/episode:N/category tokens, unknown-token `undefined` replacement, and filename character stripping. Remaining risk is obscure JavaScript `dateformat` parsing edge cases not covered by Go tests. |
| `recordingPriority`, `conflictedPriority` | Mirakurun stream priorities. | partially compatible; Go sets `X-Mirakurun-Priority` before program stream requests and records conflict reserves with `conflictedPriority`, matching the old operator. |
| `storageLowSpaceThresholdMB`, `storageLowSpaceAction`, `storageLowSpaceNotifyTo`, `storageLowSpaceCommand` | Low disk behavior. | partially compatible; `remove`, `stop`, hook command, sendmail notification, and three-hour notification throttling are implemented. |
| `schedulerStartCommand`, `schedulerEndCommand`, `epgStartCommand`, `epgEndCommand`, `conflictCommand`, `recordedCommand` | Hook subprocesses. Scheduler and operator hooks are implemented. Go waits for legacy synchronous `epgStartCommand` and `schedulerStartCommand`, while `epgEndCommand`, `conflictCommand`, and `schedulerEndCommand` are started asynchronously like the Node scheduler. |
| `operTweeter`, `operTweeterAuth`, `operTweeterFormat` | Experimental Twitter notifications. | intentionally changed; existing config fields are parsed explicitly and `compat check` warns when enabled, but Twitter posting is not implemented because the Node-era `mtwitter` integration is unnecessary for this personal-use Go runtime. |

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

Rule matching status: partially compatible. Type/channel/category/hour/duration/title/detail/flag checks are implemented in Go. Scheduler rule matching follows `chinachu-common` by checking `program.fullTitle` for title rules and accepting legacy channel forms including `type_sid`; CLI search/list filtering uses the legacy `app-cli.js` local behavior of checking `program.title` and comparing `channels`/`ignore_channels` only with `program.channel.channel`; CLI-only `ignore_descriptions` and `reserve_flags` also preserve the legacy behavior of failing to match programs without `detail`. NFC/NFD/NFKC/NFKD `normalizationForm` values are applied to both regex patterns and title/detail text for CLI search/list and scheduler rule matching. Duration matching preserves the legacy behavior of applying the rule only when both `min` and `max` are present in JSON. JavaScript RegExp semantics are approximated with Go regexp and need oracle tests for edge cases. CLI rule add/update/enable/disable/remove is implemented for core fields, including Node-style `null`/`-1` deletion markers.

## Data Files And Schemas

| File | Schema | Writer(s) | Status |
| --- | --- | --- | --- |
| `config.json` | JSON object, unknown fields allowed. | API config PUT writes the supplied `json` query value after validation. | partially compatible |
| `rules.json` | Array of rule objects. Pretty printed by rule/API writes; known fields emit in a stable legacy-oriented order. | CLI/API | partially compatible |
| `data/schedule.json` | Array of channel objects with `programs`. | scheduler | partially compatible |
| `data/reserves.json` | Array of program objects. Program and nested channel unknown fields are preserved across Go read/write cycles where the object is unmarshaled as `legacy.Program`; known fields are emitted in a stable legacy-compatible order. | scheduler/CLI/API/operator | partially compatible |
| `data/recording.json` | Array of recording program objects; `abort:true` requests stop. Go operator now polls this file while recording and closes the active stream when abort is set. CLI stop also updates matching auto reserves to skip. Program and nested channel unknown fields are preserved across Go read/write cycles where practical; known fields are emitted in a stable legacy-compatible order. | operator/CLI/API | partially compatible |
| `data/recorded.json` | Array of recorded program objects with `recorded` path. Program and nested channel unknown fields are preserved across Go read/write cycles where the object is unmarshaled as `legacy.Program`; known fields are emitted in a stable legacy-compatible order. | operator/cleanup/API | partially compatible |
| `data/scheduler.pid` | Scheduler process id text written while `update` or WUI scheduler force runs and removed on exit. | scheduler/WUI status | implemented |
| `data/operator.pid` | Operator process id text written by `service operator execute` and removed on exit. | operator/WUI status | implemented |
| `log/scheduler` | Scheduler log stream with `RUNNING SCHEDULER.`, `GETTING EPG from Mirakurun.`, `Mirakurun -> ...` fetch counts, `Mirakurun -> Error:` plus error details, `RESERVE:`, `DUPLICATE:`, `!CONFLICT:`, `SKIP:`, `OVERRIDEBYRULE:`, `WRITE:`, `TUNERS:`, duplicate ID `**WARNING**`, and `MATCHES`/`DUPLICATES`/`CONFLICTS`/`SKIPS`/`RESERVES` result counters. | scheduler/WUI | partially compatible |
| `log/operator` | Operator log stream with legacy-style `MKDIR:`, `PREPARE:`, `RECORD:`, `STREAM:`, `WRITE:`, `SPAWN:`, and `FIN:` lines plus Go `START:` compatibility lines. | operator/WUI | partially compatible |
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

API implementation status: partially compatible. The Go WUI currently implements JSON reads for status/config/rules/schedule/schedule channel/schedule programs/schedule broadcasting/reserves/recording/recorded/program lookup, with `/api/program/:id.json` matching the legacy schedule-only lookup, legacy pretty JSON for status, storage, scheduler JSON results, rules reads, schedule subresource reads, single program/reserve/recording/recorded item reads, and recorded file stat reads, legacy compact JSON for reserves/recording/recorded list reads and empty mutation responses, legacy API method allow lists from `api/resource-*.json` including `HEAD` only for `/api/schedule.json`, legacy bad-path handling where known API resources return 400 while unknown resources return 404, the old `/api/index.html` 400 special case, legacy extension stripping only for lowercase alphanumeric suffixes, old fixed text/plain error bodies such as `400 Bad Request\n`, `401 Unauthorized\n`, `404 Not Found\n`, `405 Method Not Allowed\n`, `409 Conflict\n`, `410 Gone\n`, `415 Unsupported Media Type\n`, `416 Requested Range Not Satisfiable\n`, `500 Internal Server Error\n`, and `503 Service Unavailable\n` with empty bodies for HEAD, legacy `/api/schedule.json` `Last-Modified`/`If-Modified-Since`/deflate behavior, legacy resource type checks with 415 for unsupported or missing explicit extensions including `/api/recorded/:id/file` requiring `.json` or `.m2ts`, query `method`/`_method` HTTP method override with legacy removal of those control parameters before API dispatch and overridden method names in access logs while preserving the original request URL, config PUT with the `json` query parameter, scheduler status/log/update/force, storage usage with legacy recorded-file allocated block accounting where available, log reads, live log stream tailing, rules create/update/delete/enable/disable from JSON bodies or legacy query parameters with compact mutation response JSON, program PUT manual reservation, reserve skip/unskip/delete with manual-only delete semantics and compact `{}` mutation responses, recording abort marking with auto-reserve skip, recorded item delete with timestamped backup before destructive writes, recorded file stat/stream/delete, recorded/recording preview image generation through `ffmpeg` with legacy `png`/`jpg`/`txt` response handling and error order, legacy `status.feature` flags without Go-only fields, legacy `status.operator` process status from the operator PID file, and legacy `status.wui` values for WUI watch controls without a scheduler field, recorded/recording watch XSPF, recorded watch `mode=download` `Content-Disposition`, recorded watch legacy `ffprobe -show_format` preflight before XSPF/m2ts/mp4 responses with 500 on probe or JSON parse failure, m2ts direct and transcoded streaming including legacy `ss` offset calculation, ffprobe-derived total-size calculation, and legacy `Content-Range`/`Content-Length` handling with client range values mapped back to source-file byte ranges, m2ts `ffmpeg` streaming when transcode query parameters are present, and basic fragmented mp4 `ffmpeg` streaming with common legacy query parameters (`ss` with default/minimum `2`, explicit-`ss` ffprobe duration rejection with 416, `s`, `t`, `r`, `ar`, `b:v`, `b:a`, `c:v`, `c:a`) plus recorded-watch-only legacy bitrate side effects (`b:v` adds default `b:a=96k` when audio bitrate is unset/copy, and both video/audio bitrates add matching `bufsize` arguments), plus legacy `tuner.isScrambling` 409 rejection order, channel logo, channel watch XSPF, channel watch m2ts proxy, channel watch mp4 through Mirakurun-to-ffmpeg proxying with live bitrate arguments kept separate from recorded-watch `bufsize` behavior, recorded cleanup via PUT with timestamped backup before destructive writes, and recorded/reserve/recording item reads.

## WUI / Static Assets

Type validation note: preview, watch, channel logo/watch, and recorded file resources now reject unsupported or missing explicit API extensions with legacy `415 Unsupported Media Type\n` before state lookup.

Scheduler status note: `/api/scheduler.json` preserves unresolved `RESERVE:`/`CONFLICT:` log entries as JSON `null`, matching the old WUI script when an ID is no longer present in `schedule.json`.

Scheduler log parsing note: `RESERVE:`/`CONFLICT:` IDs are extracted with the legacy lowercase alphanumeric/hyphen pattern, so trailing punctuation is ignored like the old JavaScript regular expression.

Text API note: scheduler/log `.txt` responses use the legacy `Content-Type: text/plain` without an added charset.

Recorded file note: `/api/recorded/:id/file.m2ts` ignores request `Range` headers and returns the full file with legacy `Content-Length`/`Content-Disposition`, matching `script-recorded-program-file.vm.js`.

XSPF note: recorded watch titles use the old script's replacement order (`<`, `>`, then `&`, then `"`), and channel watch titles are emitted unescaped like `script-channel-watch.vm.js`; XSPF locations only escape `&`.

The old WUI serves `web/` directly with static files, range support, cache headers for icons/images, fixed extension-based content types, Host-header validation, the Socket.IO client script, and API dispatch under `/api/`. The Go implementation serves static files from `web/` when present and can fall back to the sibling legacy `web/` directory during development. The repository now includes a dependency-free native Strata PVR dashboard at `web/index.html`, `web/app.js`, and `web/styles.css`; it reads status, reserves, recording, recorded, schedule, rules, storage, logs, and config summaries from the Go API and exposes basic actions for schedule reservation, reserve skip/unskip/manual removal, active recording stop/watch/playlist/preview, recorded watch/download/preview/delete/custom playback, selected-channel watch/playlist controls, program-dialog channel MP4 playback, direct channel-logo display in the schedule header, persisted hidden-channel filters, scheduler force-run, common and detailed config field editing plus raw config JSON edit/save through the legacy PUT endpoint, and rule enable/disable/delete/add-from-JSON. The native config form covers common fields plus WUI auth/country/service-order/proxy/mDNS settings, VAAPI, priorities, TLS paths/client-auth flags, low-space notify/command settings, and scheduler/EPG/conflict/recorded hook commands; secret nested structures such as `operTweeterAuth` remain raw-JSON-only. The native rule form covers common fields and can merge an extra JSON object for less common rule extensions while keeping the full JSON editor available for exact edits, and program detail dialogs can prefill the rule form from the selected schedule program. Static `.ico` and `.png` assets now preserve the legacy `Cache-Control: private, max-age=86400` behavior while other static assets keep `no-cache`; legacy content types for html/js/css/icons/images/video/json/xspf are set explicitly; common legacy security headers are preserved and the `Server` header now identifies `Strata PVR`; static `Last-Modified`/`If-Modified-Since` conditional requests return 304 like the old WUI; missing static files and out-of-range static Range requests return the old fixed text/plain 404/416 bodies; `/socket.io/socket.io.js` returns a lightweight no-Node compatibility client that fires connect/status/notify callbacks, polls status every 5 seconds plus data notifications every 15 seconds, and clears polling timers on `disconnect()` so existing WUI assets can load and refresh API data. Full Socket.IO realtime push remains incomplete; requests without `Host` return 400. Current status: partially compatible; Node-based frontend builds are not required.

Frontend rewrite task: partially compatible. The native dashboard, core reservation/recording/recorded actions, recorded M2TS/direct-MP4/720p-MP4/low-bitrate-MP4/XSPF/download/preview actions, recorded custom start/length/quality playback controls, active-recording M2TS/MP4/XSPF/preview actions, selected-channel M2TS/MP4/XSPF actions, program-dialog channel MP4 playback and rule-form prefill, direct schedule channel-logo display, scheduler force-run, storage summary, settings summary, config JSON reload/save with validation and confirmation, a native config form for Mirakurun URL, recorded directory/format, WUI host/ports/open listener/auth/country/proxy/mDNS/TLS fields, normalization, VAAPI, priority, low-space policy/notify/command, service lists, and hook commands, JSON-based rule actions including in-place JSON updates, common title/description/type/SID/category/channel/flag/duration/hour/recorded-format rule creation, extra JSON rule-field merging, log panels, and schedule navigation by type/channel/day/genre/persisted-hidden-channel/time range/item count are implemented. Remaining frontend gaps are full legacy workflow parity, realtime Socket.IO push, field-specific forms for secret or nested rare settings such as `operTweeterAuth`, and edge-case legacy UI affordances. The legacy-compatible `/api/config.json` PUT endpoint remains available for old WUI/API clients and is used by the native config editor and config form.

## Mirakurun Endpoints Used

The JS Mirakurun client calls:

- services list: used by scheduler.
- programs list: used by scheduler.
- tuners list: used by scheduler.
- program stream by Mirakurun program ID, decoded=true: used by operator recording.
- service/channel stream: used by WUI watch routes.
- service logo: used by channel logo route.

Current Go client status: partially compatible for HTTP, `http+unix`, and legacy `http://unix:` URL setup plus services/programs/tuners, program stream, service stream, service logo requests, Strata PVR User-Agent values for scheduler/operator/WUI requests, and `X-Mirakurun-Priority`. The User-Agent product token intentionally uses the new project name instead of the legacy product name.

Mirakurun scheduler fixtures now live under `testdata/mirakurun/`:

- `services.json`
- `programs.json`
- `tuners.json`

They are used by scheduler tests to exercise schedule import and reservation generation without requiring a live Mirakurun instance.

## Side Effects

- Wrapper creates `config.json` and `rules.json` from samples during `service ... execute` if missing.
- Wrapper ensures `log/` and `data/`.
- Scheduler writes `data/schedule.json`, `data/reserves.json`, and maintains `data/scheduler.pid` while running.
- Scheduler logs Mirakurun fetch counts, reserve/duplicate/conflict/skip/rule-override/write/tuner-count/duplicate-ID lines, and the Node-style result counters, including legacy `DUPLICATE:`, `!CONFLICT:`, and `OVERRIDEBYRULE:` lines and `dateformat`-style `isoDateTime` timestamps without a timezone colon.
- Scheduler runs `epgStartCommand`, `epgEndCommand`, `schedulerStartCommand`, `conflictCommand`, and `schedulerEndCommand`, passing the same path/counter/program arguments as the Node scheduler. `epgStartCommand` and `schedulerStartCommand` are synchronous; `epgEndCommand`, `conflictCommand`, and `schedulerEndCommand` are launched asynchronously.
- Operator clears `data/recording.json` on start.
- Operator creates `recordedDir` and nested recorded directories.
- Operator writes `data/recording.json`, `data/recorded.json`, and may remove manual reserves.
- Operator writes recorded files directly to final path with append mode.
- Go operator currently starts due non-skip reserves 15 seconds before start, including conflict reserves at `conflictedPriority`, writes `data/recording.json`, updates the active recording entry with legacy runtime fields (`recorded`, `pid:-1`, `priority`, `tuner`, and `command`) after the Mirakurun stream is opened, records the Mirakurun decoded program stream, merges the completed item into `data/recorded.json` with the legacy same-ID replacement/old-ID suffix behavior, and removes the completed reserve only when it is `isManualReserved`, matching the old operator.
- Go operator writes to a temporary `.recording-*` file and renames it after a successful copy. This is an intentional safety improvement and is not byte-for-byte identical to the old direct final-path write behavior.
- Go operator startup clears `data/recording.json` to `[]`, creates missing `recordedDir` with legacy `MKDIR:` logging, polls `abort:true` during an active stream, and runs `recordedCommand` with recorded file path plus program JSON after state writes, logging the legacy `SPAWN:` line after process start. Low-storage command plus `remove`/`stop` actions, sendmail notification, and notification throttling are partially implemented; `remove` creates a timestamped `recorded.json` backup before rewriting the list. Operator logs now include the main legacy `PREPARE`/`RECORD`/`STREAM`/`WRITE`/`SPAWN`/`FIN` recording lines, including completion writes for `recorded.json` and `recording.json`. The Go entrypoint now cancels runtime services on `SIGINT`/`SIGTERM` and Unix `SIGQUIT`, but exact in-flight recording cleanup on external signals remains incomplete.
- CLI and WUI/API cleanup remove missing file entries from `data/recorded.json` and create a timestamped backup before destructive writes.
- WUI/API may rewrite config, rules, reserves, recording, recorded. Config PUT validates the supplied JSON but stores the raw query value to preserve the Node API shape.
- Go WUI recorded file stat preserves the legacy JSON field names, including `ulink`; Unix builds fill device/inode/uid/gid/block fields from `stat(2)` where available, while fallback platforms may return zero for unavailable fields.
- Go WUI `log/:name/stream.txt` writes the legacy padding, the last 100 log lines, and follows appended log data until the request is closed.
- Go WUI scheduler JSON parses `RESERVE:` and legacy `!CONFLICT:`/`CONFLICT:` lines from `log/scheduler`; exact old shell `tac/sed` behavior is approximated in Go.
- Go WUI status includes legacy operator PID values when the operator PID file is present and checks whether the referenced process is alive before setting `alive:true`; it intentionally does not expose a scheduler field because the old `data.status` object did not include one.
- Go WUI recorded/recording watch supports XSPF and m2ts serving. Recorded watch performs the legacy `ffprobe -show_format` preflight before responding, including XSPF. Recording m2ts watch streams the last 61440 bytes and follows appended file data until the request is closed.
- Old wrapper installer/updater run git, wget, npm, and ffmpeg installation steps. Go runtime intentionally does not require Node/npm; Go updater is a safe no-op guidance command.

## Compatibility Status Matrix

| Area | Status |
| --- | --- |
| Audit | partially compatible |
| Strata PVR module skeleton | implemented |
| Config loading | partially compatible |
| Atomic JSON state | implemented |
| CLI command acceptance | partially compatible |
| Rule engine | partially compatible |
| Recorded filename format | partially compatible; legacy tuner, episode, unknown-token `undefined` replacement, named dateformat masks, `S`/`o`/`l`/`L` date tokens, and `UTC:` prefix are implemented and covered by Go tests, but unusual JavaScript `dateformat` parsing edge cases still need oracle tests. |
| Mirakurun client | partially compatible |
| Scheduler | partially compatible |
| Operator/recorder | partially compatible; startup recording-state cleanup, missing `recordedDir` creation, active `abort:true` polling, ctx/signal cancellation that closes active streams and finalizes recording/recorded state, `recordedCommand` execution, `data/operator.pid` lifecycle, process context cancellation on `SIGINT`/`SIGTERM`/Unix `SIGQUIT`, and low-storage `remove`/`stop`/sendmail core actions with throttling implemented, but exact logs and every in-flight signal edge case remain incomplete. |
| WUI/API | partially compatible |
| Native Strata PVR frontend rewrite | partially compatible; dependency-free dashboard, core reservation/recording/recorded actions, recorded playback/download/transcode-preset/custom playback actions, JSON-based create/update, expanded common form rule actions, extra JSON rule-field merging, log panels, editable common/detailed config form plus raw JSON editor, and basic schedule navigation added under `web/`, but full legacy WUI workflow coverage is incomplete. |
| Installer/updater | intentionally changed; commands are accepted and provide Go-runtime guidance, but Node-era dependency installation, git automation, and service mutation are not performed. |
| Logging | partially compatible |
| Compat doctor/check/diff/backup/wrapper | implemented; validates required JSON state files, included sample files, and expected object/array shapes, `data/`, writable `log/`, writable `recordedDir`, native Strata PVR or legacy WUI static entry files, available disk space lookup, `ffmpeg`/`ffprobe` command availability, Mirakurun services/programs/tuners reachability, Node.js runtime non-requirement, warns about intentionally omitted personal-use-overkill integrations, prints non-secret config summaries, state-count summaries, conservative next steps through backup/update/reserve review/manual WUI and operator execution, active-recording migration warnings, and local wrapper-target binary warnings from `compat doctor`, reports dry-run JSON rewrite differences for compatible state files, can back up current JSON state files under `backup/strata-pvr-*`, and can print a safe shell wrapper for manual review/install. |
| Tests | partially compatible; Go unit/integration tests cover config parsing, rule matching, recorded filename formatting, JSON state helpers, CLI behavior, scheduler decisions, operator state transitions, mock Mirakurun client behavior, WUI/API routes, static asset serving, and scheduler import from `testdata/mirakurun` fixtures. Optional JavaScript oracle tests remain future work and are not required for normal `go test ./...`. |
