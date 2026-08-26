# Clogs

**Correlated logs.** When a Tomcat service misbehaves, the evidence is split
across three files that don't agree on time: the application log, the Catalina
log, and the HTTP access log. One has millisecond timestamps in local time,
another has no timezone at all, the third has a numeric UTC offset. Lining them
up by hand, during an incident, is miserable.

Clogs reads all three, normalizes them to UTC, and gives you one timeline:

```text
2026-08-11T21:30:00Z jvm-multiline application.log:1 database query completed
2026-08-11T21:30:30Z access   gateway-access.log:1 HTTP 200 GET /svc/v4/api/site/north/orders
2026-08-11T21:31:00Z access   gateway-access.log:2 HTTP 200 GET /svc/v4/api/site/north/orders
2026-08-11T21:31:30Z access   gateway-access.log:3 HTTP 500 GET /svc/v4/api/site/north/orders
2026-08-11T21:31:35Z jvm-multiline application.log:3 database query timed out
2026-08-11T21:31:40Z catalina catalina.log:1 database timeout detected
2026-08-11T21:32:30Z catalina catalina.log:4 service recovered
```

Then it will fetch the logs off the server for you, keep them in SQLite so you
can query the window repeatedly, and render a self-contained HTML report you
can send to someone who wasn't on the call.

It reports temporal proximity and never claims causation.

## Install

```sh
go install github.com/FormlessEvoker/clogs/cmd/clogs@latest
```

Or from a clone — `make build` puts a binary in `./bin/clogs`. Requires Go 1.23
or later; the SQLite driver is pure Go, so `CGO_ENABLED=0` works.

## Try it in 60 seconds

No configuration required. Every command below takes explicit flags.

```sh
git clone https://github.com/FormlessEvoker/clogs && cd clogs && make build

# A small invented incident ships with the repo.
./bin/clogs ingest ./testdata/incidents/database-timeout \
  --db /tmp/demo.db --timezone America/Chicago

./bin/clogs query --db /tmp/demo.db \
  --around 2026-08-11T16:31:35-05:00 --before 5m --after 5m
```

That prints the timeline above, plus failure rates, error onsets, and which
requests were in flight nearby. To get the report instead:

```sh
./bin/clogs report incident --db /tmp/demo.db \
  --around 2026-08-11T16:31:35-05:00 --before 5m --after 5m \
  --timezone America/Chicago --output /tmp/incident.html
```

Open `/tmp/incident.html` — one file, no server, no external assets, safe to
attach to a ticket.

### On your own logs

```sh
# Look at a single file first — streams normalized events as NDJSON.
clogs parse ./catalina.2026-08-10.log --timezone America/Chicago

# Pull logs off a server using your existing SSH config.
clogs remote fetch app-01.example.test --dir /opt/tomcat/logs --since 6h --out ./downloads

# Ingest a whole collection, then analyze it.
clogs ingest ./downloads/app-01.example.test/2026-08-10T142400Z \
  --db ./incident.db --source app-01.example.test --timezone America/Chicago
```

`--timezone` is required for application and Catalina logs, because their
timestamps carry no UTC offset. Use the timezone of the server that wrote them.

Clogs never handles credentials — `remote` shells out to your own `ssh` and
`sftp`, so keys, agents, `known_hosts`, aliases, and ProxyJump all work as
configured.

## Then: stop retyping flags

Once you've run this twice you'll be tired of the flags. Drop a `clogs.yml` in
your incident directory and the same workflow collapses to:

```sh
clogs fetch  app-01.example.test
clogs ingest --server app-01.example.test
clogs report incident --server app-01.example.test --around 2026-08-10T12:39:00-05:00
```

Clogs finds `clogs.yml` by searching upward from the working directory, and
infers download, database, and report paths from it. Explicit flags still win.

One setting is worth knowing about early — **route templates**, which collapse
variable path segments so failure rates group instead of fragmenting:

```yaml
defaults:
  route_templates:
    - /svc/v4/api/site/{site}
```

```text
with:     /svc/v4/api/site/{site}/orders    778 requests, 43 failures
without:  /svc/v4/api/site/site-a/orders    143 requests,  8 failures
          /svc/v4/api/site/site-b/orders    131 requests,  9 failures
          ...
```

There is no default, so if your routes embed IDs and your report looks
fragmented, this is why. Full schema: [`clogs.example.yml`](clogs.example.yml)
and [docs/configuration.md](docs/configuration.md).

## What it understands

| Family | Shape |
| --- | --- |
| `jvm-multiline` | JVM application logs — multi-line records, severity on a continuation line, no timezone |
| `catalina` | Tomcat Catalina — millisecond timestamps, thread, logger, exception and root cause, stack traces |
| `access` | HTTP access logs — client, port, method, target, status, bytes; carries its own UTC offset |

Detection is by content, not filename. Events normalize into one model that
keeps the raw text, the original timestamp, the source file and line range, and
a stable signature for grouping recurrences.

## Documentation

- [Commands](docs/commands.md) — every command and flag
- [Configuration](docs/configuration.md) — `clogs.yml` reference and precedence
- [Architecture](docs/architecture.md) — how it works, and why it's built this way
- [Development](docs/development.md) — building, testing, and project layout

## Privacy

Do not commit production logs, downloaded collections, SQLite databases, or
incident reports. `.gitignore` excludes them, and `make check` runs
`scripts/check-prohibited-content.sh` across the tree to catch credential-shaped
values, non-reserved IPv4 addresses, external URLs outside the RFC 2606 example
domains, and deployment identifiers that should not be public.

Everything under `testdata/` is invented — see
[`testdata/README.md`](testdata/README.md).

## License

MIT — see [LICENSE](LICENSE).
