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

The Go CLI also accepts the legacy internal `app-cli.js` shape `-mode <command>`,
`--mode <command>`, and `--mode=<command>` before dispatching to the same command
handlers.
For reservation/recording mutation commands, program IDs are accepted as either
positional arguments or legacy `-id`/`--id` options, with boolean flags allowed
before or after the ID.

| Command | Current Go status | Notes |
| --- | --- | --- |
| `installer` | partially compatible | Accepted. Node/npm installation is intentionally not performed. |
| `updater` | intentionally changed | Accepted, but automatic git/service/installer operations are not performed. This avoids Node/npm runtime assumptions and destructive service changes; users should update repository/binary explicitly. |
| `service <operator|wui> <initscript|execute>` | partially compatible | Initscript generation uses the Go binary and includes legacy LSB headers, start/stop/restart/status handling, `/var/run/chinachu-*.pid`, `su $USER` launch, and process-group `SIGQUIT` stop behavior. `execute` creates missing `config.json`/`rules.json` from samples and ensures `log/` and `data/`; `operator execute` runs the Go operator loop; `wui execute` starts the Go WUI/API server. |
| `update [-s|--simulation]` | partially compatible | Fetches Mirakurun services/programs/tuners, writes schedule/reserves, applies rules/manual/skip/conflict logic, maintains `data/scheduler.pid`, runs scheduler/EPG/conflict hooks, and logs legacy Mirakurun fetch/error/reserve/conflict/skip/write/tuner/duplicate/result lines. |
| `search` | partially compatible | Filters `data/schedule.json` with rule-style options plus `-id`, `-now`, `-today`, `-tomorrow`, `-simple`, `-detail`, and `-n/--num`. Output is tabular but not yet byte-for-byte `easy-table`; `config.normalizationForm` matching remains incomplete. |
| `reserve <pgid> [-s|--simulation] [--1seg]` | partially compatible | Reads schedule and writes reserves, supports simulation output, the `1seg` flag, and legacy `-id/--id` program ID options; exact JSON field ordering/output spacing still incomplete. |
| `unreserve <pgid> [-s|--simulation]` | partially compatible | Data side effect, simulation output, and legacy `-id/--id` program ID options implemented; exact JSON field ordering/output spacing still incomplete. |
| `skip <pgid> [-s|--simulation]` | partially compatible | Data side effect, simulation output, target JSON output, and legacy `-id/--id` program ID options implemented; exact JSON field ordering/output spacing still incomplete. |
| `unskip <pgid> [-s|--simulation]` | partially compatible | Data side effect, simulation output, legacy `skip:` output label, and legacy `-id/--id` program ID options implemented; exact JSON field ordering/output spacing still incomplete. |
| `stop <pgid> [-s|--simulation]` | partially compatible | Marks recording entry with `abort:true`, sets the matching auto reserve to `isSkip:true`, supports simulation/JSON output like the Node CLI, and accepts legacy `-id/--id` program ID options. |
| `rule` | partially compatible | Adds/updates/removes rules with core matching fields. Supports Node-style deletion markers such as `-title null` and `-start -1`; table output still needs more work. |
| `enrule <rule#>` | partially compatible | Alias for `rule -n <rule#> --enable`. |
| `disrule <rule#>` | partially compatible | Alias for `rule -n <rule#> --disable`. |
| `rmrule <rule#>` | partially compatible | Alias for `rule -n <rule#> --remove`. |
| `rules` | partially compatible | Prints a legacy-style rule table with `-n`, `-detail`, and transposed single-row output; exact `easy-table` spacing still incomplete. |
| `reserves` | partially compatible | Prints a legacy-style program table with filtering/sort support; exact `easy-table` spacing still incomplete. |
| `recording` | partially compatible | Prints a legacy-style program table with filtering/sort support; exact `easy-table` spacing still incomplete. |
| `recorded` | partially compatible | Prints a legacy-style program table with filtering/sort support; exact `easy-table` spacing still incomplete. |
| `cleanup [-s|--simulation]` | partially compatible | Prints a legacy-style action table and removes missing recorded entries unless simulation is set. Before destructive writes, Go creates `data/recorded.json.bak-YYYYMMDDHHMMSS`. |
| `compat check`, `compat doctor`, `compat backup` | implemented | New Go-only safety checks for required JSON state files, `data/`, writable `recordedDir`, available disk space lookup, Mirakurun services/programs/tuners reachability, Node.js runtime non-requirement, plus timestamped JSON state backups under `backup/`; does not alter legacy command behavior. |
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
| `vaapiEnabled`, `vaapiDevice` | WUI transcode/preview support. | partially compatible; fields are parsed from existing config, but preview/transcode use is not implemented yet. |
| `excludeServices` | Mirakurun service IDs excluded from schedule import. | implemented |
| `serviceOrder` | Service IDs moved to the front in schedule order. | implemented |
| `wuiUsers` | Basic auth users as `user:pass`. | implemented for the authenticated listener, including the legacy `WWW-Authenticate: Basic realm="Authentication."` challenge. |
| `wuiAllowCountries` | GeoIP country allow list. | not started |
| `wuiPort`, `wuiHost` | Deprecated authenticated listener. | partially compatible; starts a separate authenticated HTTP/HTTPS server when `wuiPort` is set. |
| `wuiTlsKeyPath`, `wuiTlsCertPath`, `wuiTlsPassphrase`, `wuiTlsRequestCert`, `wuiTlsRejectUnauthorized`, `wuiTlsCaPath` | TLS listener settings. | partially compatible; cert/key listener, encrypted PEM key passphrase handling, client certificate request/verification, and CA pool loading are implemented. PFX-style key material is not implemented. |
| `wuiOpenServer`, `wuiOpenHost`, `wuiOpenPort` | Unauthenticated LAN listener. | partially compatible; starts a separate HTTP server without Basic auth and selects a private IPv4 when `wuiOpenHost` is unset. mDNS remains incomplete. |
| `wuiXFF` | Trust first `X-Forwarded-For` IP. | partially compatible; access logging uses the first forwarded address and normalizes IPv4-mapped IPv6. GeoIP country filtering is still not implemented. |
| `wuiMdnsAdvertisement` | mDNS advertisement. | not started |
| `normalizationForm` | Unicode normalization form used by title/detail matching. | partially compatible |
| `recordedFormat` | Filename template. | partially compatible; supports legacy date masks/tokens including `UTC:` prefix plus id/type/channel/channel-id/channel-sid/channel-name/tuner/title/fulltitle/subtitle/episode/episode:N/category tokens and filename character stripping. Remaining risk is unusual JavaScript `dateformat` masks not covered by Go tests. |
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

