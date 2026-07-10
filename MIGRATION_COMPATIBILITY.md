# Strata Migration Compatibility

This document describes the supported migration contract from Chinachu to
Strata PVR. It does not promise byte-for-byte compatibility with the old
Node.js runtime.

## Runtime Contract

- Strata reads `data/config.json` using the `strata/config` schema.
- SQLite at `data/strata.db` is the canonical state store.
- `data/rules.json` remains as a compatibility/export file.
- A root-level Chinachu `config.json` is never used as runtime configuration.
- Existing installations must place their files under `migrate/` and run
  `strata-pvr migrate` before starting services or CLI runtime commands.
- The runtime binary does not require Go, Node.js, SQLite, CGO, or a compiler.

## Migration Input

```text
migrate/
  config.json
  rules.json
  data/
    reserves.json
    recording.json or recordings.json
    recorded.json
    schedule.json
```

The migrator validates every JSON document before installing Strata data.
Successful migration moves the input to
`backup/chinachu-<timestamp>/` and writes a version 3 migration report with
SHA-256 hashes, byte sizes, imported row counts, and warnings.

Archive hashes and sizes are compared with the source snapshot. A mismatch,
archive move failure, or final data installation failure restores `migrate/`
and leaves no installed `data/` directory. The migration can then be retried.

## Converted Settings

| Chinachu | Strata |
| --- | --- |
| `mirakurunPath` / `schedulerMirakurunPath` | `mirakurun.url` |
| `recordingPriority`, `conflictedPriority` | `mirakurun.*Priority` |
| `recordedDir`, `recordedFormat` | `recording.directory`, `recording.filenameFormat` |
| low-space threshold and `remove`/`stop` | `recording.lowSpace` |
| authenticated or open WUI listener | one `web` listener with authentication ON/OFF |
| `wuiUsers` plaintext credentials | Argon2id password hashes |
| excluded services and service order | `services` |
| normalization form | `advanced.normalizationForm` |

Rules, reservations, schedule channels/programs, active recordings, and
recorded entries are imported into SQLite while preserving their legacy JSON
documents for API compatibility.

## Retired Settings

The following settings are detected and reported but are not imported:

- built-in TLS and client certificates
- `X-Forwarded-For` trust and GeoIP filtering
- mDNS and Twitter/Tweeter integration
- VAAPI-specific transcoding
- scheduler, EPG, conflict, recorded, and low-space command hooks
- sendmail low-space notifications
- internal UID/GID privilege switching

TLS, forwarded-header trust, and network access policy belong at a reverse
proxy, firewall, or VPN endpoint. The service manager or container runtime is
responsible for the Strata process account.

## Compatibility Scope

The Go runtime retains the Chinachu-shaped program/rule documents and the
main CLI/WUI API behavior needed for schedules, reservations, recording,
playback, cleanup, and rule management. Exact Node.js table formatting,
JavaScript regexp edge cases, hidden legacy pages, historical context menus,
and byte-for-byte JSON ordering are not migration requirements.

## Verification

Automated coverage includes successful migration, corrupt input, archive hash
mismatch with rollback and retry, archive move failure, large reservation
imports, SQLite repositories, authentication, and WUI settings. Deployment
verification should additionally cover a real Mirakurun endpoint, long-running
recording, disk pressure, and ffmpeg playback on the target host.
