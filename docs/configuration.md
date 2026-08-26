# Configuration

Configuration is optional. Every command works with explicit flags — see
[commands.md](commands.md). A `clogs.yml` exists so you stop retyping them, and
so deployment-specific knowledge lives in your workspace instead of in the
tool.

[`clogs.example.yml`](../clogs.example.yml) is a complete annotated file to
copy.

## Discovery

Clogs searches the current directory, then each parent, for `clogs.yml`, then
`clogs.yaml`. The first match wins.

Relative paths resolve against **the directory containing the file that was
found**, not the process working directory. So you can run Clogs from a
subdirectory of your incident workspace and paths still point where you meant.

Decoding is strict: unknown keys are an error. A typo fails loudly instead of
being silently ignored.

## Precedence

```text
explicit CLI flag
  > remote.servers.<name>      (remote commands only)
  > remote.defaults            (remote commands only)
  > <command> section          (parse / ingest / export / stats / query / report)
  > defaults
```

A flag participates only if you actually typed it. A flag parser's own default
value does not override configuration — otherwise every unset flag would
silently beat your config file.

Two list-valued settings, `pattern` and `route_templates`, **replace** rather
than extend when overridden. Repeating `--pattern` on the command line clears
the configured list first, so what you type is exactly what you get.

The mutually-exclusive time-window family (`after`, `before`, `on`, `since`) is
handled as a group: if a more specific layer sets any one of them, the whole
group from lower layers is discarded. A per-server `since` cannot collide with
a global `on`.

## Sections

### `defaults`

Applies to every command. Anything valid in a per-command section is valid
here.

```yaml
defaults:
  timezone: America/Chicago
  route_templates:
    - /svc/v4/api/site/{site}
```

### `paths`

Workspace layout, relative to the config file. These are what make `--server`
inference work.

```yaml
paths:
  downloads_root: ./downloads
  db_root: ./data/db
  source_root: ./data/source
  reports_root: ./reports
```

- `downloads_root` — where `fetch` writes collections
- `db_root` — where inferred databases go:
  `<db_root>/<server>/<collection>/<source>.db`
- `source_root` — if set alongside `db_root`, ingested collections move here
  afterward, keeping `downloads_root` to un-ingested material
- `reports_root` — inferred reports land at
  `<reports_root>/incident/<server>/<date>/<utc-stamp>-incident.html`

### Per-command sections

`parse`, `ingest`, `export`, `stats`, `query`, `report`. Each inherits from
`defaults`.

```yaml
ingest:
  store_raw: true
  strict: false

query:
  bucket: 1m
  quiet_period: 30s
  pre_window: 30s
  correlation_window: 5s

report:
  bucket: 1m
  correlation_window: 5s
```

### `remote`

```yaml
remote:
  defaults:
    dir: /opt/tomcat/logs
    since: 6h
    out: ./downloads
    pattern:
      - application.log*
      - access_log*.log
      - catalina.*.log

  servers:
    app-01.example.test:
      timezone: America/New_York
      dir: /srv/app/logs

    app-02.example.test:
      dir: /var/log/tomcat
```

Server keys match the destination argument **exactly and case-sensitively**. An
unlisted server just uses `remote.defaults`.

## Route templates

The setting most worth understanding, because there is no default and its
absence is silent.

```yaml
defaults:
  route_templates:
    - /svc/v4/api/site/{site}
```

Matching rules:

- Literal segments must match exactly.
- `{name}` matches any one non-empty segment and is substituted into the
  resulting template.
- A template matches a **prefix**, so trailing segments are preserved:
  `/svc/v4/api/site/north/orders` → `/svc/v4/api/site/{site}/orders`.
- Templates are tried in order; the first match wins.
- `{site}` is special — its captured value is stored as the event's `site`,
  which drives site filtering and the report's site heatmap.
- If nothing matches, the path is used verbatim as its own route template.

Why it matters. The route template is part of the event signature, and failure
rates group by it. Without a template, every variable value becomes its own
route:

```text
with:     /svc/v4/api/site/{site}/orders    778 requests, 43 failures, 5.5%
without:  /svc/v4/api/site/site-a/orders    143 requests,  8 failures, 5.6%
          /svc/v4/api/site/site-b/orders    131 requests,  9 failures, 6.9%
          /svc/v4/api/site/site-c/orders    128 requests,  7 failures, 5.5%
          ...
```

Both are "correct"; only the first tells you the endpoint is failing. If your
report's Route impact section looks fragmented, this is the reason.

Because the signature includes the route template, **changing
`route_templates` changes signatures**. Re-ingest rather than mixing events
from before and after a change in one database.

## Timezones

`--timezone` means two different things depending on where it applies, and the
distinction matters.

**At ingest**, it is a correctness input. `jvm-multiline` and `catalina`
timestamps carry no UTC offset, so Clogs cannot normalize them without being
told the zone. Use the timezone of the **server that wrote the logs**. Getting
this wrong silently shifts every event.

**At report**, it is a display choice. Timestamps were already normalized to
UTC at ingest; this only decides how they are rendered.

Access logs carry an explicit numeric offset and need neither.

## Verifying

There is no `config validate` command yet. To check that a file parses, run any
command in that directory — strict decoding means a bad key fails immediately:

```sh
cd /path/to/workspace
clogs stats --db /dev/null
# a config error reports the file path and the offending key
```
