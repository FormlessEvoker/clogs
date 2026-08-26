# Development

## Setup

Go 1.23 or later is the only requirement. The SQLite driver is pure Go, so
there is no C toolchain, no system SQLite, and no `CGO_ENABLED=1`.

```sh
git clone https://github.com/FormlessEvoker/clogs
cd clogs
make build          # -> ./bin/clogs
make check          # everything CI runs
```

## Make targets

| Target | What it does |
| --- | --- |
| `build` | `CGO_ENABLED=0` build into `./bin/clogs`, with version injected |
| `test` | `go test ./...` |
| `test-race` | `go test -race ./...` |
| `vet` / `lint` | `go vet ./...` |
| `fmt` | `gofmt -w` across the tree |
| `fmt-check` | fails if anything is unformatted |
| `content-check` | the prohibited-content gate (see below) |
| **`check`** | **`fmt-check` + `lint` + `test` + `test-race` + `content-check`** |
| `cross-build` | compiles every release target, without packaging |
| `release-build` | builds and packages every release target into `./bin/dist` |
| `clean` | removes `./bin` |

`make check` is exactly what CI runs. If it passes locally it passes there.

Version metadata is injected at link time:

```sh
make build VERSION=v0.1.0
./bin/clogs version        # clogs v0.1.0
```

Releases are cut automatically from a label on the merged pull request. See
[releasing.md](releasing.md).

## Layout

```text
cmd/clogs/              entry point
internal/
  cli/                  command dispatch, flags, config wiring, path inference
  config/               clogs.yml discovery, strict decoding, precedence
  download/             OpenSSH-backed list/fetch, manifests
  ingest/               discovery, detection dispatch, transactional storage
  model/                the normalized Event
  parser/
    jvmmultiline/       JVM application logs
    catalina/           Tomcat Catalina logs
    access/             HTTP access logs, route templating
  report/               analysis, HTML, stats, CSV/NDJSON
  storage/              schema migrations, driver seam
  timewindow/           time expressions → absolute instants
scripts/                the prohibited-content gate
testdata/               invented fixtures
docs/                   this documentation
```

See [architecture.md](architecture.md) for why it is shaped this way.

## Testing

Tests are the actual behavior contract. Where documentation and a test
disagree about what Clogs does, the test is right.

```sh
go test ./internal/parser/...           # one area
go test ./internal/cli/ -run Server -v  # config and --server inference
go test -race ./...
```

Two things to know before writing tests in `internal/cli`:

**Config discovery walks up from the working directory.** A test that runs a
command while the repository's own directory is the cwd may find a `clogs.yml`
it did not intend. The `withNoConfigCWD(t)` helper in
`internal/cli/remote_test.go` chdirs to a temp directory and restores
afterward.

**Those tests cannot use `t.Parallel()`.** `os.Chdir` is process-global, so
tests that rely on the working directory must run sequentially. This is why
several tests in that package call `withNoConfigCWD(t)` where they would
otherwise call `t.Parallel()`.

Parser packages also have fuzz targets:

```sh
go test ./internal/parser/access/ -fuzz FuzzParse -fuzztime 30s
```

## Fixtures

Everything under `testdata/` is invented — no production data, no real
hostnames, only RFC 5737 documentation IP addresses.

- `testdata/formats/<format>/` — samples of each grammar, covering LF, CRLF,
  missing final newline, malformed records, and format-specific edge cases.
- `testdata/incidents/database-timeout/` — a three-file incident used by parser
  tests and by the quickstart in the README.

These were once emitted by a generator, which has been removed. They are plain
checked-in files now; edit them directly.

## The prohibited-content gate

`scripts/check-prohibited-content.sh` runs as part of `make check` and scans the
whole tree for:

- retired deployment identifiers
- credential-shaped values — a `password`, `secret`, `api_key`, or `token` key
  assigned a non-trivial value, and bearer or basic authorization headers
- external URLs outside the RFC 2606 example domains
- non-reserved IPv4 addresses

It is written against `grep` on purpose. An earlier version called `ripgrep` and
discarded stderr, so on any machine without `rg` every check reported "no match"
and the gate passed while inspecting nothing. It now fails loudly if its tooling
is missing rather than passing silently.

To check a subset:

```sh
./scripts/check-prohibited-content.sh testdata docs
```

If you add a term to the retired-identifier list, note that the script excludes
itself from its own scan — otherwise its pattern literal is a permanent match.

## Adding a log format

There is deliberately no parser registry — three formats and a `switch` do not
need one. To add a fourth:

1. Create `internal/parser/<name>/` exposing `Detect(prefix []byte) int` and
   `Parse(ctx, reader, Options, emit) (Result, error)`.
2. Add a family constant in `internal/model/event.go`.
3. Add the detection branch in `internal/ingest/ingest.go` and the parse branch
   in `internal/cli/cli.go`. Order matters — the first positive `Detect` wins.
4. Add fixtures under `testdata/formats/<name>/` covering LF, CRLF, a missing
   final newline, and malformed input.

If a fifth or sixth format makes that `switch` genuinely painful, that is the
signal to introduce a registry — not before.

## Conventions

- `gofmt` is enforced; `make fmt` before committing.
- Output is deterministic. Stable sorts with explicit tie-breaks, fixed column
  orders, no generation timestamps in results — tests assert on exact bytes.
- Files Clogs writes are `0600`, directories `0700`.
- Analysis language reports proximity, never causation.
