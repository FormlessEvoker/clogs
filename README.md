# Clogs

Clogs collects, parses, and correlates JVM application logs, Tomcat Catalina
logs, and HTTP access logs, then renders a self-contained incident report.

It fetches logs through your existing OpenSSH configuration, normalizes three
log families into one event model, stores them in SQLite, and produces a merged
timeline with route failure rates, error onsets, and temporal proximity — while
explicitly declining to assert causation.

Nothing is deployment-specific: remote directories, filename patterns,
timezones, route shapes, and analysis defaults all live in a `clogs.yml` you
control.

## Install

```sh
go install github.com/FormlessEvoker/clogs/cmd/clogs@latest
```

Or build from a clone:

```sh
make build            # -> ./bin/clogs
make build VERSION=v0.1.0
./bin/clogs version
```

Requires Go 1.23 or later. The SQLite driver is pure Go, so `CGO_ENABLED=0`
builds work.

## Quick start

Create a workspace with a `clogs.yml` (copy [`clogs.example.yml`](clogs.example.yml)),
then:

```sh
# Collect, using the directories, patterns, and window from config.
clogs fetch app-01.example.test

# Ingest the most recent collection for that server.
clogs ingest --server app-01.example.test

# Analyze a window, then render the report.
clogs query  --server app-01.example.test --around 2026-08-10T12:39:00-05:00 \
  --before 15m --after 45m
clogs report incident --server app-01.example.test \
  --around 2026-08-10T12:39:00-05:00 --before 15m --after 45m
```

`--server` resolves that server's configuration and infers the collection
directory, database path, and report path from `paths` in `clogs.yml`. Every
value remains overridable with an explicit flag.

The same workflow without configuration, passing everything explicitly:

```sh
clogs remote fetch app-01.example.test --dir /opt/tomcat/logs --since 6h --out ./downloads
clogs ingest ./downloads/app-01.example.test/2026-08-10T142400Z \
  --db ./clogs.db --source app-01.example.test --timezone America/Chicago
clogs parse  ./app.log --timezone America/Chicago
clogs stats  --db ./clogs.db
clogs export --db ./clogs.db --format csv --output ./events.csv
clogs query  --db ./clogs.db --around 2026-08-10T12:39:00-05:00 --before 30s --after 30s
```

## Configuration

Clogs searches the current directory and each parent for `clogs.yml`, then
`clogs.yaml`. Relative paths resolve against the directory containing the file
that was found, not the process working directory. Unknown keys are rejected,
so a typo fails loudly rather than being silently ignored.

Precedence, highest first:

```text
explicit CLI flag  >  remote.servers.<name>  >  remote.defaults
                   >  <command> section      >  defaults
```

A flag participates only when you actually supplied it — a flag parser's own
default does not override configuration.

See [`clogs.example.yml`](clogs.example.yml) for the full annotated schema.

### Route templates

This is the setting most worth knowing about. Route normalization is
configuration, not built-in behavior:

```yaml
defaults:
  route_templates:
    - /svc/v4/api/site/{site}
```

Literal segments must match exactly; `{name}` matches any one non-empty
segment; a template matches a prefix, so trailing segments are preserved.
`{site}` is special — its captured value becomes the event's site.

Without a matching template, each distinct path is its own route, so failure
rates fragment across every variable value instead of grouping:

```text
with:     /svc/v4/api/site/{site}/orders   778 requests, 43 failures
without:  /svc/v4/api/site/site-a/orders   143 requests,  8 failures
          /svc/v4/api/site/site-b/orders   131 requests,  9 failures
          ...
```

## CLI conventions

- Help and successful command output are written to stdout.
- Diagnostics are written to stderr.
- Exit code `0` indicates success, `2` indicates invalid command-line usage,
  and `1` is reserved for runtime failures.
- `fetch` and `list` are configuration-driven shorthands for `remote fetch` and
  `remote list`. The `remote` forms remain available and take explicit flags.

## Remote collection

`clogs` delegates authentication and host verification to the local `sftp`
program. Configure the destination in OpenSSH as usual (keys, agent,
`known_hosts`, aliases, and ProxyJump all work). A destination may include a
specific user, such as `johndoe@server.example`. If OpenSSH needs a password or
keyboard-interactive challenge, it prompts through the terminal; Clogs neither
accepts nor stores passwords or credentials.

Each `remote` command opens one temporary OpenSSH control connection and reuses
it for its SFTP operations, so a password prompt occurs at most once per command.

```sh
clogs remote list app-01.example.test --dir /opt/tomcat/logs
clogs remote fetch app-01.example.test --dir /opt/tomcat/logs --out ./downloads

# Today between 9:30 PM and 11:15 PM in the workstation timezone.
clogs remote list app-01.example.test --dir /opt/tomcat/logs \
  --after 21:30 --before 23:15

# One server-local calendar day, with its timezone supplied explicitly.
clogs remote fetch app-01.example.test --dir /opt/tomcat/logs \
  --on 2026-08-06 --tz America/New_York --out ./downloads
```

