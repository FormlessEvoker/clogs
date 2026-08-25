package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/FormlessEvoker/clogs/internal/config"
)

func loadConfig() (config.Config, string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return config.Config{}, "", err
	}
	return config.LoadNearest(cwd)
}

func configUsageError(stderr io.Writer, path string, err error) int {
	if path != "" {
		return usageError(stderr, fmt.Sprintf("invalid config %s: %v", path, err))
	}
	return usageError(stderr, fmt.Sprintf("load config: %v", err))
}

type overridePatterns struct {
	values   []string
	explicit bool
}

func (p *overridePatterns) Seed(defaults []string) {
	p.values = append([]string(nil), defaults...)
	p.explicit = false
}

func (p *overridePatterns) String() string {
	return strings.Join(p.values, ",")
}

func (p *overridePatterns) Set(value string) error {
	if !p.explicit {
		p.values = p.values[:0]
		p.explicit = true
	}
	p.values = append(p.values, value)
	return nil
}

func (p *overridePatterns) Values() []string {
	return append([]string(nil), p.values...)
}

func parseDurationDefault(value string, fallback time.Duration) (time.Duration, error) {
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, err
	}
	return parsed, nil
}
