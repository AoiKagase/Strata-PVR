# Strata PVR

A Chinachu-compatible PVR for Mirakurun, written in Go.

This repository is a Go compatibility implementation targeting legacy gamma
behavior and file formats. It is intentionally being added beside the original
JavaScript codebase referenced from the sibling legacy repository; it does not
overwrite the original wrapper automatically.

## Current State

Compatibility scaffolding and major runtime pieces are present:

- `MIGRATION_COMPATIBILITY.md` records the audited CLI, config, rule, data, API,
  WUI, and Mirakurun compatibility surface.
- `cmd/strata-pvr` is the binary entrypoint.
- Config loading preserves unknown fields.
- JSON state helpers write through a temp file and rename.
- Rule matching covers the core legacy rule fields, including title, ignore
  title, description, type, channel, category, hour, duration, flags, and
  configured Unicode normalization.
- Recorded filename formatting covers the common legacy tokens, date masks,
  unknown-token `undefined` behavior, and UTC-prefix handling.
- A Mirakurun client supports HTTP, `http+unix`, and legacy `http://unix:`
  socket URL setup.
- `strata-pvr update` performs the first scheduler pass against Mirakurun and
  writes legacy JSON state.
- `strata-pvr service operator execute` runs the Go operator loop, starts due
  reservations, records Mirakurun program streams, and updates legacy JSON
  state.
- `strata-pvr service wui execute` starts a Go WUI/API server with Basic auth,
  static asset serving, rule/reservation mutations, recorded file access,
  recorded/recording TS watch routes, channel logo/watch proxying,
  scheduler/storage/log endpoints, and compatible JSON API endpoints.
- CLI command names are accepted, with reservation, skip/unskip, stop,
  cleanup, rule mutation, compat check/doctor/diff/backup/wrapper, and
  service execution paths implemented for the Go runtime.

## Build

Install Go 1.22 or newer, then run:

```sh
go test ./...
go build -o strata-pvr ./cmd/strata-pvr
```

On the local Windows development machine, Go is installed at the default path
`C:\Program Files\Go\bin\go.exe`. Use that binary directly if `go` is not on
`PATH`.

WUI MP4 playback is browser-oriented, but it is generated on demand by
`ffmpeg`. Install `ffmpeg` and make sure the WUI process can find it on `PATH`;
otherwise MP4 watch routes return `503 Service Unavailable` and `log/wui`
contains `exec: "ffmpeg": executable file not found in %PATH%`.

The final runtime must not require Node.js. Optional JS-vs-Go compatibility
oracle tests may be added later, but ordinary Go tests must pass without Node.
Mirakurun scheduler fixtures are kept under `testdata/mirakurun/` for Go tests
that should not depend on a live Mirakurun instance.

## Usage

Run from the existing PVR working directory that contains `config.json`,
`rules.json`, and `data/`. Strata PVR uses the existing Mirakurun backend
configured by `mirakurunPath`; it does not replace tuner, recpt1, B-CAS, PT3, or
Mirakurun configuration.

```sh
./strata-pvr update
./strata-pvr reserves
./strata-pvr service operator execute
./strata-pvr service wui execute
```

For init script generation, keep the original JavaScript wrapper in place and
write the Go output to a separate file for review:

```sh
./strata-pvr service operator initscript > strata-pvr-operator
./strata-pvr service wui initscript > strata-pvr-wui
```

Compatibility and environment checks are available with:

```sh
./strata-pvr compat check
./strata-pvr compat doctor
```

`compat doctor` includes the same checks plus a non-secret configuration
summary for Mirakurun, configured and resolved recording paths, WUI listeners,
and storage policy. The checks include the bundled `config.sample.json` and
`rules.sample.json`, so packaging mistakes are caught before first-run service
initialization. It also prints state-file counts for schedule channels, reserves,
active recordings, and recorded entries, warns when active recordings are
present, prints conservative next-step commands, and warns when the local
`strata-pvr` binary expected by generated wrappers and init scripts is not
present yet.

To review a safe shell wrapper that forwards existing command arguments to the
Go binary without overwriting any file automatically:

```sh
./strata-pvr compat wrapper > strata-pvr-wrapper
```

## Personal Deployment Checklist

Before replacing any existing command wrapper or service, verify the Go runtime
from the production PVR directory:

```sh
./strata-pvr compat backup
./strata-pvr compat doctor
./strata-pvr update -s
./strata-pvr reserves
./strata-pvr service wui execute
./strata-pvr service operator execute
```

If `config.json` or `rules.json` is missing, `service ... execute` copies the
included `config.sample.json` and `rules.sample.json` before starting. The Go
sample keeps GeoIP country filtering and mDNS advertisement disabled by default
because those Node-era integrations are intentionally omitted. Change the sample
`wuiUsers` credential before enabling or exposing the authenticated listener;
`compat check` warns if the sample credential is still configured. It also warns
when `wuiOpenServer` enables the unauthenticated listener; keep that listener on
a trusted network or disable it for authenticated-only access.

For a conservative first run, start the WUI and operator manually in separate
terminals and confirm that `log/wui`, `log/operator`, `data/reserves.json`,
`data/recording.json`, and `data/recorded.json` update as expected. Only after
that should generated init scripts or a compatibility wrapper be installed.

## Frontend

The repository now includes an initial native Strata PVR frontend under `web/`.
It is a dependency-free HTML/CSS/JavaScript dashboard that reads the Go API for
status, reserves, recording, recorded, and schedule summaries. It also exposes
basic Go API actions for reserving schedule items, skipping/unskipping reserves,
removing manual reserves, stopping active recordings, and opening/downloading or
deleting recorded items. It also lists auto-reservation rules, can enable,
disable, delete, add rules from JSON, and add common title/description/type/
SID/category/channel/flag/duration/hour/recorded-format rules from form fields.
The rule form also accepts an extra JSON object for less common fields, and
existing rules can be copied into a JSON editor and saved back in place.
Recorded items expose M2TS, browser-oriented MP4 transcode, 720p MP4,
low-bitrate MP4, custom start/length/quality playback, XSPF, download, and
delete actions, and active recordings expose live M2TS/MP4/XSPF watch actions.
MP4 actions require `ffmpeg` on the WUI process `PATH`. The legacy WUI
asset fallback remains available during compatibility work. Scheduler, operator,
and WUI logs are visible from the dashboard as tail-style text panels. A
settings panel shows non-secret runtime configuration such as Mirakurun URL,
recorded directory, WUI ports, storage policy, and normalization, and includes
common and detailed settings forms plus a validated raw JSON editor for
`config.json` updates through the legacy API.
The schedule panel can filter by channel, time range, and item count while
keeping the existing `/api/schedule.json` data path.

Native settings forms cover WUI auth/country/service-order/proxy/mDNS/TLS,
VAAPI, priorities, low-space notify/command settings, and hook commands. Use
the raw JSON editor or direct `config.json` edits for secret or nested rare
fields such as `operTweeterAuth`. The legacy-compatible `/api/config.json` PUT
endpoint remains available for old clients. The frontend does not require
Node.js, npm, webpack, or any Node-based build step.
