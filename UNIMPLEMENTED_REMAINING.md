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
| Operator/recorder | `app-operator.js`, `common/lib/chinachu-common.js` | Core reserve execution, recording state, low-space behavior, abort handling, hooks, finalization, and normal/cancel finalize log ordering are implemented. Remaining signal risk is optional external JavaScript oracle validation for rare races. |
| WUI/API server | `app-wui.js`, `api/resource-*.json`, `api/script-*.vm.js` | API resources, static serving, auth/listeners, watch/preview routes, log streams, scheduler force, mutating actions, and legacy Socket.IO 0.9 XHR polling server push are covered. Remaining differences are old-client/oracle-test risks. |
| Legacy browser WUI | `web/index.html`, `web/chinachu.js`, `web/page/**/*.js` | Native Go WUI covers the main state-changing workflows. Remaining differences are page-level frontend affordances, old context menus, and browser-specific behavior, not core migration blockers unless legacy UI byte-for-byte behavior becomes a target. |
| Config and samples | `config.sample.json`, `rules.sample.json` | Non-retired runtime fields are parsed and mostly surfaced in WUI forms. Some Node-era integrations are intentionally not implemented. |

## Legacy WUI Feature Audit

The old WUI page map in `..\Chinachu\web\page\index.json` defines
`dashboard`, `schedule`, `rules`, `reserves`, `recording`, `recorded`,
`pref`, `search`, plus direct `program` and `channel` pages. The table below
records the current Strata status after checking each page implementation.
Some defined pages, such as `schedule/timeline`, are direct-hash pages rather
than entries exposed by the normal top navigation.

| Legacy page/source | Legacy function/operation covered | Strata status |
| --- | --- | --- |
| `dashboard/top.js` | Summary panels for on-air channels, reserves, recording, recorded; live watch entry; program detail navigation; reserve/skip/stop/delete/rule actions through context menus. | Core workflow implemented in the dashboard and dialogs. Old right-click context-menu utilities remain parity backlog. |
| `dashboard/status.js`, `dashboard/storage.js`, `dashboard/log.js` | Process/status display, storage visualization, and scheduler/operator/WUI log stream panels. | Implemented with native status, storage/metrics, and log panels. Exact D3 chart styling and streaming log behavior are not byte-for-byte parity targets. |
| `schedule/table.js` | Vertical timetable with type/day/category/hidden-channel controls, channel logo/watch/search links, reserve/skip/stop/rule actions, and mouse/keyboard panning. | Main timetable implemented with type/channel/day/genre filters, hidden-channel persistence, logo display, state badges, zoom, and core actions. Old popover/context-menu actions and exact panning behavior remain partial. |
| `schedule/timeline.js` | Direct-hash horizontal timeline view with channel rows, time-axis scrolling, drawer, keyboard movement, and context menus. It is defined in `index.json`, but the normal schedule nav defaults to `schedule/table` and no `pageIndex` exposes a side-nav link. | Not implemented. Because it is not reachable from the normal legacy WUI navigation, it is treated as an implementation-unnecessary hidden page rather than native WUI parity backlog. |
| `search/top.js` | Dedicated future-program search page with modal fields for category, title, description, type, hour range, program ID, and channel ID, plus URL-preserved pagination. | Implemented as integrated schedule detail filters for title, description, program ID, channel ID, and hour range, combined with existing type/category/channel controls. The old standalone hash page is not reproduced. |
| `recorded/search.js` | Dedicated recorded-program search page with category/title/description/type/hour/program/channel fields and URL-preserved pagination. | Implemented as integrated recorded-list detail filters for title, description, type, program ID, channel ID, and hour range, combined with existing category/sort controls. The old standalone hash page is not reproduced. |
| `rules/list.js` | Grid view, scheduler execute button, add/edit/delete, multi-select delete, double-click edit, and broad rule-field columns. | Core rule add/edit/delete, scheduler force, JSON/form editing, filtering, sorting, and extra JSON fields are implemented. Multi-select bulk delete and exact old grid column behavior remain parity backlog. |
| `reserves/list.js`, `recording/list.js`, `recorded/list.js` | Paginated grids with detail navigation, reserve skip/unskip/cancel, stop recording, cleanup, delete, create rule, tweet/copy/external-search context menus. | Core list views and state-changing actions are implemented. Tweet/SCOT/copy/Google/Wikipedia context-menu utilities and exact pagination model are not implemented. |
| `program/view.js` | Full program detail, reserve/skip/stop/delete/download/watch/rule actions, status alerts, URL auto-linking, recorded file size, three recorded thumbnails, recording thumbnail, prev/next pager and Left/Right shortcuts. | Core detail dialog and actions are implemented. URL auto-linking, multi-thumbnail detail view, file-size alert, and prev/next detail pager are not implemented. |
| `program/watch.js`, `channel/watch.js` | MP4/M2TS/XSPF watch forms, in-browser playback where possible, VLC deep links on mobile, local playback settings, volume control. | API watch routes and native playback/XSPF flows are implemented. Mobile VLC intent/callback shortcuts and exact old watch form behavior remain parity backlog. |
| `pref/config.js` | Ace-based raw `config.json` editor with reload/save toolbar actions. | Raw config editing and known-field form editing are implemented. Ace editor integration and exact toolbar behavior are not implemented. |
| `web/chinachu.js` shell | Socket.IO realtime updates, nav badges, favicon/notification changes, footer connected count/operator status, category hotkeys, side page index. | Same-origin refresh notifications and nav counts/status are implemented. Browser notification/favorite-icon changes, footer connected-count display, and exact old category shell behavior are not implemented. |

