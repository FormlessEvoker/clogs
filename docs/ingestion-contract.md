# Ingestion Contract

This document records the current ingestion compatibility baseline. It does not
introduce a parser registry or storage schema changes.

## Discovery and selection

- A file input is ingested directly; a directory is walked recursively for
  regular files, then sorted lexically. The selected database and its WAL/SHM
  sidecars are excluded from directory discovery.
- Directory discovery does not follow file or directory symlink entries. A
  symlink supplied as the direct input is followed and uses the symlink's base
  name as its relative path.
- Supplying the selected database itself as the direct input fails before an
  ingest-run record is created.
- Detection uses the first 32 KiB in JVM, access, then Catalina order. Empty
  or unrecognized files are skipped with `unrecognized or empty content`; an
  input containing no supported file returns `no supported log files found`.
  If a prefix matches more than one detector, the first family in that order
  wins even when the other family's record appears earlier in the file.

## Persistence and duplicates

- Every recognized, nonduplicate file has one transaction containing its source
  file record, events, and details. Source provenance includes label, input
  path and relative path, SHA-256, size, mtime, detected family, parser version,
  and JVM timezone.
- Duplicate identity is exactly source label + relative path + content SHA-256.
  Such input is skipped. Changing any one component creates a distinct source
  occurrence: identical content under another source or relative path is
  ingested, as is changed content at the same source and relative path.
- Raw event text is stored only with `StoreRaw`; source lines and ordinals are
  always retained.

## Errors and run records

- Lenient mode commits valid parsed events and records JVM orphans/access
  malformed lines plus parser warnings. Strict mode rejects a file with either
  malformed input or warnings; its entire transaction rolls back.
- Cancellation observed by a parser is a per-file parse failure. The file
  transaction rolls back, the file result records `context canceled`, and the
  run is finalized through the normal failed-file summary.
- Per-file failures do not stop later files. A run with failures finishes
  `failed` with `<n> file(s) failed`, retaining successful files. An early
  return before normal finish records `failed` with `ingestion interrupted`.
- Each run records input, source, timezone, lifecycle status, completion time,
  and aggregate seen/ingested/skipped/event/malformed counts.

## Evidence

- Core families, raw retention, duplicate skip, lenient malformed handling, and
  strict rollback: `internal/ingest/ingest_test.go` existing family tests.
- Recursive deterministic discovery and DB-sidecar exclusion:
  `TestDiscoverRecursesSortsAndExcludesDatabaseFiles`.
- Partial failure, retained successes before and after a failure, and
  finished-run metadata:
  `TestRunTracksPartialFailureAndRunMetadata`.
- Full strict transaction rollback: `TestStrictRollbackRemovesAllFileRecords`.
- Parser cancellation, file rollback, and failed-run finalization:
  `TestRunCancellationDuringParsingRollsBackFile`.
- Direct database rejection before run creation:
  `TestRunRejectsDatabaseAsDirectInput`.
- Ambiguous detector precedence and persisted parser identity:
  `TestRunDetectorAmbiguityUsesJVMPrecedence`.
- Relative-path and content variations in the duplicate tuple:
  `TestRunDuplicateIdentityVariesByPathAndContent`; source-label variation:
  `TestRunStoresAndDeduplicatesSourceOccurrence`.
- Directory and direct-input symlink behavior:
  `TestDiscoverySkipsSymlinksButDirectSymlinkIsIngested`.
- Warning-only lenient commit and strict rollback:
  `TestRunWarningOnlyLenientAndStrictBehavior`.
