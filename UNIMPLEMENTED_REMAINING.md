# Unimplemented Remaining Items

This list is derived from `MIGRATION_COMPATIBILITY.md` after the current
compatibility audit. It separates true missing runtime behavior from intentional
compatibility breaks and residual parity risks.

## Runtime Gaps

| Area | Remaining item | Notes |
| --- | --- | --- |
| WUI realtime | Full Socket.IO realtime push transport | `/socket.io/socket.io.js` remains a polling compatibility shim, but common `notify-*` resources, duplicate-poll suppression, `once`/`off`, manual `emit`, disconnect cleanup, basic reconnect events, and same-origin `BroadcastChannel`/`storage` notification wakeups are covered. The native WUI now publishes matching notifications after mutating API calls and refreshes other open tabs immediately. Exact Socket.IO server push transport/protocol and legacy event ordering are still not reproduced. |
| WUI frontend | Legacy list/filter affordances | mostly implemented | Main dashboard, schedule, recording, recorded, rule, and manual reserve flows are implemented; reservation, recorded, channel-program, and rule pages now have title/detail/channel/condition search with category or state filtering where relevant, sort controls, clear actions, and those list filters are restored from local storage. Fine legacy affordances such as every old per-column filter/sort shortcut, named filter presets, compact page transition, and historical list interaction remain unverified. |
| WUI frontend | Legacy dialogs and confirmations | partial | Native confirmation dialog now covers destructive actions, config save, and rule add/save from both the JSON editor and form editor, with metadata where relevant, cancel/backdrop/Esc handling, danger styling, and focus restoration. Exact old dialog text, browser-specific default-button behavior, and validation timing are still not byte-for-byte compatible. |
| WUI frontend | Legacy keyboard/mouse shortcuts | mostly implemented | Primary mouse operations work. Native shortcuts now cover number-key section navigation, `r` refresh, `/` current-view search focus, `j`/`k` and arrow-key row movement, `Esc` dialog close, row Enter/Space detail open, and row double-click detail open. Exhaustive old context-menu affordances and browser-specific shortcut behavior are not yet mapped. |
| WUI frontend | Legacy visual/state parity | partial | Hidden-channel persistence, schedule navigation, log panels, main actions, selected-program highlighting, per-view window scroll restoration, schedule-grid scroll retention, initial loading/failure placeholders, refresh button busy state, and schedule/on-air/detail reservation/recording state badges are implemented. Exact old empty/error wording and minor visual transitions remain partially covered. |
| WUI frontend | Live/recording watch actions | mostly implemented | Live and recording MP4 conversion watch actions are exposed as playback-oriented `視聴` labels, and schedule/on-air/detail rows now switch to recording watch/stop actions when the same program is in `recording.json`. M2TS buttons are intentionally limited to recorded files to avoid infinite live download behavior. Legacy live M2TS endpoints remain for API compatibility. |
| WUI frontend | Rare non-retired config controls | mostly implemented | Known non-retired runtime config fields in `internal/config.Config` have dedicated form controls and are now also visible in the settings summary. Future runtime config additions should get controls when they have clear operational value. |
| WUI frontend | Unknown/custom config fields | intentionally raw JSON | Unknown fields are preserved through raw JSON editing rather than first-class controls, because the Go runtime cannot know deployment-specific extension semantics. |
| WUI frontend | Retired Tweeter/Twitter config fields | intentionally raw JSON | Legacy Twitter posting fields are parsed/preserved and warned about, but dedicated WUI controls are intentionally omitted because the integration is retired/unavailable. |
| Operator | Rare in-flight signal parity | Runtime services cancel on `SIGINT`, `SIGTERM`, and Unix `SIGQUIT`; active streams are closed, recording/recorded state is finalized, and `recordedCommand` plus `FIN` logging are covered even after context cancellation. Remaining risk is exact byte-for-byte cleanup/log ordering for rarer external-signal races. |

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

## Compatibility Risks / Needs Oracle Tests

| Area | Remaining risk |
| --- | --- |
| CLI tables | Byte-for-byte `easy-table` truncation/alignment edge cases for `rules`, `reserves`, `recording`, `recorded`, and `search` output. |
| JSON state writes | Known fields are emitted in stable legacy-oriented order, but unknown-field insertion order and obscure spacing edge cases can still differ. |
| Rule engine | JavaScript RegExp semantics are approximated with Go regexp; edge cases need oracle tests. |
| Recorded filename format | Unusual JavaScript `dateformat` parsing edge cases need oracle tests. |
| Mirakurun client | Product token intentionally uses `StrataPVR` instead of the legacy product name. |
| Logs | Scheduler/operator/WUI logs cover major legacy lines, but exact shell/log formatting parity remains partial. |
| Tests | Optional JavaScript oracle tests remain future work and are not required for normal `go test ./...`. |

## Verification Backlog

| Area | Check |
| --- | --- |
| Native WUI | Manual browser verification of expanded schedule grid, rule/manual-reserve flows, hidden-channel persistence, and MP4 playback buttons. |
| API/WUI streaming | Browser-level verification of fragmented MP4 playback behavior under realistic `ffmpeg`/`ffprobe` availability. |
| Migration docs | Keep this file synchronized whenever `MIGRATION_COMPATIBILITY.md` moves an item from partial to implemented or intentionally changed. |