## Functionally Required Remaining Items

| Priority | Area | Remaining item | Why it is still functionally required |
| --- | --- | --- | --- |
| - | WUI/API server | No known must-implement runtime item remains. | Legacy Socket.IO 0.9 handshake and XHR polling server push are implemented for state-file changes; old-client oracle validation remains a compatibility check rather than a known missing runtime feature. |

## Compatibility Risks / Needs Oracle Tests

| Area | Remaining risk |
| --- | --- |
| CLI tables | Byte-for-byte `easy-table` truncation/alignment edge cases for `rules`, `reserves`, `recording`, `recorded`, and `search` output. |
| JSON state writes | Known fields are emitted in stable legacy-oriented order, but unknown-field insertion order and obscure spacing edge cases can still differ from Node `JSON.stringify` behavior. |
| Rule engine | JavaScript RegExp semantics are approximated with Go regexp; uncommon regex behavior needs oracle tests against `app-cli.js`/`chinachu-common.js`. |
| Recorded filename format | Unusual JavaScript `dateformat` parsing edge cases need oracle tests against the legacy `dateformat` module. |
| Operator signals | Runtime services cancel on `SIGINT`, `SIGTERM`, and Unix `SIGQUIT`; active streams are closed, recording/recorded state is finalized, and `recordedCommand` plus `FIN` logging/order are covered. Remaining risk is optional external JavaScript oracle validation for rarer external-signal races. |
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
| Live/recording watch actions | mostly implemented | Live and recording MP4 conversion watch actions are exposed as playback-oriented labels, and schedule/on-air/detail rows switch to recording watch/stop actions when the same program is in `recording.json`. M2TS buttons, including recorded-file start-offset playback, are intentionally limited to recorded files to avoid infinite live download behavior. Legacy live M2TS endpoints remain for API compatibility. |
| Legacy advanced search pages | mostly implemented | Schedule and recorded views now include integrated detail filters covering title, description, program ID, channel ID, and hour range, with recorded type and existing schedule type/category/channel controls. Old standalone `search/top` and `recorded/search` hash pages are not reproduced one-for-one. |
| Legacy context-menu utilities | not implemented | Old grids and schedule cards exposed tweet, SCOT copy, ID/title/description copy, Google, related-site, and Wikipedia actions. These do not change recorder state and are omitted from the native WUI. |
| Legacy program detail extras | partial | Core program metadata and actions are available, but old URL auto-linking, recorded file-size alert, three-position thumbnail gallery, recording thumbnail placement, previous/next pager, and Left/Right pager shortcuts are not fully reproduced. |
| Legacy watch-page browser integrations | partial | Playback, MP4 conversion options, XSPF, and recorded-file download/start-offset workflows are covered. Old mobile VLC intent/callback launching, local `channel.watch.settings` parity, and exact inline video toolbar behavior are not implemented. |
| Legacy shell/browser chrome | partial | Counts and operational status are visible in native navigation/status views. Old favicon switching, browser notifications, footer connected-count display, operator footer button, and side-page index behavior are not reproduced exactly. |
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
| Legacy hidden WUI page | `schedule/timeline` direct-hash page | Defined in `web/page/index.json`, but not exposed by the normal legacy WUI navigation. The supported schedule view is `schedule/table`; Strata does not need to reproduce unreachable hidden UI pages. |
| WUI GeoIP | `wuiAllowCountries` runtime filtering | Config is parsed and `compat check` warns. Runtime GeoIP filtering is intentionally omitted; use firewall, reverse proxy, VPN, or Basic auth. |
| WUI mDNS | `wuiMdnsAdvertisement` service advertisement | Config is parsed and `compat check` warns. mDNS advertisement is intentionally omitted to avoid extra runtime/network dependency. |
| WUI TLS | PFX/P12 key material | PEM key/cert TLS is supported. PFX/P12 is intentionally omitted; convert certificates to PEM for this Go runtime. |

## Verification Backlog

| Area | Check |
| --- | --- |
| Native WUI | Manual browser verification of expanded schedule grid, rule/manual-reserve flows, hidden-channel persistence, and MP4 playback buttons. |
| API/WUI streaming | Browser-level verification of fragmented MP4 playback behavior under realistic `ffmpeg`/`ffprobe` availability. |
| Legacy Socket.IO clients | Verify behavior with an old `web/` asset set or third-party Socket.IO client before declaring byte-for-byte realtime parity complete; the Go server implements Socket.IO 0.9 handshake and XHR polling server push, but does not advertise WebSocket transport. |
| Migration docs | Keep this file synchronized whenever `MIGRATION_COMPATIBILITY.md` moves an item from partial to implemented or intentionally changed. |
