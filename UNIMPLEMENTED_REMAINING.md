# Remaining Items

## Functionally Required

No known functionally required Strata runtime or migration item remains.

The supported installation path is now explicit:

1. Run `strata-pvr init` for a new installation.
2. For Chinachu data, copy it under `migrate/` and run
   `strata-pvr migrate`.
3. Run services and operational CLI commands only after `data/config.json`
   and `data/strata.db` exist.

## Deployment Verification

The following checks depend on the target environment and are not repository
implementation gaps:

- real Mirakurun service/program/tuner acquisition
- long-running recording and cancellation during shutdown
- disk-pressure behavior for `remove` and `stop`
- fragmented MP4 playback and seeking with the installed ffmpeg/ffprobe
- preview-cache retention with the deployment's recording volume
- reverse-proxy TLS, forwarded-header policy, and authentication exposure
- service-account permissions configured by systemd, Docker, or another
  process manager

## Optional Compatibility Work

These are intentionally outside the required Strata scope:

- byte-for-byte Node.js CLI table and JSON formatting
- uncommon JavaScript regexp and dateformat oracle tests
- legacy Socket.IO client oracle testing
- hidden or unreachable legacy WUI pages
- historical right-click menus, external searches, Twitter actions, favicon
  changes, and browser notification parity
- legacy multi-thumbnail and exact watch-toolbar behavior

Retired Chinachu configuration fields are accepted only by the migration
parser so that warnings can be produced. They are not part of the Strata
runtime configuration or WUI.
