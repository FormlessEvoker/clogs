package ingest

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FormlessEvoker/clogs/internal/storage"
)

const fixture = "Aug 05, 2026 6:27:33 AM example.Logger work\nINFO: complete\n"

type cancelOnSecondErrContext struct {
	context.Context
	calls int
}

func (ctx *cancelOnSecondErrContext) Err() error {
	ctx.calls++
	if ctx.calls >= 2 {
		return context.Canceled
	}
	return nil
}

func TestRunStoresAndDeduplicatesSourceOccurrence(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "application.log")
	if err := os.WriteFile(path, []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "clogs.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	options := Options{Input: path, Database: dbPath, Source: "one", Timezone: "America/Chicago", StoreRaw: true}
	summary, err := Run(context.Background(), db, options)
	if err != nil {
		t.Fatal(err)
	}
	if summary.FilesIngested != 1 || summary.Events != 1 {
		t.Fatalf("summary = %#v", summary)
	}
	assertCount(t, db, "SELECT COUNT(*) FROM events", 1)
	assertCount(t, db, "SELECT COUNT(*) FROM java_details", 1)
	var raw string
	if err := db.QueryRow("SELECT raw_text FROM events").Scan(&raw); err != nil || raw == "" {
		t.Fatalf("raw = %q, err = %v", raw, err)
	}
	summary, err = Run(context.Background(), db, options)
	if err != nil {
		t.Fatal(err)
	}
	if summary.FilesSkipped != 1 || summary.Events != 0 {
		t.Fatalf("dedup summary = %#v", summary)
	}
	assertCount(t, db, "SELECT COUNT(*) FROM events", 1)
	options.Source = "two"
	summary, err = Run(context.Background(), db, options)
	if err != nil {
		t.Fatal(err)
	}
	if summary.FilesIngested != 1 {
		t.Fatalf("source separation summary = %#v", summary)
	}
	assertCount(t, db, "SELECT COUNT(*) FROM events", 2)
}

