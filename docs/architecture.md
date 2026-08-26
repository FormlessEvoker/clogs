# Architecture

## Origins

Clogs was written to answer one recurring question during Tomcat incidents:
*what was happening across all three logs at the moment things broke?*

The deployment it came from ran a JVM application behind Tomcat, writing three
log files that each told part of the story and none of it completely:

- the application log, multi-line, no timezone on its timestamps
- the Catalina log, millisecond precision, also no timezone
- an HTTP access log, its own numeric UTC offset

Answering the question meant SSHing to a server, pulling three files, and
reading them side by side in a terminal while mentally converting timestamps.
That works once. It does not work at 2am, across several servers, when the
interesting window is ninety seconds wide and buried in ten megabytes.

The first version was hard-coded to that deployment — its route shapes, its
filenames, its product names. What exists now is the same pipeline with every
deployment-specific value moved into configuration, which is why
[`route_templates`](configuration.md#route-templates) exists and why nothing in
the source names a particular application.

## Pipeline

```text
remote or local files
        ↓
  acquisition            internal/download    OpenSSH sftp, manifest, hashes
        ↓
  detection              internal/parser/*    by content, not filename
        ↓
  parsing                                     3 families → one event model
        ↓
  normalization                               all timestamps → UTC
        ↓
  storage                internal/storage     SQLite, provenance, dedup
        ↓
  analysis               internal/report      merged timeline, rates, onsets
        ↓
  presentation                                text, JSON, CSV, NDJSON, HTML
```

Each stage is usable on its own. `parse` stops after normalization, `ingest`
stops after storage, `query` and `report incident` share one analysis
implementation and differ only in rendering.

## Packages

| Package | Lines | Responsibility |
| --- | ---: | --- |
| `internal/cli` | 1446 | Command dispatch, flag parsing, config wiring, path inference |
| `internal/report` | 1321 | Analysis, HTML rendering, stats, CSV/NDJSON export |
| `internal/download` | 426 | OpenSSH-backed listing and fetching, manifests |
| `internal/ingest` | 381 | Discovery, detection dispatch, transactional storage |
| `internal/config` | 214 | `clogs.yml` discovery, strict decoding, precedence merge |
| `internal/parser/jvmmultiline` | 182 | JVM application log grammar |
| `internal/parser/access` | 179 | HTTP access log grammar, route templating |
| `internal/timewindow` | 176 | Ergonomic time expressions → absolute instants |
| `internal/parser/catalina` | 149 | Catalina log grammar |
| `internal/storage` | 84 | Schema migrations, driver seam |
| `internal/model` | 64 | The normalized `Event` |
| `cmd/clogs` | 16 | Entry point |

## Design decisions

### OpenSSH does authentication; Clogs never sees credentials

`remote` shells out to your `ssh` and `sftp` binaries rather than linking a Go
SSH library. Keys, agents, `known_hosts`, aliases, ProxyJump, and
keyboard-interactive prompts all work because they are genuinely OpenSSH doing
them. Clogs cannot accept, store, or leak a credential it never handles.

The cost is a process boundary and dependence on the local OpenSSH
configuration. The benefit is that the security-sensitive part of the tool is
code nobody has to trust us to have written correctly. One control connection
is opened per command and reused, so a password prompt happens at most once.

### Detection by content, not filename

Filenames are deployment conventions and they vary. Each parser exposes a
`Detect(prefix) int` returning a confidence score; ingestion tries JVM, then
Catalina, then access, and takes the first positive. A file nothing recognizes
is skipped rather than failed, so pointing `ingest` at a directory of mixed
content does something sensible.

### Normalize to UTC at ingest, format at display

Two of the three formats carry no timezone, so the zone has to come from
outside the file. Clogs takes it once, at ingest, and stores UTC. Everything
downstream — ordering, windowing, correlation — operates on a single timeline
with no per-record timezone logic.

The consequence is worth stating plainly: `--timezone` at ingest is a
correctness input, and getting it wrong silently shifts every event. The same
flag at report time is only a display choice.

### Configuration is data

`clogs.yml` holds patterns, paths, timezones, route shapes, and analysis
defaults. It cannot execute anything and holds no secrets. Decoding is strict —
unknown keys are an error — so a typo fails at startup rather than silently
selecting a default.

Route normalization is the clearest example of the boundary. It used to be a
compiled-in conditional matching one specific URL shape. It is now a list of
templates in configuration, which is what lets the tool be published without
naming anyone's endpoints.

### Deterministic output

Given the same database and parameters, every output is byte-identical: stable
sort orders with explicit tie-breaks, fixed CSV columns, no timestamps of
generation embedded in results. Reports are diffable and tests can assert on
exact bytes.

### Correlation, never causation

The analysis reports what happened near what, with the window and sample size
stated alongside. It does not rank causes or assert that one event produced
another. Log proximity is evidence for a human, and phrasing it as more than
that in an incident is actively harmful.

### Evidence is preserved

Raw text, original timestamp strings, source file paths, and line ranges travel
with every event, and reports link back to them. An analysis you cannot check
against the original line is not much use during an incident. Raw storage can
be disabled with `--store-raw=false` when size matters.

### Lenient by default, strict on request

Real logs contain truncated lines, interleaved writes, and records from
versions that no longer exist. Ingestion retains valid events and reports
malformed ones with line numbers. `--strict` rolls back an entire file instead,
for when you need to trust the corpus completely.

### SQLite as the working set

An incident window gets queried repeatedly with different parameters. Parsing
ten megabytes once and querying it many times beats re-parsing per question.
SQLite means no server, an inspectable artifact, and a file you can hand to
someone. The driver is pure Go, so builds work with `CGO_ENABLED=0`.

Schema changes go through numbered migrations in
`internal/storage/migrations/`, recorded in the database.

### Deduplication by content

An event is identified for dedup by source label, relative path, and content
hash — so re-ingesting an overlapping collection is safe and idempotent, while
the same file from two different servers stays two distinct sources.

### Signatures

A signature is a SHA-256 over family, HTTP method, route template, and status —
deliberately excluding client address, query string, response bytes, and
timestamp. That makes "the same call failing repeatedly" a groupable thing.

Because the route template is an input, changing `route_templates` changes
signatures. Re-ingest rather than mixing pre- and post-change events in one
database.

## Deliberately not built

Recorded because they were all considered and rejected as premature for a tool
with one operator:

- A parser registry and plugin contract. Three formats and a `switch` do not
  need indirection; add it when a sixth format makes the switch hurt.
- A processor pipeline abstraction over route templating.
- Signature and schema versioning with dual-format export. There are no
  external consumers to stay compatible with.
- Workspace run-state, resumable workflows, and a `status --run` command.
- A generic comparison engine (`clogs compare`) over arbitrary windows.

An earlier plan specified all of these across 85 tasks. It was abandoned in
favor of the configuration work that actually shipped.
