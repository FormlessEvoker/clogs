# Remote Acquisition Contract

This document records the compatibility contract for the current low-level
`clogs remote list` and `clogs remote fetch` commands. It is descriptive; it
does not introduce configuration or profile behavior.

## Inputs and selection

- A request has one non-empty OpenSSH destination and one absolute remote
  directory. Destinations cannot start with `-` or contain control characters;
  directories cannot contain control characters or `..`.
- Default patterns are `application.log*`, `access_log*.log`, and
  `catalina.*.log`. Supplying one or more patterns replaces that list. Patterns
  must be non-empty, control-character-free, and valid path globs.
- Only regular files in an SFTP long listing participate. Names containing a
  path separator, control character, `.`, or `..` reject the listing rather
  than becoming local paths.
- Matches are lexically ordered by filename. Modification windows filter remote
  mtimes inclusively; a minute-precision listing represents that full minute
  and a day-precision listing represents that full day.

## Transport and output

- Authentication, host verification, aliases, and ProxyJump remain OpenSSH's
  responsibility. Every command creates one private control connection and
  reuses its control path for listing and every transfer, then requests master
  shutdown.
- Fetch creates `<output>/<sanitized-source>/<UTC timestamp>/` with user-only
  directories. Sanitization is lossy; colliding source names do not merge
  collections because an existing timestamped collection is rejected.
- A transfer writes `<name>.part`, then atomically renames it to `<name>` only
  on success. Completed files are mode `0600`; failed temporary files are
  removed. Successful files remain available if another transfer fails.
- `manifest.json` is mode `0600` and records source, remote directory,
  resolved patterns/window, UTC collection time, successful-file hashes and
  remote mtime precision, plus failures. A partial transfer returns an error
  only after writing that manifest.
- Collection never parses logs or writes SQLite data.