Rule matching status: partially compatible. Type/channel/category/hour/duration/title/detail/flag checks are implemented in Go. Duration matching preserves the legacy behavior of applying the rule only when both `min` and `max` are present in JSON. JavaScript RegExp semantics are approximated with Go regexp and need oracle tests for edge cases. CLI rule add/update/enable/disable/remove is implemented for core fields, including Node-style `null`/`-1` deletion markers.

## Data Files And Schemas

| File | Schema | Writer(s) | Status |
| --- | --- | --- | --- |
| `config.json` | JSON object, unknown fields allowed. | API config PUT writes the supplied `json` query value after validation. | partially compatible |
| `rules.json` | Array of rule objects. Pretty printed by rule/API writes. | CLI/API | partially compatible |
| `data/schedule.json` | Array of channel objects with `programs`. | scheduler | partially compatible |
| `data/reserves.json` | Array of program objects. Program and nested channel unknown fields are preserved across Go read/write cycles where the object is unmarshaled as `chinachu.Program`. | scheduler/CLI/API/operator | partially compatible |
| `data/recording.json` | Array of recording program objects; `abort:true` requests stop. Go operator now polls this file while recording and closes the active stream when abort is set. CLI stop also updates matching auto reserves to skip. Program and nested channel unknown fields are preserved across Go read/write cycles where practical. | operator/CLI/API | partially compatible |
| `data/recorded.json` | Array of recorded program objects with `recorded` path. Program and nested channel unknown fields are preserved across Go read/write cycles where the object is unmarshaled as `chinachu.Program`. | operator/cleanup/API | partially compatible |
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

