# Storage and Export Contract

This document records current logical persistence and export behavior. It does
not prescribe the future generalized schema.

## Storage model

- Embedded migrations create `schema_migrations`, `ingest_runs`, `source_files`,
  `signatures`, `events`, `java_details`, and `http_details`.

| Table | Current columns / identity | Relationships |
| --- | --- | --- |
| `schema_migrations` | `version` primary key, `applied_at` | Records each embedded migration after it succeeds. |
| `ingest_runs` | primary key; start/completion, input/source/timezone, aggregate counts, status, error | Parent of `source_files`. |
| `source_files` | primary key; run, source/path/relative path/hash, size and modification time, detected family/parser/timezone, ingest time and counts | `(source_label, relative_path, sha256)` is globally unique; `ingest_run_id` references `ingest_runs`. |
| `signatures` | primary key; unique fingerprint plus algorithm, family, severity, namespace, operation, template, exception | Parent of `events`. |
| `events` | primary key; source file/signature, family, normalized/original timestamp and precision, line range/ordinal, severity, message, optional raw text and warnings | References `source_files` and `signatures`. |
| `java_details` | `event_id` primary key; logger, operation, thread, protocol, exception/root-cause fields, stack trace | Optional one-to-one extension of `events`; deletes with its event. |
| `http_details` | `event_id` primary key; client/server, request, route/site, HTTP version, status and bytes | Optional one-to-one extension of `events`; deletes with its event. |

- The source-file and event foreign keys use the current SQLite default
  `ON DELETE NO ACTION`; only Java and HTTP detail rows use `ON DELETE CASCADE`.
  SQLite rejects duplicate source-file identities and child rows whose required
  parent does not exist. Deleting an event removes its detail rows, while
  deleting a source file with events or a run with source files is rejected.
- Opening a database enables foreign keys and WAL, applies unapplied embedded
  migrations transactionally, and can be repeated without reapplying them.
  Reopening preserves the exact `(version, applied_at)` migration records.
  A newly created database file is opened exclusively with mode `0600`; failure
  to create it (for example, when its parent is missing) is returned as a
  `create database` error.

## Exports

- `Events` orders rows by event nanoseconds, source label, relative path, source
  line start, then event ID. This resolves equal timestamps, equal source paths,
  and equal source lines deterministically. A nonempty source filter restricts
  the complete result while retaining source label and relative-path provenance.
- NDJSON emits one `Event` object per ordered row. Every record contains exactly
  these required keys: `source_label`, `source_path`, `family`, `occurred_at`,
  `original_timestamp`, `precision`, `source_line_start`, `source_line_end`,
  `message`, and `signature`. Empty optional strings and nil optional numbers
  are omitted; parse warnings appear as a JSON array only when stored and nonempty.
  Raw text appears only when requested and present in storage; populated Java
  and HTTP detail fields are included.
- CSV always emits these 33 columns, followed by the same ordered rows:
  `source_label`, `source_path`, `family`, `occurred_at`, `original_timestamp`,
  `precision`, `source_line_start`, `source_line_end`, `severity`, `message`,
  `signature`, `raw_text`, `parse_warnings`, `logger`, `operation`, `thread`,
  `protocol_type`, `exception_class`, `exception_message`, `root_cause_class`,
  `root_cause_message`, `stack_trace`, `client_address`, `server_port`,
  `method`, `raw_target`, `path`, `raw_query`, `route_template`, `site`,
  `http_version`, `status`, `response_bytes`.
  The standard CSV writer quotes commas, quotes, and newlines; warnings join as
  ` | `. Missing optional values are empty cells, including raw text when it is
  omitted. NDJSON and CSV propagate output-writer errors.

## Evidence

- Migration application, stable migration records after reopening, WAL/foreign-key setup, and creation mode:
  `internal/storage/storage_test.go:TestOpenAppliesMigrationsAndCanReopen`.
- Table relationships, identity/FK enforcement, cascade/no-action deletion behavior, and a failed missing-parent create:
  `TestSchemaRelationshipsAndOpenCreateErrors`.
- Deterministic baseline export/provenance: `TestExportsAreDeterministicAndCarryProvenance`.
- Ordering ties, source filtering, and raw inclusion: `TestEventsOrderTiesAndFilterBySource`.
- NDJSON field presence, warning-array behavior, optional-field omission, and
  raw control: `TestNDJSONFieldPresenceAndRawControl`.
- Exact CSV header/order, raw column, comma/quote/newline escaping:
  `TestCSVExportUsesFixedColumnsAndEscapesFields`.
- CSV primitive quoting round-trip: `TestCSVWriterQuotesStructuredFields`.
- Export output-writer errors: `TestExportPropagatesOutputWriterErrors`.
- Source-filtered logical counts: `TestStatsSupportsSourceFilter`.
