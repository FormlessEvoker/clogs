// Package config loads clogs.yml defaults from the current directory tree.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Defaults CommandDefaults `yaml:"defaults"`
	Paths    PathsConfig     `yaml:"paths"`
	Remote   RemoteConfig    `yaml:"remote"`
	Parse    CommandDefaults `yaml:"parse"`
	Ingest   CommandDefaults `yaml:"ingest"`
	Export   CommandDefaults `yaml:"export"`
	Stats    CommandDefaults `yaml:"stats"`
	Query    CommandDefaults `yaml:"query"`
	Report   CommandDefaults `yaml:"report"`
}

type PathsConfig struct {
	DownloadsRoot string `yaml:"downloads_root"`
	DBRoot        string `yaml:"db_root"`
	SourceRoot    string `yaml:"source_root"`
	ReportsRoot   string `yaml:"reports_root"`
}

type RemoteConfig struct {
	Defaults CommandDefaults            `yaml:"defaults"`
	Servers  map[string]CommandDefaults `yaml:"servers"`
}

type CommandDefaults struct {
	DB                string   `yaml:"db"`
	Timezone          string   `yaml:"timezone"`
	Source            string   `yaml:"source"`
	Around            string   `yaml:"around"`
	Before            string   `yaml:"before"`
	After             string   `yaml:"after"`
	On                string   `yaml:"on"`
	Since             string   `yaml:"since"`
	Format            string   `yaml:"format"`
	Bucket            string   `yaml:"bucket"`
	QuietPeriod       string   `yaml:"quiet_period"`
	PreWindow         string   `yaml:"pre_window"`
	CorrelationWindow string   `yaml:"correlation_window"`
	Output            string   `yaml:"output"`
	Title             string   `yaml:"title"`
	Family            string   `yaml:"family"`
	Severity          string   `yaml:"severity"`
	Status            string   `yaml:"status"`
	Route             string   `yaml:"route"`
	Site              string   `yaml:"site"`
	Signature         string   `yaml:"signature"`
	Dir               string   `yaml:"dir"`
	Out               string   `yaml:"out"`
	Patterns          []string `yaml:"pattern"`
	RouteTemplates    []string `yaml:"route_templates"`
	StoreRaw          *bool    `yaml:"store_raw"`
	Strict            *bool    `yaml:"strict"`
	IncludeRaw        *bool    `yaml:"include_raw"`
}

func FindNearest(startDir string) (string, bool, error) {
	current, err := filepath.Abs(startDir)
	if err != nil {
		return "", false, err
	}
	candidates := []string{"clogs.yml", "clogs.yaml"}
	for {
		for _, name := range candidates {
			candidate := filepath.Join(current, name)
			info, statErr := os.Stat(candidate)
			if statErr == nil {
				if info.IsDir() {
					return "", false, fmt.Errorf("%s is a directory", candidate)
				}
				return candidate, true, nil
			}
			if !os.IsNotExist(statErr) {
				return "", false, statErr
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", false, nil
		}
		current = parent
	}
}

func Load(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer file.Close()
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	var cfg Config
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	return cfg, nil
}

func LoadNearest(startDir string) (Config, string, error) {
	path, found, err := FindNearest(startDir)
	if err != nil {
		return Config{}, "", err
	}
	if !found {
		return Config{}, "", nil
	}
	cfg, err := Load(path)
	if err != nil {
		return Config{}, path, err
	}
	return cfg, path, nil
}

func (c Config) DefaultsFor(command string) CommandDefaults {
	switch command {
	case "parse":
		return merge(c.Defaults, c.Parse)
	case "ingest":
		return merge(c.Defaults, c.Ingest)
	case "export":
		return merge(c.Defaults, c.Export)
	case "stats":
		return merge(c.Defaults, c.Stats)
	case "query":
		return merge(c.Defaults, c.Query)
	case "report":
		return merge(c.Defaults, c.Report)
	default:
		return c.Defaults
	}
}

func (c Config) RemoteDefaults(destination string) CommandDefaults {
	merged := c.Defaults
	if hasRemoteWindow(c.Remote.Defaults) {
		merged.After, merged.Before, merged.On, merged.Since = "", "", "", ""
	}
	merged = merge(merged, c.Remote.Defaults)
	if c.Remote.Servers != nil {
		if server, ok := c.Remote.Servers[destination]; ok {
			if hasRemoteWindow(server) {
				merged.After, merged.Before, merged.On, merged.Since = "", "", "", ""
			}
			merged = merge(merged, server)
		}
	}
	return merged
}

func hasRemoteWindow(defaults CommandDefaults) bool {
	return defaults.After != "" || defaults.Before != "" || defaults.On != "" || defaults.Since != ""
}

func merge(base, overlay CommandDefaults) CommandDefaults {
	result := base
	mergeString := func(dst *string, value string) {
		if value != "" {
			*dst = value
		}
	}
	mergeString(&result.DB, overlay.DB)
	mergeString(&result.Timezone, overlay.Timezone)
	mergeString(&result.Source, overlay.Source)
	mergeString(&result.Around, overlay.Around)
	mergeString(&result.Before, overlay.Before)
	mergeString(&result.After, overlay.After)
	mergeString(&result.On, overlay.On)
	mergeString(&result.Since, overlay.Since)
	mergeString(&result.Format, overlay.Format)
	mergeString(&result.Bucket, overlay.Bucket)
	mergeString(&result.QuietPeriod, overlay.QuietPeriod)
	mergeString(&result.PreWindow, overlay.PreWindow)
	mergeString(&result.CorrelationWindow, overlay.CorrelationWindow)
	mergeString(&result.Output, overlay.Output)
	mergeString(&result.Title, overlay.Title)
	mergeString(&result.Family, overlay.Family)
	mergeString(&result.Severity, overlay.Severity)
	mergeString(&result.Status, overlay.Status)
	mergeString(&result.Route, overlay.Route)
	mergeString(&result.Site, overlay.Site)
	mergeString(&result.Signature, overlay.Signature)
	mergeString(&result.Dir, overlay.Dir)
	mergeString(&result.Out, overlay.Out)
	if overlay.Patterns != nil {
		result.Patterns = append([]string(nil), overlay.Patterns...)
	}
	if overlay.RouteTemplates != nil {
		result.RouteTemplates = append([]string(nil), overlay.RouteTemplates...)
	}
	if overlay.StoreRaw != nil {
		value := *overlay.StoreRaw
		result.StoreRaw = &value
	}
	if overlay.Strict != nil {
		value := *overlay.Strict
		result.Strict = &value
	}
	if overlay.IncludeRaw != nil {
		value := *overlay.IncludeRaw
		result.IncludeRaw = &value
	}
	return result
}
