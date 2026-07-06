# Chinachu-Go

This repository is a Go compatibility implementation targeting Chinachu gamma.

The implementation is intentionally being added beside the original JavaScript
codebase referenced from `../Chinachu`; it does not overwrite the original
`./chinachu` wrapper automatically.

## Current State

Phase 1/2 scaffolding is present:

- `MIGRATION_COMPATIBILITY.md` records the audited CLI, config, rule, data, API,
  WUI, and Mirakurun compatibility surface.
- `cmd/chinachu-go` is the binary entrypoint.
- Config loading preserves unknown fields.
- JSON state helpers write through a temp file and rename.
- Rule matching and recorded filename formatting are partially implemented.
- A Mirakurun client supports HTTP and `http+unix` setup.
- `chinachu-go update` performs the first scheduler pass against Mirakurun and
  writes legacy JSON state.
- `chinachu-go service operator execute` runs the first Go operator loop, starts
  due reservations, records Mirakurun program streams, and updates legacy JSON
  state.
- `chinachu-go service wui execute` starts a Go WUI/API server with Basic auth,
  static asset serving, rule/reservation mutations, recorded file access,
  recorded/recording TS watch routes, channel logo/watch proxying,
  scheduler/storage/log endpoints, and the first compatible JSON API endpoints.
- CLI command names are accepted, with reservation state and rule operations
  partially implemented.

## Build

Install Go 1.22 or newer, then run:

```sh
go test ./...
go build -o chinachu-go ./cmd/chinachu-go
```

The final runtime must not require Node.js. Optional JS-vs-Go compatibility
oracle tests may be added later, but ordinary Go tests must pass without Node.

## Usage

Run from the Chinachu working directory that contains `config.json`, `rules.json`,
and `data/`. The Go runtime uses the existing Mirakurun backend configured by
`mirakurunPath`; it does not replace tuner, recpt1, B-CAS, PT3, or Mirakurun
configuration.

```sh
./chinachu-go update
./chinachu-go reserves
./chinachu-go service operator execute
./chinachu-go service wui execute
```

For init script generation, keep the original JavaScript wrapper in place and
write the Go output to a separate file for review:

```sh
./chinachu-go service operator initscript > chinachu-operator-go
./chinachu-go service wui initscript > chinachu-wui-go
```

Compatibility and environment checks are available with:

```sh
./chinachu-go compat check
./chinachu-go compat doctor
```
