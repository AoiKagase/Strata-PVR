# Unimplemented Remaining Items

This list is derived from `MIGRATION_COMPATIBILITY.md` after re-auditing the
legacy source tree at `..\Chinachu`. It separates source-compatible runtime
gaps from intentional compatibility breaks, parity risks, and verification
backlog.

## Legacy Source Coverage

| Source area | Legacy files checked | Functional migration decision |
| --- | --- | --- |
| CLI wrapper and modes | `chinachu`, `app-cli.js` | No known must-implement command is missing. Remaining differences are output-format and JSON-order parity risks. |
| Scheduler | `app-scheduler.js`, `common/lib/chinachu-common.js` | Core schedule import, rule matching, reserve generation, hooks, pid files, and legacy log lines are implemented. Remaining differences are oracle-test risks. |
| Operator/recorder | `app-operator.js`, `common/lib/chinachu-common.js` | Core reserve execution, recording state, low-space behavior, abort handling, hooks, and finalization are implemented. Rare signal/log ordering still needs oracle-level validation. |
| WUI/API server | `app-wui.js`, `api/resource-*.json`, `api/script-*.vm.js` | API resources, static serving, auth/listeners, watch/preview routes, log streams, scheduler force, and mutating actions are covered. Full Socket.IO server push remains the only known functional runtime gap. |
| Legacy browser WUI | `web/index.html`, `web/chinachu.js`, `web/page/**/*.js` | Native Go WUI covers the main user workflows. Remaining differences are old frontend affordances and browser-specific behavior, not core migration blockers unless legacy UI byte-for-byte behavior becomes a target. |
| Config and samples | `config.sample.json`, `rules.sample.json` | Non-retired runtime fields are parsed and mostly surfaced in WUI forms. Some Node-era integrations are intentionally not implemented. |

## Functionally Required Remaining Items

| Priority | Area | Remaining item | Why it is still functionally required |
| --- | --- | --- | --- |
| P0 | WUI realtime | Full Socket.IO server push transport | The legacy WUI server used Socket.IO in `app-wui.js` and emitted `notify-rules`, `notify-reserves`, `notify-recording`, `notify-recorded`, and `notify-schedule` on state changes. The Go shim provides polling, same-origin wakeups, reconnect events, and compatibility client APIs, but it does not reproduce the Socket.IO transport/protocol or exact server-push event ordering. This matters for old WUI assets or third-party clients that depend on real Socket.IO behavior rather than eventual polling. |

## Compatibility Risks / Needs Oracle Tests

| Area | Remaining risk |
| --- | --- |
| CLI tables | Byte-for-byte `easy-table` truncation/alignment edge cases for `rules`, `reserves`, `recording`, `recorded`, and `search` output. |
| JSON state writes | Known fields are emitted in stable legacy-oriented order, but unknown-field insertion order and obscure spacing edge cases can still differ from Node `JSON.stringify` behavior. |
| Rule engine | JavaScript RegExp semantics are approximated with Go regexp; uncommon regex behavior needs oracle tests against `app-cli.js`/`chinachu-common.js`. |
| Recorded filename format | Unusual JavaScript `dateformat` parsing edge cases need oracle tests against the legacy `dateformat` module. |
| Operator signals | Runtime services cancel on `SIGINT`, `SIGTERM`, and Unix `SIGQUIT`; active streams are closed, recording/recorded state is finalized, and `recordedCommand` plus `FIN` logging are covered. Remaining risk is exact byte-for-byte cleanup/log ordering for rarer external-signal races. |
| Logs | Scheduler/operator/WUI logs cover major legacy lines, but exact shell/log formatting parity remains partial. |
| Mirakurun client | Product token intentionally uses `StrataPVR` instead of the legacy product name. |
| Tests | Optional JavaScript oracle tests remain future work and are not required for normal `go test ./...`. |

## Native WUI Parity Backlog

These are useful parity improvements, but the `..\Chinachu` audit does not make
them functionally mandatory for migration because the API and state-changing
workflows are already available.

