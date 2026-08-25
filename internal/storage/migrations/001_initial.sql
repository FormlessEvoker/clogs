CREATE TABLE ingest_runs (
 id INTEGER PRIMARY KEY, started_at TEXT NOT NULL, completed_at TEXT, input_path TEXT NOT NULL, source_label TEXT NOT NULL, timezone TEXT,
 files_seen INTEGER NOT NULL DEFAULT 0, files_ingested INTEGER NOT NULL DEFAULT 0, files_skipped INTEGER NOT NULL DEFAULT 0,
 events_ingested INTEGER NOT NULL DEFAULT 0, malformed_lines INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL, error_message TEXT
);
CREATE TABLE source_files (
 id INTEGER PRIMARY KEY, ingest_run_id INTEGER NOT NULL REFERENCES ingest_runs(id), source_label TEXT NOT NULL, path TEXT NOT NULL,
 relative_path TEXT NOT NULL, sha256 TEXT NOT NULL, size_bytes INTEGER NOT NULL, modified_at_ns INTEGER, detected_family TEXT NOT NULL,
 timezone TEXT, parser_version TEXT NOT NULL, ingested_at TEXT NOT NULL, event_count INTEGER NOT NULL DEFAULT 0,
 malformed_line_count INTEGER NOT NULL DEFAULT 0, UNIQUE(source_label, relative_path, sha256)
);
CREATE TABLE signatures (
 id INTEGER PRIMARY KEY, fingerprint TEXT NOT NULL UNIQUE, algorithm_version INTEGER NOT NULL, family TEXT NOT NULL, severity TEXT,
 namespace TEXT, operation TEXT, message_template TEXT, exception_class TEXT
);
CREATE TABLE events (
 id INTEGER PRIMARY KEY, source_file_id INTEGER NOT NULL REFERENCES source_files(id), signature_id INTEGER NOT NULL REFERENCES signatures(id),
 family TEXT NOT NULL, occurred_at_ns INTEGER NOT NULL, occurred_at_utc TEXT NOT NULL, original_timestamp TEXT NOT NULL,
 timestamp_precision TEXT NOT NULL, source_line_start INTEGER NOT NULL, source_line_end INTEGER NOT NULL, source_ordinal INTEGER NOT NULL,
 severity TEXT, message TEXT NOT NULL, raw_text TEXT, parse_warnings TEXT
);
CREATE TABLE java_details (
 event_id INTEGER PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE, logger TEXT, operation TEXT, thread TEXT, protocol_type TEXT,
 exception_class TEXT, exception_message TEXT, root_cause_class TEXT, root_cause_message TEXT, stack_trace TEXT
);
CREATE INDEX events_time_idx ON events(occurred_at_ns);
CREATE INDEX events_family_time_idx ON events(family, occurred_at_ns);
CREATE INDEX events_severity_time_idx ON events(severity, occurred_at_ns);
CREATE INDEX events_signature_time_idx ON events(signature_id, occurred_at_ns);
CREATE INDEX source_files_label_idx ON source_files(source_label);
CREATE INDEX java_exception_idx ON java_details(exception_class);
CREATE INDEX java_protocol_idx ON java_details(protocol_type);