func TestRunStoreRawFalseAndStrictRollback(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "application.log")
	if err := os.WriteFile(path, []byte("orphan\n"+fixture), 0600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "clogs.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	options := Options{Input: path, Database: dbPath, Source: "local", Timezone: "America/Chicago", StoreRaw: false}
	if _, err := Run(context.Background(), db, options); err != nil {
		t.Fatal(err)
	}
	var raw sql.NullString
	if err := db.QueryRow("SELECT raw_text FROM events").Scan(&raw); err != nil || raw.Valid {
		t.Fatalf("raw = %#v, err = %v", raw, err)
	}
	strictPath := filepath.Join(dir, "strict.db")
	db2, err := storage.Open(strictPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	options.Database = strictPath
	options.Strict = true
	if _, err := Run(context.Background(), db2, options); err == nil {
		t.Fatal("strict Run() error = nil")
	}
	assertCount(t, db2, "SELECT COUNT(*) FROM events", 0)
}

func TestRunIngestsAccessWithMalformedLineAndStrictRollback(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "access.log")
	valid := "10.0.0.1 [05/Aug/2026:07:03:31 -0500] port:1 \"GET /svc/v4/api/site/a/x?q=one HTTP/1.1\" 500 -\n"
	if err := os.WriteFile(path, []byte("bad line\n"+valid), 0600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "clogs.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	options := Options{Input: path, Database: dbPath, Source: "local", StoreRaw: true}
	summary, err := Run(context.Background(), db, options)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Events != 1 || summary.Malformed != 1 || summary.Families["access"] != 1 {
		t.Fatalf("summary=%#v", summary)
	}
	assertCount(t, db, "SELECT COUNT(*) FROM http_details", 1)
	strictPath := filepath.Join(dir, "strict.db")
	db2, err := storage.Open(strictPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	options.Database, options.Strict = strictPath, true
	if _, err := Run(context.Background(), db2, options); err == nil {
		t.Fatal("strict error = nil")
	}
	assertCount(t, db2, "SELECT COUNT(*) FROM events", 0)
}

func TestRunIngestsAllThreeFamilies(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	files := map[string]string{
		"application.log": "Aug 05, 2026 7:03:30 AM example.Logger work\nINFO: complete\n",
		"access.log":      "10.0.0.1 [05/Aug/2026:07:03:31 -0500] port:1 \"GET /ok HTTP/1.1\" 500 0\n",
		"catalina.log":    "05-Aug-2026 07:03:31.216 SEVERE [worker] example.Logger.work failed\n\tjava.lang.OutOfMemoryError: heap\n",
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	dbPath := filepath.Join(t.TempDir(), "clogs.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	summary, err := Run(context.Background(), db, Options{Input: dir, Database: dbPath, Source: "local", Timezone: "America/Chicago", StoreRaw: true})
	if err != nil || summary.FilesIngested != 3 || summary.Families["jvm-multiline"] != 1 || summary.Families["access"] != 1 || summary.Families["catalina"] != 1 {
		t.Fatalf("summary=%#v err=%v", summary, err)
	}
	assertCount(t, db, "SELECT COUNT(*) FROM events", 3)
	var thread string
	if err := db.QueryRow("SELECT thread FROM java_details WHERE thread = 'worker'").Scan(&thread); err != nil || thread != "worker" {
		t.Fatalf("thread=%q err=%v", thread, err)
	}
}

func TestDiscoverRecursesSortsAndExcludesDatabaseFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for name := range map[string]string{"z.log": fixture, "nested/a.log": fixture, "clogs.db": "not a log", "clogs.db-wal": "", "clogs.db-shm": ""} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(fixture), 0600); err != nil {
			t.Fatal(err)
		}
	}
	files, root, err := discover(dir, filepath.Join(dir, "clogs.db"))
	if err != nil {
		t.Fatal(err)
	}
	if root != dir || strings.Join(files, ",") != strings.Join([]string{filepath.Join(dir, "nested", "a.log"), filepath.Join(dir, "z.log")}, ",") {
		t.Errorf("files=%q root=%q", files, root)
	}
}

func TestRunSkipsEmptyAndUnrecognizedInput(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for name, contents := range map[string]string{"empty.log": "", "unknown.log": "not a supported record\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	dbPath := filepath.Join(t.TempDir(), "clogs.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	summary, err := Run(context.Background(), db, Options{Input: dir, Database: dbPath, Source: "local"})
	if err == nil || !strings.Contains(err.Error(), "no supported log files found") || summary.FilesSeen != 2 || summary.FilesSkipped != 2 {
		t.Fatalf("summary=%#v err=%v", summary, err)
	}
	for _, file := range summary.Files {
		if file.Status != "skipped" || file.Reason != "unrecognized or empty content" {
			t.Errorf("file result=%#v", file)
		}
	}
}

func TestRunDirectFileUsesBaseRelativePathAndDetectsFamily(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "access.log")
	if err := os.WriteFile(path, []byte("10.0.0.1 [05/Aug/2026:07:03:31 -0500] port:1 \"GET /ok HTTP/1.1\" 200 0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "clogs.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	summary, err := Run(context.Background(), db, Options{Input: path, Database: dbPath, Source: "local"})
	if err != nil || summary.FilesIngested != 1 || len(summary.Files) != 1 || summary.Files[0].Path != "access.log" || summary.Files[0].Family != "access" {
		t.Fatalf("summary=%#v err=%v", summary, err)
	}
}

func TestRunTracksPartialFailureAndRunMetadata(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "access.log"), []byte("10.0.0.1 [05/Aug/2026:07:03:31 -0500] port:1 \"GET /ok HTTP/1.1\" 200 0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "application.log"), []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "z-access.log"), []byte("10.0.0.1 [05/Aug/2026:07:03:32 -0500] port:1 \"GET /later HTTP/1.1\" 200 0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "clogs.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	summary, err := Run(context.Background(), db, Options{Input: dir, Database: dbPath, Source: "local", StoreRaw: true})
	if err == nil || summary.FilesIngested != 2 || summary.FilesFailed != 1 || summary.Events != 2 {
		t.Fatalf("summary=%#v err=%v", summary, err)
	}
	assertCount(t, db, "SELECT COUNT(*) FROM events", 2)
	var status, message string
	var completed sql.NullString
	if err := db.QueryRow("SELECT status,error_message,completed_at FROM ingest_runs ORDER BY id DESC LIMIT 1").Scan(&status, &message, &completed); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || message != "1 file(s) failed" || !completed.Valid {
		t.Errorf("run status=%q message=%q completed=%#v", status, message, completed)
	}
}

func TestStrictRollbackRemovesAllFileRecords(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "application.log")
	if err := os.WriteFile(path, []byte("orphan\n"+fixture), 0600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "clogs.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := Run(context.Background(), db, Options{Input: path, Database: dbPath, Source: "local", Timezone: "America/Chicago", Strict: true}); err == nil {
		t.Fatal("strict Run() error = nil")
	}
	assertCount(t, db, "SELECT COUNT(*) FROM source_files", 0)
	assertCount(t, db, "SELECT COUNT(*) FROM events", 0)
	assertCount(t, db, "SELECT COUNT(*) FROM java_details", 0)
}

func TestRunCancellationDuringParsingRollsBackFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "access.log")
	input := "10.0.0.1 [05/Aug/2026:07:03:31 -0500] port:1 \"GET /first HTTP/1.1\" 200 0\n" +
		"10.0.0.1 [05/Aug/2026:07:03:32 -0500] port:1 \"GET /second HTTP/1.1\" 200 0\n"
	if err := os.WriteFile(path, []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "clogs.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := &cancelOnSecondErrContext{Context: context.Background()}
	summary, err := Run(ctx, db, Options{Input: path, Database: dbPath, Source: "local"})
	if err == nil || summary.FilesFailed != 1 || summary.Events != 0 || len(summary.Files) != 1 || !strings.Contains(summary.Files[0].Reason, "context canceled") {
		t.Fatalf("summary=%#v err=%v", summary, err)
	}
	assertCount(t, db, "SELECT COUNT(*) FROM source_files", 0)
	assertCount(t, db, "SELECT COUNT(*) FROM events", 0)
	var status, message string
	if err := db.QueryRow("SELECT status,error_message FROM ingest_runs ORDER BY id DESC LIMIT 1").Scan(&status, &message); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || message != "1 file(s) failed" {
		t.Fatalf("status=%q message=%q", status, message)
	}
}

func TestRunRejectsDatabaseAsDirectInput(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "clogs.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	summary, err := Run(context.Background(), db, Options{Input: dbPath, Database: dbPath, Source: "local"})
	if err == nil || !strings.Contains(err.Error(), "database cannot be an ingestion input") || summary.FilesSeen != 0 {
		t.Fatalf("summary=%#v err=%v", summary, err)
	}
	assertCount(t, db, "SELECT COUNT(*) FROM ingest_runs", 0)
}

func TestRunDetectorAmbiguityUsesJVMPrecedence(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "ambiguous.log")
	input := "10.0.0.1 [05/Aug/2026:07:03:31 -0500] port:1 \"GET /first HTTP/1.1\" 200 0\n" + fixture
	if err := os.WriteFile(path, []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "clogs.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	summary, err := Run(context.Background(), db, Options{Input: path, Database: dbPath, Source: "local", Timezone: "America/Chicago"})
	if err != nil || summary.FilesIngested != 1 || summary.Events != 1 || summary.Malformed != 1 || summary.Families["jvm-multiline"] != 1 || summary.Files[0].Family != "jvm-multiline" {
		t.Fatalf("summary=%#v err=%v", summary, err)
	}
	var family, version string
	if err := db.QueryRow("SELECT detected_family,parser_version FROM source_files").Scan(&family, &version); err != nil {
		t.Fatal(err)
	}
	if family != "jvm-multiline" || version != "jvm-multiline-v1" {
		t.Fatalf("family=%q version=%q", family, version)
	}
}

func TestRunDuplicateIdentityVariesByPathAndContent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	firstPath := filepath.Join(dir, "a.log")
	secondPath := filepath.Join(dir, "b.log")
	for _, path := range []string{firstPath, secondPath} {
		if err := os.WriteFile(path, []byte(fixture), 0600); err != nil {
			t.Fatal(err)
		}
	}
	dbPath := filepath.Join(t.TempDir(), "clogs.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	options := Options{Input: dir, Database: dbPath, Source: "local", Timezone: "America/Chicago"}
	summary, err := Run(context.Background(), db, options)
	if err != nil || summary.FilesIngested != 2 || summary.Events != 2 {
		t.Fatalf("path-variant summary=%#v err=%v", summary, err)
	}
	if err := os.WriteFile(firstPath, []byte("Aug 05, 2026 6:27:34 AM example.Logger work\nINFO: changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	summary, err = Run(context.Background(), db, options)
	if err != nil || summary.FilesIngested != 1 || summary.FilesSkipped != 1 || summary.Events != 1 {
		t.Fatalf("content-variant summary=%#v err=%v", summary, err)
	}
	assertCount(t, db, "SELECT COUNT(*) FROM source_files", 3)
	assertCount(t, db, "SELECT COUNT(*) FROM events", 3)
}

func TestDiscoverySkipsSymlinksButDirectSymlinkIsIngested(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "target.log")
	if err := os.WriteFile(target, []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	scanDir := filepath.Join(root, "scan")
	if err := os.Mkdir(scanDir, 0700); err != nil {
		t.Fatal(err)
	}
	fileLink := filepath.Join(scanDir, "linked.log")
	if err := os.Symlink(target, fileLink); err != nil {
		t.Skipf("create file symlink: %v", err)
	}
	directoryTarget := filepath.Join(root, "directory-target")
	if err := os.Mkdir(directoryTarget, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directoryTarget, "nested.log"), []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(directoryTarget, filepath.Join(scanDir, "linked-directory")); err != nil {
		t.Skipf("create directory symlink: %v", err)
	}
	files, _, err := discover(scanDir, filepath.Join(root, "clogs.db"))
	if err != nil || len(files) != 0 {
		t.Fatalf("files=%q err=%v", files, err)
	}

	dbPath := filepath.Join(t.TempDir(), "clogs.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	summary, err := Run(context.Background(), db, Options{Input: fileLink, Database: dbPath, Source: "local", Timezone: "America/Chicago"})
	if err != nil || summary.FilesIngested != 1 || len(summary.Files) != 1 || summary.Files[0].Path != "linked.log" {
		t.Fatalf("summary=%#v err=%v", summary, err)
	}
}

func TestRunWarningOnlyLenientAndStrictBehavior(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "warning.log")
	input := "Aug 05, 2026 6:27:33 AM example.Logger work\nmessage without level\n"
	if err := os.WriteFile(path, []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	lenientPath := filepath.Join(t.TempDir(), "lenient.db")
	lenient, err := storage.Open(lenientPath)
	if err != nil {
		t.Fatal(err)
	}
	defer lenient.Close()
	options := Options{Input: path, Database: lenientPath, Source: "local", Timezone: "America/Chicago"}
	summary, err := Run(context.Background(), lenient, options)
	if err != nil || summary.FilesIngested != 1 || summary.Events != 1 || summary.Malformed != 0 || summary.Warnings != 1 {
		t.Fatalf("lenient summary=%#v err=%v", summary, err)
	}
	assertCount(t, lenient, "SELECT COUNT(*) FROM events", 1)

	strictPath := filepath.Join(t.TempDir(), "strict.db")
	strict, err := storage.Open(strictPath)
	if err != nil {
		t.Fatal(err)
	}
	defer strict.Close()
	options.Database, options.Strict = strictPath, true
	summary, err = Run(context.Background(), strict, options)
	if err == nil || summary.FilesFailed != 1 || summary.Malformed != 0 || summary.Warnings != 1 || !strings.Contains(summary.Files[0].Reason, "0 malformed line(s), 1 warning(s)") {
		t.Fatalf("strict summary=%#v err=%v", summary, err)
	}
	assertCount(t, strict, "SELECT COUNT(*) FROM source_files", 0)
	assertCount(t, strict, "SELECT COUNT(*) FROM events", 0)
}

func assertCount(t *testing.T, db *sql.DB, query string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(query).Scan(&got); err != nil || got != want {
		t.Fatalf("%s = %d, %v; want %d", query, got, err, want)
	}
}