The default filename allowlist is `application.log*`, `access_log*.log`, and
`catalina.*.log`. Repeat `--pattern` to replace those defaults, for example
`--pattern 'application.log*' --pattern 'catalina.*.log'`.

Remote modification-time filters are available on both `list` and `fetch`:

- `--since 6h` selects files changed between six hours ago and the command's
  captured current time.
- `--after` and `--before` accept `HH:mm`, `HH:mm:ss`, `YYYY-MM-DD`, a local
  date and time, or RFC3339. Time-only input means today; date-only input means
  midnight at the start of that date.
- `--on YYYY-MM-DD` selects the complete calendar day.
- Inputs without an explicit offset use the workstation timezone by default.
  Use `--timezone` or its short alias `--tz` to select an IANA timezone such as
  `America/New_York`.

`--on`, `--since`, and `--after`/`--before` are mutually exclusive. Filtering
uses remote file modification times, not timestamps inside the files. OpenSSH's
SFTP listing reports recent mtimes to the minute and older entries to the day;
the manifest records that precision together with the resolved UTC boundaries.

Fetch creates `downloads/<sanitized-source>/<UTC-collection-time>/` with
user-only permissions. Each completed file is downloaded through a temporary
`.part` name and atomically renamed, then recorded in `manifest.json` with its
size, SHA-256, remote modification time, and reported precision. Failed `.part`
files are removed; the manifest records the
failure and the command exits non-zero, while successful files remain available
for inspection. Remote collection never invokes parsing or SQLite ingestion.

## JVM application-log inspection

`parse` recognizes JVM application, Catalina, and access-log content rather than relying on a filename
and streams one normalized event per NDJSON line. JVM timestamps have no
timezone offset, so an IANA timezone is required for JVM:

```sh
clogs parse ./application.log --timezone America/Chicago --format ndjson
```

The output preserves the raw event text, original timestamp, UTC timestamp,
source-line range, parser warnings, family-specific fields, and a stable
signature. Access logs preserve the client, port, method, target/path/query,
route template, site, HTTP version, status, and nullable response bytes.
Catalina events preserve millisecond precision, thread, logger, operation,
exception/root cause, and stack trace.

## SQLite ingestion

`ingest` recursively scans a file or directory, detects supported content, and
stores JVM application, Catalina, and access events in SQLite. `--db` is required;
`--timezone` is required when JVM or Catalina files are present. `--source`
defaults to `local`. Raw evidence is stored by default and can be omitted with
`--store-raw=false`. The same source label, relative path, and content hash is
ingested once; identical logs from distinct source labels remain separate.

Lenient ingestion reports orphan lines and parser warnings while retaining valid
events. `--strict` instead rolls back that entire file and returns a non-zero
exit. The database contains migration history, ingest-run metadata, source-file
provenance, signatures, events, and JVM application details, and can be opened with
standard SQLite tools.

## Export and statistics

`clogs stats --db ./clogs.db [--source label] [--json]` reports the stored time
range, source files, parse warnings, family/severity counts, HTTP status classes,
and top routes, signatures, exceptions, and protocol types.

`clogs export --db ./clogs.db --format ndjson|csv [--source label]` writes events
in stable chronological/source order. NDJSON contains one flattened event per
line; CSV has fixed columns and safely quotes multiline data. Both include source
provenance. Raw text is omitted by default and is exported only with
`--include-raw`.

## Cross-log queries

`clogs query` displays all three families in deterministic time order and reports
elapsed-time buckets, route failure rates, signature frequencies, error/exception
onsets, pre-onset windows, and nearby HTTP-request samples. Filters include
source, family, severity, HTTP status or class, route, site, and signature.

Analysis baselines always use the stated time/source window; the other filters
limit the displayed timeline. Correlation output reports the configured window
and sample size and explicitly avoids causal claims. Use `--format json` for a
machine-readable result.

## Incident visualization

`clogs report incident` generates a deterministic, self-contained HTML report
from the same merged analysis contract used by `clogs query`:

```sh
./bin/clogs report incident \
  --db ./clogs.db \
  --around 2026-08-06T22:20:00-04:00 \
  --before 50m \
  --after 55m \
  --bucket 1m \
  --timezone America/New_York \
  --source server-a \
  --output ./incident-report.html
```

The portable report contains an aligned request/failure/OOM/service-lifecycle
timeline, route failure counts and rates, OOM-to-failure proximity, a
site-by-time heatmap, and expandable evidence with source-file and line
provenance. It requires no server or external assets. `--timezone` controls only
how normalized database timestamps are displayed; use the log-producing
server's timezone during ingestion for JVM and Catalina's zone-less timestamps.

## Privacy

Do not commit production logs, downloaded collections, SQLite databases, or
incident reports containing production evidence. `.gitignore` excludes those by
default, and `make check` runs `scripts/check-prohibited-content.sh` across the
tree to catch credential-shaped values, non-reserved IPv4 addresses, external
URLs outside the RFC 2606 example domains, and deployment identifiers that
should not be public.

Everything under `testdata/` is invented. Keep it that way — see
[`testdata/README.md`](testdata/README.md).

Clogs never accepts, stores, or transmits credentials. Remote authentication is
delegated entirely to your local OpenSSH client.
