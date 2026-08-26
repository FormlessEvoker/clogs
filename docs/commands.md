# Commands

Every command works with explicit flags and no configuration. When a
`clogs.yml` is present, it supplies defaults and enables the `--server`
shorthands described under each command. See
[configuration.md](configuration.md).

## Conventions

- Help and successful output go to stdout; diagnostics go to stderr.
- Exit `0` success, `2` invalid usage, `1` runtime failure.
- `--timezone` (alias `--tz`) takes an IANA name such as `America/Chicago`.
- Files written by Clogs are user-only (`0600`); directories are `0700`.

---

## `clogs remote list` / `clogs remote fetch`

Collect logs over your workstation's OpenSSH configuration.

```sh
clogs remote list  app-01.example.test --dir /opt/tomcat/logs
clogs remote fetch app-01.example.test --dir /opt/tomcat/logs --out ./downloads
```

Clogs never accepts, stores, or transmits credentials. It shells out to `ssh`
and `sftp`, so keys, agents, `known_hosts`, aliases, and ProxyJump work exactly
as you have them configured. A destination may include a user
(`someone@host.example`). If OpenSSH needs a password or a keyboard-interactive
challenge it prompts in your terminal.

Each command opens one temporary control connection and reuses it for all SFTP
operations, so you are prompted at most once per command.

### Filename patterns

Defaults are `application.log*`, `access_log*.log`, and `catalina.*.log`.
Repeating `--pattern` **replaces** the whole list rather than adding to it:

```sh
clogs remote list app-01.example.test --dir /opt/tomcat/logs \
  --pattern 'application.log*' --pattern 'catalina.*.log'
```

### Modification-time windows

```sh
--since 6h                      # changed in the last six hours
--after 21:30 --before 23:15    # times today, in the selected timezone
--after '2026-08-06 21:30'      # local date and time
--on 2026-08-06                 # one complete local calendar day
```

`--after`/`--before` also accept `HH:mm:ss`, `YYYY-MM-DD`, and RFC3339.
Time-only means today; date-only means midnight starting that date. Values
without an offset use the workstation timezone unless `--timezone` is given.
`--on`, `--since`, and `--after`/`--before` are mutually exclusive.

Filtering uses **remote file modification times**, not timestamps inside the
files. OpenSSH reports recent mtimes to the minute and older entries to the
day; the manifest records that precision alongside the resolved UTC bounds.

### What fetch writes

`<out>/<sanitized-destination>/<UTC-collection-time>/`. Each file downloads to
a temporary `.part` name and is atomically renamed on success, then recorded in
`manifest.json` with size, SHA-256, remote mtime, and reported precision.
Failed `.part` files are removed; the manifest records the failure and the
command exits non-zero, while successfully transferred files remain usable.

Fetch never parses or ingests.

### With configuration

```sh
clogs fetch app-01.example.test      # = remote fetch, using configured dir/window/patterns
clogs list  app-01.example.test
```

---

## `clogs parse`

Stream one file as newline-delimited JSON, without touching a database. The
fastest way to check that a format is recognized.

```sh
clogs parse ./catalina.2026-08-10.log --timezone America/Chicago
clogs parse ./gateway-access.log                    # access logs carry an offset
```

Detection is by content, not filename. Output preserves raw text, the original
timestamp string, the UTC timestamp, the source line range, parser warnings,
family-specific fields, and a stable signature.

`--timezone` is required for `jvm-multiline` and `catalina` input.

---

## `clogs ingest`

Recursively scan a file or directory, detect formats, and store events in
SQLite.

```sh
clogs ingest ./downloads/app-01.example.test/2026-08-10T142400Z \
  --db ./incident.db --source app-01.example.test --timezone America/Chicago
```

- `--db` is required. `--source` defaults to `local`.
- Raw evidence is stored by default; `--store-raw=false` omits it.
- The same source label + relative path + content hash ingests once. Identical
  logs under different source labels stay separate.
- Unrecognized files are skipped, not failed.
- Lenient by default: orphan lines and parser warnings are reported while valid
  events are retained. `--strict` instead rolls back the entire file and exits
  non-zero.