API implementation status: partially compatible. The Go WUI currently implements JSON reads for status/config/rules/schedule/schedule channel/schedule programs/schedule broadcasting/reserves/recording/recorded/program lookup, legacy resource type checks with 415 for unsupported explicit extensions, query `method`/`_method` HTTP method override with legacy removal of those control parameters before API dispatch, config PUT with the `json` query parameter, scheduler status/log/update/force, storage usage with legacy recorded-file allocated block accounting where available, log reads, live log stream tailing, rules create/update/delete/enable/disable from JSON bodies or legacy query parameters, program PUT manual reservation, reserve skip/unskip/delete with manual-only delete semantics, recording abort marking with auto-reserve skip, recorded item delete with timestamped backup before destructive writes, recorded file stat/stream/delete, recorded/recording preview image generation through `ffmpeg` with legacy `png`/`jpg`/`txt` response handling and error order, `status.feature.previewer:true`, `status.feature.streamer:true` for WUI watch controls, recorded/recording watch XSPF, m2ts, and basic fragmented mp4 `ffmpeg` streaming with common legacy query parameters (`s`, `t`, `r`, `ar`, `b:v`, `b:a`, `c:v`, `c:a`) plus legacy `tuner.isScrambling` 409 rejection order, channel logo, channel watch XSPF, channel watch m2ts proxy, channel watch mp4 through Mirakurun-to-ffmpeg proxying, recorded cleanup via PUT with timestamped backup before destructive writes, and recorded/reserve/recording item reads. Compression routes, exact mp4 range/ffprobe size calculation, and exact status fields remain incomplete.

## WUI / Static Assets

The old WUI serves `web/` directly with static files, range support, cache headers for icons/images, fixed extension-based content types, Host-header validation, and API dispatch under `/api/`. The Go implementation serves static files from `web/` when present and can fall back to `../Chinachu/web` during development. Static `.ico` and `.png` assets now preserve the legacy `Cache-Control: private, max-age=86400` behavior while other static assets keep `no-cache`; legacy content types for html/js/css/icons/images/video/json/xspf are set explicitly; requests without `Host` return 400. Current status: partially compatible; Node-based frontend builds are not required.

## Mirakurun Endpoints Used

The JS Mirakurun client calls:

- services list: used by scheduler.
- programs list: used by scheduler.
- tuners list: used by scheduler.
- program stream by Mirakurun program ID, decoded=true: used by operator recording.
- service/channel stream: used by WUI watch routes.
- service logo: used by channel logo route.

Current Go client status: partially compatible for HTTP, `http+unix`, and legacy `http://unix:` URL setup plus services/programs/tuners, program stream, service stream, service logo requests, and `X-Mirakurun-Priority`.

## Side Effects

