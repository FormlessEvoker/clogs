package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/FormlessEvoker/clogs/internal/config"
)

func serverDefaults(cfg config.Config, server string) config.CommandDefaults {
	if server == "" {
		return config.CommandDefaults{}
	}
	return cfg.RemoteDefaults(server)
}

func downloadRoot(cfg config.Config, defaults config.CommandDefaults) string {
	if defaults.Out != "" {
		return defaults.Out
	}
	if cfg.Paths.DownloadsRoot != "" {
		return cfg.Paths.DownloadsRoot
	}
	return "downloads"
}

func databaseRoot(cfg config.Config) string {
	return cfg.Paths.DBRoot
}

func reportsRoot(cfg config.Config) string {
	if cfg.Paths.ReportsRoot != "" {
		return cfg.Paths.ReportsRoot
	}
	return "reports"
}

func inferLatestCollection(root, server string) (string, error) {
	serverRoot := filepath.Join(root, sanitizeSource(server))
	entries, err := os.ReadDir(serverRoot)
	if err != nil {
		return "", err
	}
	var (
		bestPath  string
		bestStamp time.Time
		found     bool
	)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(serverRoot, entry.Name())
		stamp, ok := parseCollectionStamp(entry.Name())
		if !ok {
			info, infoErr := entry.Info()
			if infoErr != nil {
				continue
			}
			stamp = info.ModTime()
		}
		if !found || stamp.After(bestStamp) || (stamp.Equal(bestStamp) && candidate > bestPath) {
			bestPath = candidate
			bestStamp = stamp
			found = true
		}
	}
	if !found {
		return "", fmt.Errorf("no download collections found under %s", serverRoot)
	}
	return bestPath, nil
}

func inferDatabasePath(input, server, source, dbRoot string) string {
	collection := collectionIDForInput(input)
	sourceName := sanitizeSource(source)
	if source == "" {
		sourceName = sanitizeSource(server)
	}
	if dbRoot != "" {
		return filepath.Join(dbRoot, sanitizeSource(server), collection, sourceName+".db")
	}
	base := input
	if stat, err := os.Stat(input); err == nil && stat.IsDir() {
		base = input
	} else {
		base = filepath.Dir(input)
	}
	return filepath.Join(base, sourceName+".db")
}

func collectionIDForInput(input string) string {
	base := input
	if stat, err := os.Stat(input); err == nil && !stat.IsDir() {
		base = filepath.Dir(input)
	}
	name := filepath.Base(base)
	if name == "." || name == string(filepath.Separator) || name == "" {
		return "collection"
	}
	return sanitizeSource(name)
}

func inferLatestDatabasePath(cfg config.Config, server, source string, defaults config.CommandDefaults) (string, error) {
	collectionRoot := downloadRoot(cfg, defaults)
	if root := databaseRoot(cfg); root != "" {
		collectionRoot = root
	}
	collection, err := inferLatestCollection(collectionRoot, server)
	if err != nil {
		return "", err
	}
	candidates := []string{
		filepath.Join(collection, sanitizeSource(source)+".db"),
		filepath.Join(collection, sanitizeSource(server)+".db"),
	}
	for _, candidate := range candidates {
		if validExistingFile(candidate) {
			return candidate, nil
		}
	}
	return candidates[0], fmt.Errorf("no database found for %s under %s", server, collection)
}

func inferIncidentReportPath(root, server string, around time.Time) string {
	dateFolder := around.Format("2006-01-02")
	stamp := around.UTC().Format("20060102T150405Z")
	return filepath.Join(root, "incident", sanitizeSource(server), dateFolder, stamp+"-incident.html")
}

func moveCollectionToSourceRoot(collectionPath, sourceRoot, server string) (string, error) {
	if sourceRoot == "" || server == "" {
		return "", nil
	}
	info, err := os.Stat(collectionPath)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("inferred input %q is not a directory", collectionPath)
	}
	target := filepath.Join(sourceRoot, sanitizeSource(server), filepath.Base(collectionPath))
	if filepath.Clean(target) == filepath.Clean(collectionPath) {
		return target, nil
	}
	if _, err := os.Stat(target); err == nil {
		return "", fmt.Errorf("archive destination %q already exists", target)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return "", err
	}
	if err := os.Rename(collectionPath, target); err != nil {
		return "", err
	}
	return target, nil
}

func parseCollectionStamp(name string) (time.Time, bool) {
	layouts := []struct {
		layout string
		utc    bool
	}{
		{"2006-01-02T150405Z", true},
		{"2006-01-02T150405", true},
		{"2006-01-02_150405Z", true},
		{"2006-01-02_150405", true},
	}
	for _, candidate := range layouts {
		if candidate.utc {
			stamp, err := time.Parse(candidate.layout, name)
			if err == nil {
				return stamp.UTC(), true
			}
			continue
		}
		stamp, err := time.ParseInLocation(candidate.layout, name, time.UTC)
		if err == nil {
			return stamp.UTC(), true
		}
	}
	return time.Time{}, false
}

func validExistingFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func sanitizeSource(value string) string {
	var b strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(".-_", r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "source"
	}
	return b.String()
}

func hasExplicitFlag(visited map[string]bool, name string) bool {
	return visited[name]
}

func setIfUnsetString(target *string, visited map[string]bool, name, value string) {
	if !visited[name] && value != "" {
		*target = value
	}
}

func setIfUnsetBool(target *bool, visited map[string]bool, name string, value bool) {
	if !visited[name] {
		*target = value
	}
}
