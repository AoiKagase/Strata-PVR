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
- Rule matching and recorded filename formatting are partially implemented.
- A Mirakurun client supports HTTP and `http+unix` setup.
- `strata-pvr update` performs the first scheduler pass against Mirakurun and
  writes legacy JSON state.
- `strata-pvr service operator execute` runs the Go operator loop, starts due
  reservations, records Mirakurun program streams, and updates legacy JSON
  state.
- `strata-pvr service wui execute` starts a Go WUI/API server with Basic auth,
  static asset serving, rule/reservation mutations, recorded file access,
  recorded/recording TS watch routes, channel logo/watch proxying,
  scheduler/storage/log endpoints, and compatible JSON API endpoints.
- CLI command names are accepted, with reservation state and rule operations
  partially implemented.

## Build

Install Go 1.22 or newer, then run:

```sh
go test ./...
go build -o strata-pvr ./cmd/strata-pvr
```

The final runtime must not require Node.js. Optional JS-vs-Go compatibility
oracle tests may be added later, but ordinary Go tests must pass without Node.

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

To review a safe shell wrapper that forwards existing command arguments to the
Go binary without overwriting any file automatically:

```sh
./strata-pvr compat wrapper > strata-pvr-wrapper
```

## Frontend

The repository now includes an initial native Strata PVR frontend under `web/`.
It is a dependency-free HTML/CSS/JavaScript dashboard that reads the Go API for
status, reserves, recording, recorded, and schedule summaries. It also exposes
basic Go API actions for reserving schedule items, skipping/unskipping reserves,
removing manual reserves, stopping active recordings, and opening/downloading or
deleting recorded items. The legacy WUI asset fallback remains available during
compatibility work.

The frontend still needs rule editing, detailed schedule navigation, log views,
settings, and richer playback controls before it can replace every legacy WUI
workflow, but it does not require Node.js, npm, webpack, or any Node-based build
step.