The database holds migration history, ingest-run metadata, source-file
provenance, signatures, events, and JVM application details. It is an ordinary
SQLite file — open it with any tool.

### With configuration

```sh
clogs ingest --server app-01.example.test
```

Omitting the input path uses the latest download collection for that server.
`--source` falls back to the server alias. With `paths.db_root` set, the
database goes to `<db_root>/<server>/<collection>/<source>.db`. With
`paths.source_root` also set, the collection is moved to
`<source_root>/<server>/<collection>` after a successful ingest.

---

## `clogs stats`

```sh
clogs stats --db ./incident.db [--source app-01.example.test] [--json]
```

Reports the stored time range, source files, parse warnings, family and
severity counts, HTTP status classes, and top routes, signatures, exceptions,
and protocol types. `--json` is deterministic and machine-readable.

---

## `clogs export`

```sh
clogs export --db ./incident.db --format ndjson
clogs export --db ./incident.db --format csv --output ./events.csv
```

Deterministic chronological/source order. NDJSON is one flattened event per
line; CSV has fixed columns and quotes multiline values safely. Both carry
source provenance. Raw text is omitted unless `--include-raw` is given. Output
defaults to stdout.

---

## `clogs query`

The analysis command. Merges all three families into one timeline and computes
the elapsed-time analyses.

```sh
clogs query --db ./incident.db \
  --around 2026-08-10T12:39:00-05:00 --before 15m --after 45m
```

Reports the merged timeline, elapsed-time buckets, route failure counts and
rates, signature frequencies, error and exception onsets, pre-onset windows,
and nearby HTTP request samples.

**Filters** narrow what is *displayed*: `--source`, `--family`, `--severity`,
`--status` (exact like `500`, or a class like `5xx`), `--route`, `--site`,
`--signature`.

**Analysis controls**: `--bucket`, `--quiet-period`, `--pre-window`,
`--correlation-window`.

An important distinction: analysis baselines always use the stated time and
source window. The other filters limit only the displayed timeline — so
filtering to `5xx` does not silently change the failure rates you are reading.

The window end is clamped to the latest available event for the selected
source, and `--around` must not be later than that event.

`--format json` for machine-readable output.

### With configuration

```sh
clogs query --server app-01.example.test --around 2026-08-10T12:39:00-05:00
```

Omitting `--db` uses the latest inferred collection under `paths.db_root`, and
`--source` defaults to the server alias.

---

## `clogs report incident`

Renders the same analysis as a single self-contained HTML file — no server, no
external assets, safe to attach to a ticket.

```sh
clogs report incident --db ./incident.db \
  --around 2026-08-10T12:39:00-05:00 --before 15m --after 45m \
  --timezone America/Chicago --output ./incident.html
```

`--db`, `--around`, `--timezone`, and `--output` are required without
configuration. Window options `--before`, `--after`, `--bucket`; analysis
options `--quiet-period`, `--pre-window`, `--correlation-window`; `--source`
limits to one source; `--title` replaces the heading.

**`--timezone` here controls display only.** Timestamps were normalized to UTC
at ingest — this decides how they are rendered back. Use the log-producing
server's timezone at *ingest* time; that is the one that affects correctness.

### Report sections

| Section | What it shows |
| --- | --- |
| Unified incident timeline | All families aligned, with requests, failures, and service lifecycle events |
| Route impact | Requests, failures, and failure rate per route template |
| Exception-to-failure proximity | How close in time exception onsets and HTTP failures fall |
| Pre-onset route pressure | Which routes were active in the window immediately before an onset |
| Repeated exact calls | Identical request signatures recurring in the window |
| Affected sites over time | Site-by-time heatmap |
| Evidence sequence | Expandable raw records with source file and line provenance |

Proximity is reported as proximity. The report states its correlation window
and sample size and does not assert causation.

### With configuration

```sh
clogs report incident --server app-01.example.test --around 2026-08-10T12:39:00-05:00
```

`--db`, `--timezone`, `--source`, and `--output` are all inferred; the report
lands at
`paths.reports_root/incident/<server>/<date>/<utc-stamp>-incident.html`.

---

## `clogs version`

```sh
clogs version     # "clogs dev" unless built with make build VERSION=v0.1.0
```
