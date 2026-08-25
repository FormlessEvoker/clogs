package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadNearestPrefersCurrentDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	child := filepath.Join(root, "nested")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "clogs.yml"), []byte("defaults:\n  timezone: UTC\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, "clogs.yml"), []byte("defaults:\n  timezone: America/Chicago\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, path, err := LoadNearest(child)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(child, "clogs.yml") {
		t.Fatalf("path = %q", path)
	}
	if cfg.Defaults.Timezone != "America/Chicago" {
		t.Fatalf("timezone = %q", cfg.Defaults.Timezone)
	}
}

func TestLoadNearestFallsBackToParentDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	child := filepath.Join(root, "nested")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "clogs.yml"), []byte("defaults:\n  timezone: UTC\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, path, err := LoadNearest(child)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(root, "clogs.yml") {
		t.Fatalf("path = %q", path)
	}
	if cfg.Defaults.Timezone != "UTC" {
		t.Fatalf("timezone = %q", cfg.Defaults.Timezone)
	}
}

func TestLoadNearestSupportsYAMLFileExtension(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "clogs.yaml"), []byte("defaults:\n  timezone: UTC\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, path, err := LoadNearest(root)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(root, "clogs.yaml") {
		t.Fatalf("path = %q", path)
	}
	if cfg.Defaults.Timezone != "UTC" {
		t.Fatalf("timezone = %q", cfg.Defaults.Timezone)
	}
}

func TestLoadRejectsUnknownKeys(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "clogs.yml")
	if err := os.WriteFile(path, []byte("defaults:\n  nope: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "field nope") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadAcceptsPathsConfiguration(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "clogs.yml")
	if err := os.WriteFile(path, []byte("paths:\n  downloads_root: ./downloads\n  db_root: ./data/db\n  source_root: ./data/source\n  reports_root: ./reports\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Paths.DownloadsRoot != "./downloads" || cfg.Paths.DBRoot != "./data/db" || cfg.Paths.SourceRoot != "./data/source" || cfg.Paths.ReportsRoot != "./reports" {
		t.Fatalf("unexpected paths config: %+v", cfg.Paths)
	}
}
