package storage

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestOpenAppliesMigrationsAndCanReopen(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "clogs.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	firstMigrations := migrationRows(t, db)
	for _, table := range []string{"schema_migrations", "ingest_runs", "source_files", "signatures", "events", "java_details", "http_details"} {
		var name string
		if err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name); err != nil {
			db.Close()
			t.Fatalf("table %q: %v", table, err)
		}
	}
	var foreignKeys int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		db.Close()
		t.Fatalf("foreign_keys=%d err=%v", foreignKeys, err)
	}
	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil || journalMode != "wal" {
		db.Close()
		t.Fatalf("journal_mode=%q err=%v", journalMode, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	secondMigrations := migrationRows(t, db)
	if !reflect.DeepEqual(secondMigrations, firstMigrations) {
		db.Close()
		t.Fatalf("migrations after reopen=%#v want %#v", secondMigrations, firstMigrations)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("database mode=%#o want 0600", info.Mode().Perm())
	}
}

func TestSchemaRelationshipsAndOpenCreateErrors(t *testing.T) {
	t.Parallel()

	db, err := Open(filepath.Join(t.TempDir(), "clogs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	for _, relationship := range []struct {
		table     string
		reference string
		onDelete  string
	}{
		{"source_files", "ingest_runs", "NO ACTION"},
		{"events", "source_files", "NO ACTION"},
		{"events", "signatures", "NO ACTION"},
		{"java_details", "events", "CASCADE"},
		{"http_details", "events", "CASCADE"},
	} {
		assertForeignKey(t, db, relationship.table, relationship.reference, relationship.onDelete)
	}
	assertSchemaIntegrityBehavior(t, db)

	_, err = Open(filepath.Join(t.TempDir(), "missing", "clogs.db"))
	if err == nil || !strings.Contains(err.Error(), "create database") {
		t.Fatalf("Open missing parent error=%v", err)
	}
}

type migration struct {
	Version   int
	AppliedAt string
}

func migrationRows(t *testing.T, db *sql.DB) []migration {
	t.Helper()
	rows, err := db.Query("SELECT version,applied_at FROM schema_migrations ORDER BY version")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var migrations []migration
	for rows.Next() {
		var migration migration
		if err := rows.Scan(&migration.Version, &migration.AppliedAt); err != nil {
			t.Fatal(err)
		}
		if migration.AppliedAt == "" {
			t.Fatalf("migration %d has empty applied_at", migration.Version)
		}
		migrations = append(migrations, migration)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(migrations) == 0 {
		t.Fatal("no migrations applied")
	}
	return migrations
}

func assertSchemaIntegrityBehavior(t *testing.T, db *sql.DB) {
	t.Helper()
	result, err := db.Exec(`INSERT INTO ingest_runs(started_at,input_path,source_label,status) VALUES(?,?,?,?)`, "start", "input", "source", "complete")
	if err != nil {
		t.Fatal(err)
	}
	runID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO source_files(ingest_run_id,source_label,path,relative_path,sha256,size_bytes,detected_family,parser_version,ingested_at) VALUES(?,?,?,?,?,?,?,?,?)`, runID, "source", "path", "path", "hash", 1, "family", "version", "now"); err != nil {
		t.Fatal(err)
	}
	var sourceID int64
	if err := db.QueryRow("SELECT id FROM source_files").Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO source_files(ingest_run_id,source_label,path,relative_path,sha256,size_bytes,detected_family,parser_version,ingested_at) VALUES(?,?,?,?,?,?,?,?,?)`, runID, "source", "another-path", "path", "hash", 1, "family", "version", "now"); err == nil {
		t.Fatal("duplicate source identity insert succeeded")
	}
	if _, err := db.Exec(`INSERT INTO source_files(ingest_run_id,source_label,path,relative_path,sha256,size_bytes,detected_family,parser_version,ingested_at) VALUES(?,?,?,?,?,?,?,?,?)`, 999, "other", "other", "other", "other", 1, "family", "version", "now"); err == nil {
		t.Fatal("invalid source-file parent insert succeeded")
	}
	result, err = db.Exec(`INSERT INTO signatures(fingerprint,algorithm_version,family) VALUES(?,?,?)`, "signature", 1, "family")
	if err != nil {
		t.Fatal(err)
	}
	signatureID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO events(source_file_id,signature_id,family,occurred_at_ns,occurred_at_utc,original_timestamp,timestamp_precision,source_line_start,source_line_end,source_ordinal,message) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, 999, signatureID, "family", 1, "now", "now", "second", 1, 1, 1, "invalid"); err == nil {
		t.Fatal("invalid event parent insert succeeded")
	}
	result, err = db.Exec(`INSERT INTO events(source_file_id,signature_id,family,occurred_at_ns,occurred_at_utc,original_timestamp,timestamp_precision,source_line_start,source_line_end,source_ordinal,message) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, sourceID, signatureID, "family", 1, "now", "now", "second", 1, 1, 1, "event")
	if err != nil {
		t.Fatal(err)
	}
	eventID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO java_details(event_id,logger) VALUES(?,?)`, eventID, "logger"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO http_details(event_id,method) VALUES(?,?)`, eventID, "GET"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DELETE FROM source_files WHERE id=?", sourceID); err == nil {
		t.Fatal("source-file parent deletion succeeded while event exists")
	}
	if _, err := db.Exec("DELETE FROM events WHERE id=?", eventID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"java_details", "http_details"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s count=%d err=%v after event delete", table, count, err)
		}
	}
	if _, err := db.Exec("DELETE FROM ingest_runs WHERE id=?", runID); err == nil {
		t.Fatal("ingest-run parent deletion succeeded while source file exists")
	}
}

func assertForeignKey(t *testing.T, db interface {
	Query(string, ...any) (*sql.Rows, error)
}, table, reference, onDelete string) {
	t.Helper()
	rows, err := db.Query("PRAGMA foreign_key_list(" + table + ")")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, sequence int
		var foundReference, from, to, onUpdate, foundDelete, match string
		if err := rows.Scan(&id, &sequence, &foundReference, &from, &to, &onUpdate, &foundDelete, &match); err != nil {
			t.Fatal(err)
		}
		if foundReference == reference && foundDelete == onDelete {
			return
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	t.Fatalf("%s has no foreign key to %s with ON DELETE %s", table, reference, onDelete)
}