- Wrapper creates `config.json` and `rules.json` from samples during `service ... execute` if missing.
- Wrapper ensures `log/` and `data/`.
- Scheduler writes `data/schedule.json`, `data/reserves.json`, and maintains `data/scheduler.pid` while running.
- Scheduler logs Mirakurun fetch counts, reserve/duplicate/conflict/skip/rule-override/write/tuner-count/duplicate-ID lines, and the Node-style result counters, including legacy `DUPLICATE:`, `!CONFLICT:`, and `OVERRIDEBYRULE:` lines and `dateformat`-style `isoDateTime` timestamps without a timezone colon.
- Scheduler runs `epgStartCommand`, `epgEndCommand`, `schedulerStartCommand`, `conflictCommand`, and `schedulerEndCommand`, passing the same path/counter/program arguments as the Node scheduler. Go waits for these hooks to finish.
- Operator clears `data/recording.json` on start.
- Operator creates `recordedDir` and nested recorded directories.
- Operator writes `data/recording.json`, `data/recorded.json`, and may remove manual reserves.
- Operator writes recorded files directly to final path with append mode.
- Go operator currently starts due non-skip/non-conflict reserves 15 seconds before start, writes `data/recording.json`, updates the active recording entry with legacy runtime fields (`recorded`, `pid:-1`, `priority`, `tuner`, and `command`) after the Mirakurun stream is opened, records the Mirakurun decoded program stream, merges the completed item into `data/recorded.json` with the legacy same-ID replacement/old-ID suffix behavior, and removes the completed reserve only when it is `isManualReserved`, matching the old operator.
- Go operator writes to a temporary `.recording-*` file and renames it after a successful copy. This is an intentional safety improvement and is not byte-for-byte identical to the old direct final-path write behavior.
- Go operator startup clears `data/recording.json` to `[]`, creates missing `recordedDir` with legacy `MKDIR:` logging, polls `abort:true` during an active stream, and runs `recordedCommand` with recorded file path plus program JSON after state writes, logging the legacy `SPAWN:` line after process start. Low-storage command plus `remove`/`stop` actions, sendmail notification, and notification throttling are partially implemented; `remove` creates a timestamped `recorded.json` backup before rewriting the list. Operator logs now include the main legacy `PREPARE`/`RECORD`/`STREAM`/`WRITE`/`SPAWN`/`FIN` recording lines, including completion writes for `recorded.json` and `recording.json`, but every signal side effect remains incomplete.
- CLI and WUI/API cleanup remove missing file entries from `data/recorded.json` and create a timestamped backup before destructive writes.
- WUI/API may rewrite config, rules, reserves, recording, recorded. Config PUT validates the supplied JSON but stores the raw query value to preserve the Node API shape.
- Go WUI recorded file stat preserves the legacy JSON field names, including `ulink`; Unix builds fill device/inode/uid/gid/block fields from `stat(2)` where available, while fallback platforms may return zero for unavailable fields.
- Go WUI `log/:name/stream.txt` writes the legacy padding, the last 100 log lines, and follows appended log data until the request is closed.
- Go WUI scheduler JSON parses `RESERVE:` and legacy `!CONFLICT:`/`CONFLICT:` lines from `log/scheduler`; exact old shell `tac/sed` behavior is approximated in Go.
- Go WUI status includes operator/scheduler PID values when PID files are present and checks whether the referenced process is alive before setting `alive:true`.
- Go WUI recorded/recording watch supports XSPF and m2ts serving. Recording m2ts watch streams the last 61440 bytes and follows appended file data until the request is closed.
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
| Recorded filename format | partially compatible; legacy tuner, episode, common dateformat masks, and `UTC:` prefix are implemented, but unusual JavaScript `dateformat` masks still need oracle tests. |
| Mirakurun client | partially compatible |
| Scheduler | partially compatible |
| Operator/recorder | partially compatible; startup recording-state cleanup, missing `recordedDir` creation, active `abort:true` polling, `recordedCommand` execution, `data/operator.pid` lifecycle, and low-storage `remove`/`stop`/sendmail core actions with throttling implemented, but exact logs and signal side effects remain incomplete. |
| WUI/API | partially compatible |
| Installer/updater | partially compatible |
| Logging | partially compatible |
| Compat doctor/check/backup | implemented; validates required JSON state files, `data/`, writable `recordedDir`, available disk space lookup, Mirakurun services/programs/tuners reachability, Node.js runtime non-requirement, and can back up current JSON state files under `backup/`. |
| Tests | partially compatible |
