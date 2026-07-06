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
  channel logo/watch proxying, and the first compatible JSON API endpoints.
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
