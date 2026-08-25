# Current Log Format Contract

This document records the current parser behavior as a compatibility baseline.
It is descriptive and preserves the current parser IDs, route normalization,
and signature rules until later roadmap work replaces them.

## Common conventions

- Detection examines lines in a prefix and returns `100` for a recognized
  format, otherwise `0`. The parse CLI tries JVM, Catalina, then access.
  Ingestion uses the distinct order JVM, access, then Catalina.
- `SourcePath`, inclusive source-line range, original timestamp, raw text, UTC
  occurrence time, precision, family, and a SHA-256 signature are emitted in a
  normalized event. CRLF is accepted where a parser removes a trailing `\r`.
- Each parser limits one physical line to 1 MiB. It observes cancellation at
  input-line loop boundaries and returns reader or emit-callback errors
  immediately.
- The format-specific sections identify current behavior; an item marked
  **missing test** is intentionally not claimed as independently
  characterized yet.

## JVM JVM multiline

### Record format and assembly

- A header is `Mon D, YYYY H:MM:SS AM|PM <context>`, for example
  `Aug 05, 2026 6:27:33 AM example.Logger work`.
- The timestamp is interpreted with the required IANA timezone and emitted in
  UTC with second precision. Lines before the first header increment
  `OrphanLines`. A later header flushes the current event; EOF also flushes it.
- Every non-header after a header is a continuation. The first continuation
  recognizes `UPPERCASE: message` as severity and message. Otherwise severity
  is empty and the event warning is `missing recognized LEVEL: message line`.
  Remaining continuation lines append to message and Java stack trace.
- The last whitespace-delimited context token is `operation`; prior tokens are
  `logger`. `protocol type: TOKEN` is extracted case-insensitively. The first
  Exception/Error is the exception class; the first `Caused by:` Exception/Error
  is the root cause. Raw text and source range retain the complete assembly.

### Signature

SHA-256 of NUL-separated family, severity, logger, operation, and
whitespace-normalized message.

### Evidence

- Multiline, orphan, EOF, timezone conversion, fields, protocol, exceptions,
  root cause, raw text: `internal/parser/jvm-multiline/jvm-multiline_test.go:TestParseMultilineEventAndEOF`.
- Missing level warning: `TestParseWarnsWhenLevelIsMissing`.
- Detection, including CRLF input: `TestDetect`; CRLF parsing and raw evidence:
  `TestParseAcceptsCRLF`.
- Cancellation and reader/emitter failure propagation:
  `TestParsePropagatesCancellationReaderAndEmitterErrors`.
- The physical-line limit: `TestParseRejectsOversizedLines`.
- Required timezone, malformed timestamps, and one-token or absent context:
  `TestParseTimestampAndContextBoundaries`.
- Exact signature inputs, whitespace normalization, and excluded source path:
  `TestSignatureUsesDocumentedInputs`.
- Parser fuzzing: `FuzzParse`.

## Catalina JVM multiline

### Record format and assembly

- A header is `DD-Mon-YYYY HH:MM:SS.mmm LEVEL [thread] logger message`, for
  example `05-Aug-2026 07:03:31.216 SEVERE [worker] example.Handler.process Failed`.
- The required IANA timezone produces UTC time with millisecond precision.
  Headers delimit records, EOF flushes the final record, and pre-header lines
  increment `OrphanLines`.
- Header fields map to severity, thread, logger, and message. Operation is the
  logger suffix after its final dot, or the entire logger when no dot exists.
  Continuations form Java stack trace only; they do not extend event message.
- The first Exception/Error supplies class and message. The first `Caused by:`
  Exception/Error supplies root-cause class and message. Raw text and source
  range cover the header and continuations.

### Signature

SHA-256 of NUL-separated family, severity, logger, whitespace-normalized
message, exception class, root-cause class, and whitespace-normalized
root-cause message.

### Evidence

- Consecutive multiline events, orphan handling, EOF, timezone conversion,
  precision, thread/operation, exception and root cause:
  `internal/parser/catalina/catalina_test.go:TestParseMultilineConsecutiveAndEOF`.
- Detection, CRLF parsing, normalized stack trace, and raw evidence:
  `TestDetectAndParseCRLF`.
- Cancellation and reader/emitter failure propagation:
  `TestParsePropagatesCancellationReaderAndEmitterErrors`.
- The physical-line limit: `TestParseRejectsOversizedLines`.
- Required timezone, malformed timestamps, logger-without-dot operation, and
  exact exception/root-cause message mapping:
  `TestParseTimestampOperationAndExceptionBoundaries`.
- Exact signature inputs, normalized whitespace, and excluded Java/provenance
  fields: `TestSignatureUsesDocumentedInputs`.
- Fixture coverage: `TestFixtureParses`; parser fuzzing: `FuzzParse`.

## HTTP access records

### Record format and assembly

- Each nonblank physical line is one record:
  `<client> [DD/Mon/YYYY:HH:MM:SS ±ZZZZ] port:<digits> "METHOD target HTTP/version" <three-digit-status> <bytes|->`.
- The numeric timestamp offset is authoritative; emitted time is UTC with
  second precision. A timezone option is not used. Blank lines are skipped.
- Client address, server port, method, raw target, HTTP version, status, and
  optional non-negative response bytes populate HTTP details. A valid request
  URI additionally populates path and query; URI parse failure leaves those
  derived fields empty while retaining the event.
- The current specialized route rule changes
  `/svc/v4/api/site/<nonempty>/...` to `/svc/v4/api/site/{site}/...` and
  sets `site`. Other paths are unchanged.
- A nonblank invalid line is recorded as `Malformed` with its line number and
  parsing continues with later valid lines. Raw text is the normalized line plus
  one newline; source range is that one line.

### Signature

SHA-256 of NUL-separated family, HTTP method, route template, and status. It
excludes client address, query string, response bytes, and timestamp.

### Evidence

- IPv4/IPv6, UTC conversion, bytes, query, and current route normalization:
  `internal/parser/access/access_test.go:TestParseExtractsAndNormalizesAccessEvent`.
- Signature exclusion and method change: `TestSignatureExcludesQueryAndClient`.
- Retained valid records plus malformed-line accounting:
  `TestParseRetainsValidLinesAndReportsMalformed`.
- Detection, blank lines, CRLF, invalid-URI fallback, and raw evidence:
  `TestDetectAndParseCRLFBlankAndInvalidURI`.
- Cancellation and reader/emitter failure propagation:
  `TestParsePropagatesCancellationReaderAndEmitterErrors`.
- The physical-line limit: `TestParseRejectsOversizedLines`.
- Invalid timestamp, port, request, status shape, and response-byte variants:
  `TestParseReportsMalformedFieldVariants`.
- Signature changes for method, normalized route, and status, plus exclusions
  for client, query, response bytes, timestamp, and source provenance:
  `TestSignatureExcludesQueryAndClient`.
- Fixture count: `TestIncidentFixtureHasNineRequestsAndSixServerErrors`;
  fuzzing: `FuzzParse`.

## Cross-format result handling

JVM and Catalina represent pre-header material as orphan lines; access
represents nonblank invalid records as malformed lines. JVM can emit parse
warnings for a missing first severity line; Catalina currently emits neither
warnings nor malformed-record entries. In ingestion, Java orphans and access
malformed lines are tracked as malformed input, and strict mode fails a file on
malformed input or warnings. See `internal/ingest/ingest.go:ingestFile` and its
tests for that integration behavior.