| Area | Status | Notes |
| --- | --- | --- |
| Legacy list/filter affordances | mostly implemented | Main dashboard, schedule, recording, recorded, rule, and manual reserve flows are implemented; reservation, recorded, channel-program, and rule pages have title/detail/channel/condition search with category or state filtering where relevant, sort controls, clear actions, Esc-to-clear search shortcuts, and local restoration. Fine legacy affordances such as every old per-column filter/sort shortcut, named filter presets, compact page transition, and historical list interaction remain unverified. |
| Legacy dialogs and confirmations | partial | Native confirmation dialog covers destructive actions, config save, and rule add/save from both the JSON editor and form editor, with metadata where relevant, cancel/backdrop/Esc handling, danger styling, focus restoration, OK-button initial focus for Enter-key confirmation, and Tab/Shift+Tab focus wrapping. Exact old dialog text, browser-specific default-button edge cases, and validation timing are not byte-for-byte compatible. |
| Legacy keyboard/mouse shortcuts | mostly implemented | Primary mouse operations work. Native shortcuts cover number-key section navigation, `r` refresh, `/` current-view search focus, `Esc` search-filter clear and dialog close, `j`/`k`, arrow-key, `Home`/`End`, and `PageUp`/`PageDown` row movement, row Enter/Space detail open, and row double-click detail open. Exhaustive old context-menu affordances and browser-specific shortcut behavior are not yet mapped. |
| Legacy visual/state parity | partial | Hidden-channel persistence, schedule navigation, log panels, main actions, selected-program highlighting, per-view window scroll restoration, schedule-grid scroll retention, initial loading/failure placeholders, refresh button busy state, and schedule/on-air/detail reservation/recording state badges are implemented. Exact old empty/error wording and minor visual transitions remain partially covered. |
| Live/recording watch actions | mostly implemented | Live and recording MP4 conversion watch actions are exposed as playback-oriented labels, and schedule/on-air/detail rows switch to recording watch/stop actions when the same program is in `recording.json`. M2TS buttons are intentionally limited to recorded files to avoid infinite live download behavior. Legacy live M2TS endpoints remain for API compatibility. |
| Rare non-retired config controls | mostly implemented | Known non-retired runtime config fields in `internal/config.Config` have dedicated form controls and are visible in the settings summary. Future runtime config additions should get controls when they have clear operational value. |
| Unknown/custom config fields | intentionally raw JSON | Unknown fields are preserved through raw JSON editing rather than first-class controls, because the Go runtime cannot know deployment-specific extension semantics. |
| Retired Tweeter/Twitter config fields | intentionally raw JSON | Legacy Twitter posting fields are parsed/preserved and warned about, but dedicated WUI controls are intentionally omitted because the integration is retired/unavailable. |

## Intentional Non-Implementations

| Area | Item | Rationale |
| --- | --- | --- |
| Installer | Node/npm-era dependency installer | Go runtime is installed/built directly; Node/npm module installation is intentionally not performed. |
| Updater | Automatic git/service/installer operations | Avoids destructive service mutation and Node-era assumptions. |
| IRC bot | Experimental Node-era `ircbot` | Command is accepted with guidance; use WUI/API or an external bot. |
| Test command | Node-era `usr/bin/<app>` execution | Command is accepted with guidance; external tools should be run explicitly. |
| Twitter | `operTweeter*` posting | Legacy Twitter API integration is retired/unavailable; fields are parsed/preserved and warned about. |
| WUI GeoIP | `wuiAllowCountries` runtime filtering | Config is parsed and `compat check` warns. Runtime GeoIP filtering is intentionally omitted; use firewall, reverse proxy, VPN, or Basic auth. |
| WUI mDNS | `wuiMdnsAdvertisement` service advertisement | Config is parsed and `compat check` warns. mDNS advertisement is intentionally omitted to avoid extra runtime/network dependency. |
| WUI TLS | PFX/P12 key material | PEM key/cert TLS is supported. PFX/P12 is intentionally omitted; convert certificates to PEM for this Go runtime. |

## Verification Backlog

| Area | Check |
| --- | --- |
| Native WUI | Manual browser verification of expanded schedule grid, rule/manual-reserve flows, hidden-channel persistence, and MP4 playback buttons. |
| API/WUI streaming | Browser-level verification of fragmented MP4 playback behavior under realistic `ffmpeg`/`ffprobe` availability. |
| Legacy Socket.IO clients | Verify behavior with an old `web/` asset set or third-party Socket.IO client before declaring realtime parity complete. |
| Migration docs | Keep this file synchronized whenever `MIGRATION_COMPATIBILITY.md` moves an item from partial to implemented or intentionally changed. |
