// Package cli provides the command-line interface for clogs.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/FormlessEvoker/clogs/internal/download"
	"github.com/FormlessEvoker/clogs/internal/ingest"
	"github.com/FormlessEvoker/clogs/internal/model"
	"github.com/FormlessEvoker/clogs/internal/parser/access"
	"github.com/FormlessEvoker/clogs/internal/parser/catalina"
	"github.com/FormlessEvoker/clogs/internal/parser/jvmmultiline"
	"github.com/FormlessEvoker/clogs/internal/report"
	"github.com/FormlessEvoker/clogs/internal/storage"
	"github.com/FormlessEvoker/clogs/internal/timewindow"
)

const (
	ExitSuccess = 0
	ExitRuntime = 1
	ExitUsage   = 2
)

// BuildVersion is replaced at build time with -ldflags. Development builds use
// the default value.
var BuildVersion = "dev"

var plannedCommands = map[string]string{}

// Run executes the CLI using the supplied streams. It returns a documented
// process exit code and never writes diagnostics to stdout.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		writeRootHelp(stdout)
		return ExitSuccess
	}

	switch args[0] {
	case "-h", "--help":
		if len(args) != 1 {
			return usageError(stderr, "--help does not accept additional arguments")
		}
		writeRootHelp(stdout)
		return ExitSuccess
	case "help":
		return runHelp(args[1:], stdout, stderr)
	case "version":
		return runVersion(args[1:], stdout, stderr)
	case "fetch":
		return runRemote(append([]string{"fetch"}, args[1:]...), stdout, stderr, download.NewService())
	case "list":
		return runRemote(append([]string{"list"}, args[1:]...), stdout, stderr, download.NewService())
	case "remote":
		return runRemote(args[1:], stdout, stderr, download.NewService())
	case "parse":
		return runParse(args[1:], stdout, stderr)
	case "ingest":
		return runIngest(args[1:], stdout, stderr)
	case "export":
		return runExport(args[1:], stdout, stderr)
	case "stats":
		return runStats(args[1:], stdout, stderr)
	case "query":
		return runQuery(args[1:], stdout, stderr)
	case "report":
		return runReport(args[1:], stdout, stderr)
	default:
		if _, planned := plannedCommands[args[0]]; planned {
			return usageError(stderr, fmt.Sprintf("%q is planned but not yet implemented", args[0]))
		}
		return usageError(stderr, fmt.Sprintf("unknown command %q", args[0]))
	}
}

func runHelp(args []string, stdout, stderr io.Writer) int {
	switch len(args) {
	case 0:
		writeRootHelp(stdout)
		return ExitSuccess
	case 1:
		switch args[0] {
		case "version":
			writeVersionHelp(stdout)
			return ExitSuccess
		case "remote":
			writeRemoteHelp(stdout)
			return ExitSuccess
		case "fetch", "list":
			writeRemoteHelp(stdout)
			return ExitSuccess
		case "parse":
			writeParseHelp(stdout)
			return ExitSuccess
		case "ingest":
			writeIngestHelp(stdout)
			return ExitSuccess
		case "export":
			writeExportHelp(stdout)
			return ExitSuccess
		case "stats":
			writeStatsHelp(stdout)
			return ExitSuccess
		case "query":
			writeQueryHelp(stdout)
			return ExitSuccess
		case "report":
			writeReportHelp(stdout)
			return ExitSuccess
		default:
			if _, planned := plannedCommands[args[0]]; planned {
				fmt.Fprintf(stdout, "%s is planned but not yet implemented.\n", args[0])
				return ExitSuccess
			}
			return usageError(stderr, fmt.Sprintf("unknown command %q", args[0]))
		}
	default:
		return usageError(stderr, "help accepts at most one command")
	}
}

func runReport(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		writeReportHelp(stdout)
		return ExitSuccess
	}
	if args[0] != "incident" {
		return usageError(stderr, fmt.Sprintf("report supports only the incident subcommand, not %q", args[0]))
	}
	if len(args) == 1 || args[1] == "-h" || args[1] == "--help" {
		writeReportHelp(stdout)
		return ExitSuccess
	}
	cfg, cfgPath, err := loadConfig()
	if err != nil {
		return configUsageError(stderr, cfgPath, err)
	}
	defaults := cfg.DefaultsFor("report")
	beforeDefault, err := parseDurationDefault(defaults.Before, time.Hour)
	if err != nil {
		return usageError(stderr, fmt.Sprintf("invalid config report.before: %v", err))
	}
	afterDefault, err := parseDurationDefault(defaults.After, time.Hour)
	if err != nil {
		return usageError(stderr, fmt.Sprintf("invalid config report.after: %v", err))
	}
	bucketDefault, err := parseDurationDefault(defaults.Bucket, time.Minute)
	if err != nil {
		return usageError(stderr, fmt.Sprintf("invalid config report.bucket: %v", err))
	}
	quietDefault, err := parseDurationDefault(defaults.QuietPeriod, 30*time.Second)
	if err != nil {
		return usageError(stderr, fmt.Sprintf("invalid config report.quiet_period: %v", err))
	}
	preDefault, err := parseDurationDefault(defaults.PreWindow, 30*time.Second)
	if err != nil {
		return usageError(stderr, fmt.Sprintf("invalid config report.pre_window: %v", err))
	}
	correlationDefault, err := parseDurationDefault(defaults.CorrelationWindow, 2*time.Second)
	if err != nil {
		return usageError(stderr, fmt.Sprintf("invalid config report.correlation_window: %v", err))
	}
	flags := flag.NewFlagSet("report incident", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	server := flags.String("server", "", "server alias for download/db inference")
	database := flags.String("db", defaults.DB, "SQLite database")
	aroundText := flags.String("around", defaults.Around, "RFC3339 center time")
	before := flags.Duration("before", beforeDefault, "time before center")
	after := flags.Duration("after", afterDefault, "time after center")
	bucket := flags.Duration("bucket", bucketDefault, "elapsed-time bucket")
	quiet := flags.Duration("quiet-period", quietDefault, "burst quiet period")
	pre := flags.Duration("pre-window", preDefault, "pre-onset window")
	correlation := flags.Duration("correlation-window", correlationDefault, "exception/request window")
	timezone := flags.String("timezone", defaults.Timezone, "IANA display timezone")
	source := flags.String("source", defaults.Source, "source filter")
	outputPath := flags.String("output", defaults.Output, "HTML output path")
	title := flags.String("title", defaults.Title, "report title")
	if err := flags.Parse(args[1:]); err != nil {
		return usageError(stderr, err.Error())
	}
	visited := map[string]bool{}
	flags.Visit(func(flag *flag.Flag) {
		visited[flag.Name] = true
	})
	if flags.NArg() != 0 {
		return usageError(stderr, "report incident does not accept positional arguments")
	}
	if *server != "" {
		serverCfg := serverDefaults(cfg, *server)
		setIfUnsetString(timezone, visited, "timezone", serverCfg.Timezone)
		setIfUnsetString(source, visited, "source", *server)
		if *database == "" {
			inferred, inferErr := inferLatestDatabasePath(cfg, *server, *source, serverCfg)
			if inferErr != nil {
				return usageError(stderr, fmt.Sprintf("infer latest database for %s: %v", *server, inferErr))
			}
			*database = inferred
		}
	}
	if *database == "" || *aroundText == "" || *timezone == "" {
		return usageError(stderr, "report incident requires --db, --around, and --timezone")
	}
	around, err := time.Parse(time.RFC3339Nano, *aroundText)
	if err != nil {
		return usageError(stderr, fmt.Sprintf("invalid --around: %v", err))
	}
	if *outputPath == "" {
		if *server == "" {
			return usageError(stderr, "report incident requires --output when --server is not set")
		}
		*outputPath = inferIncidentReportPath(reportsRoot(cfg), *server, around)
	}
	if *server != "" && !visited["db"] && !validExistingFile(*database) {
		return usageError(stderr, fmt.Sprintf("inferred database %q not found", *database))
	}
	if _, err := time.LoadLocation(*timezone); err != nil {
		return usageError(stderr, fmt.Sprintf("invalid --timezone: %v", err))
	}
	if *before < 0 || *after < 0 || *bucket <= 0 || *quiet < 0 || *pre < 0 || *correlation < 0 {
		return usageError(stderr, "report durations must be non-negative and --bucket must be positive")
	}
	databaseAbs, err := filepath.Abs(*database)
	if err != nil {
		return usageError(stderr, fmt.Sprintf("resolve --db: %v", err))
	}
	outputAbs, err := filepath.Abs(*outputPath)
	if err != nil {
		return usageError(stderr, fmt.Sprintf("resolve --output: %v", err))
	}
	if databaseAbs == outputAbs {
		return usageError(stderr, "--output must not overwrite --db")
	}
	db, err := storage.Open(*database)
	if err != nil {
		fmt.Fprintf(stderr, "clogs: %v\n", err)
		return ExitRuntime
	}
	defer db.Close()
	analysis, err := report.Analyze(context.Background(), db, report.QueryOptions{
		Around: around, Before: *before, After: *after, Bucket: *bucket,
		QuietPeriod: *quiet, PreWindow: *pre, CorrelationWindow: *correlation, Source: *source,
	})
	if err != nil {
		fmt.Fprintf(stderr, "clogs: report incident: %v\n", err)
		return ExitRuntime
	}
	if err := writeIncidentFile(*outputPath, func(writer io.Writer) error {
		return report.WriteIncidentHTML(writer, analysis, report.IncidentHTMLConfig{Timezone: *timezone, Title: *title})
	}); err != nil {
		fmt.Fprintf(stderr, "clogs: report incident: %v\n", err)
		return ExitRuntime
	}
	fmt.Fprintf(stdout, "Incident report: %s\nEvents analyzed: %d\n", *outputPath, analysis.BaselineSize)
	return ExitSuccess
}

func writeIncidentFile(path string, write func(io.Writer) error) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".clogs-report-*.html")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0600); err != nil {
		temporary.Close()
		return err
	}
	if err := write(temporary); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func runQuery(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		writeQueryHelp(stdout)
		return ExitSuccess
	}
	cfg, cfgPath, err := loadConfig()
	if err != nil {
		return configUsageError(stderr, cfgPath, err)
	}
	defaults := cfg.DefaultsFor("query")
	formatDefault := "human"
	if defaults.Format != "" {
		formatDefault = defaults.Format
	}
	beforeDefault, err := parseDurationDefault(defaults.Before, 2*time.Minute)
	if err != nil {
		return usageError(stderr, fmt.Sprintf("invalid config query.before: %v", err))
	}
	afterDefault, err := parseDurationDefault(defaults.After, 2*time.Minute)
	if err != nil {
		return usageError(stderr, fmt.Sprintf("invalid config query.after: %v", err))
	}
	bucketDefault, err := parseDurationDefault(defaults.Bucket, time.Second)
	if err != nil {
		return usageError(stderr, fmt.Sprintf("invalid config query.bucket: %v", err))
	}
	quietDefault, err := parseDurationDefault(defaults.QuietPeriod, 30*time.Second)
	if err != nil {
		return usageError(stderr, fmt.Sprintf("invalid config query.quiet_period: %v", err))
	}
	preDefault, err := parseDurationDefault(defaults.PreWindow, 30*time.Second)
	if err != nil {
		return usageError(stderr, fmt.Sprintf("invalid config query.pre_window: %v", err))
	}
	correlationDefault, err := parseDurationDefault(defaults.CorrelationWindow, 2*time.Second)
	if err != nil {
		return usageError(stderr, fmt.Sprintf("invalid config query.correlation_window: %v", err))
	}
	flags := flag.NewFlagSet("query", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	server := flags.String("server", "", "server alias for download/db inference")
	database := flags.String("db", defaults.DB, "SQLite database")
	aroundText := flags.String("around", defaults.Around, "RFC3339 center time")
	before := flags.Duration("before", beforeDefault, "time before center")
	after := flags.Duration("after", afterDefault, "time after center")
	bucket := flags.Duration("bucket", bucketDefault, "elapsed-time bucket")
	quiet := flags.Duration("quiet-period", quietDefault, "burst quiet period")
	pre := flags.Duration("pre-window", preDefault, "pre-onset window")
	correlation := flags.Duration("correlation-window", correlationDefault, "exception/request window")
	format := flags.String("format", formatDefault, "human or json output")
	source := flags.String("source", defaults.Source, "source filter")
	family := flags.String("family", defaults.Family, "family filter")
	severity := flags.String("severity", defaults.Severity, "severity filter")
	status := flags.String("status", defaults.Status, "HTTP status or class")
	route := flags.String("route", defaults.Route, "route-template filter")
	site := flags.String("site", defaults.Site, "site filter")
	signature := flags.String("signature", defaults.Signature, "signature filter")
	if err := flags.Parse(args); err != nil {
		return usageError(stderr, err.Error())
	}
	visited := map[string]bool{}
	flags.Visit(func(flag *flag.Flag) {
		visited[flag.Name] = true
	})
	if flags.NArg() != 0 {
		return usageError(stderr, "query does not accept positional arguments")
	}
	if *server != "" {
		serverCfg := serverDefaults(cfg, *server)
		setIfUnsetString(source, visited, "source", *server)
		if *database == "" {
			inferred, inferErr := inferLatestDatabasePath(cfg, *server, *source, serverCfg)
			if inferErr != nil {
				return usageError(stderr, fmt.Sprintf("infer latest database for %s: %v", *server, inferErr))
			}
			*database = inferred
		}
	}
	if *database == "" {
		return usageError(stderr, "query requires --db")
	}
	if *server != "" && !visited["db"] && !validExistingFile(*database) {
		return usageError(stderr, fmt.Sprintf("inferred database %q not found", *database))
	}
	if *aroundText == "" {
		return usageError(stderr, "query requires --around")
	}
	around, err := time.Parse(time.RFC3339Nano, *aroundText)
	if err != nil {
		return usageError(stderr, fmt.Sprintf("invalid --around: %v", err))
	}
	if *before < 0 || *after < 0 || *bucket <= 0 || *quiet < 0 || *pre < 0 || *correlation < 0 {
		return usageError(stderr, "query durations must be non-negative and --bucket must be positive")
	}
	if *format != "human" && *format != "json" {
		return usageError(stderr, "query supports only --format human or json")
	}
	if *family != "" && *family != "jvm-multiline" && *family != "access" && *family != "catalina" {
		return usageError(stderr, "--family must be jvm-multiline, access, or catalina")
	}
	if *status != "" {
		validClass := len(*status) == 3 && (*status)[0] >= '1' && (*status)[0] <= '5' && (*status)[1:] == "xx"
		value, parseErr := strconv.Atoi(*status)
		if !validClass && (parseErr != nil || value < 100 || value > 599) {
			return usageError(stderr, "--status must be an HTTP status such as 500 or class such as 5xx")
		}
	}
	db, err := storage.Open(*database)
	if err != nil {
		fmt.Fprintf(stderr, "clogs: %v\n", err)
		return ExitRuntime
	}
	defer db.Close()
	result, err := report.Analyze(context.Background(), db, report.QueryOptions{Around: around, Before: *before, After: *after, Bucket: *bucket, QuietPeriod: *quiet, PreWindow: *pre, CorrelationWindow: *correlation, Source: *source, Family: *family, Severity: *severity, Status: *status, Route: *route, Site: *site, Signature: *signature})
	if err != nil {
		fmt.Fprintf(stderr, "clogs: query: %v\n", err)
		return ExitRuntime
	}
	if *format == "json" {
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			fmt.Fprintf(stderr, "clogs: query: %v\n", err)
			return ExitRuntime
		}
	} else {
		writeQuery(stdout, result)
	}
	return ExitSuccess
}

func writeQuery(output io.Writer, result report.QueryReport) {
	fmt.Fprintf(output, "Window: %s to %s (around %s)\n", result.WindowStart, result.WindowEnd, result.Around)
	fmt.Fprintf(output, "Timeline filters: %s\n", formatFilters(result.Filters))
	fmt.Fprintf(output, "Timeline sample: %d event(s); analysis baseline: %d event(s) in the time/source window\n", result.SampleSize, result.BaselineSize)
	fmt.Fprintf(output, "Analysis parameters: bucket=%s quiet-period=%s pre-window=%s correlation-window=%s\n\nTimeline:\n", result.Bucket, result.QuietPeriod, result.PreWindow, result.CorrelationWindow)
	for _, event := range result.Timeline {
		detail := event.Message
		if event.Status != nil {
			detail = fmt.Sprintf("HTTP %d %s %s", *event.Status, event.Method, event.RouteTemplate)
		}
		fmt.Fprintf(output, "  %s %-8s %s:%d %s\n", event.OccurredAt, event.Family, event.SourcePath, event.SourceLineStart, detail)
	}
	fmt.Fprintln(output, "\nElapsed-time buckets:")
	for _, bucket := range result.Buckets {
		fmt.Fprintf(output, "  %s..%s %s\n", bucket.Start, bucket.End, formatFamilyCounts(bucket.Counts))
	}
	fmt.Fprintln(output, "\nRoute failure rates:")
	for _, route := range result.Routes {
		fmt.Fprintf(output, "  %s site=%s failures=%d/%d (%.1f%%)\n", route.Route, blank(route.Site), route.Failures, route.Requests, route.FailureRate*100)
	}
	fmt.Fprintln(output, "\nPre-OOM request pattern signals:")
	for _, signal := range result.RequestSignals {
		fmt.Fprintf(output, "  %s onset=%d spike=%d pre=%d prior=%d ratio=%.2fx\n", signal.Route, signal.OnsetCount, signal.SpikeCount, signal.PreRequests, signal.PriorRequests, signal.PreToPrior)
	}
	fmt.Fprintln(output, "\nSignature frequency:")
	writeCounts(output, result.Signatures)
	fmt.Fprintln(output, "Onsets and pre-error windows:")
	for _, onset := range result.Onsets {
		fmt.Fprintf(output, "  %s %s exception=%s pre-counts=%s\n", onset.OccurredAt, onset.Family, blank(onset.ExceptionClass), formatFamilyCounts(onset.PreCounts))
		if onset.CorrelationStatement != "" {
			fmt.Fprintf(output, "    %s\n", onset.CorrelationStatement)
		}
	}
}
func formatFilters(values map[string]string) string {
	if len(values) == 0 {
		return "(none)"
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+values[key])
	}
	return strings.Join(parts, ", ")
}
func formatFamilyCounts(values map[string]int) string {
	if len(values) == 0 {
		return "(none)"
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, values[key]))
	}
	return strings.Join(parts, ", ")
}
func blank(value string) string {
	if value == "" {
		return "(none)"
	}
	return value
}

func runExport(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		writeExportHelp(stdout)
		return ExitSuccess
	}
	cfg, cfgPath, err := loadConfig()
	if err != nil {
		return configUsageError(stderr, cfgPath, err)
	}
	defaults := cfg.DefaultsFor("export")
	flags := flag.NewFlagSet("export", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	database := flags.String("db", defaults.DB, "SQLite database")
	formatDefault := "ndjson"
	if defaults.Format != "" {
		formatDefault = defaults.Format
	}
	format := flags.String("format", formatDefault, "output format")
	outputPath := flags.String("output", defaults.Output, "output file; default stdout")
	source := flags.String("source", defaults.Source, "source label filter")
	rawDefault := false
	if defaults.IncludeRaw != nil {
		rawDefault = *defaults.IncludeRaw
	}
	raw := flags.Bool("include-raw", rawDefault, "include raw event text")
	if err := flags.Parse(args); err != nil {
		return usageError(stderr, err.Error())
	}
	if flags.NArg() != 0 {
		return usageError(stderr, "export does not accept positional arguments")
	}
	if *database == "" {
		return usageError(stderr, "export requires --db")
	}
	if *format != "ndjson" && *format != "csv" {
		return usageError(stderr, "export supports only --format ndjson or csv")
	}
	db, err := storage.Open(*database)
	if err != nil {
		fmt.Fprintf(stderr, "clogs: %v\n", err)
		return ExitRuntime
	}
	defer db.Close()
	output := stdout
	var file *os.File
	if *outputPath != "" {
		file, err = os.OpenFile(*outputPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			fmt.Fprintf(stderr, "clogs: open output %q: %v\n", *outputPath, err)
			return ExitRuntime
		}
		defer file.Close()
		output = file
	}
	if *format == "ndjson" {
		err = report.WriteNDJSON(context.Background(), db, *source, *raw, output)
	} else {
		err = report.WriteCSV(context.Background(), db, *source, *raw, output)
	}
	if err != nil {
		fmt.Fprintf(stderr, "clogs: export: %v\n", err)
		return ExitRuntime
	}
	return ExitSuccess
}

func runStats(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		writeStatsHelp(stdout)
		return ExitSuccess
	}
	cfg, cfgPath, err := loadConfig()
	if err != nil {
		return configUsageError(stderr, cfgPath, err)
	}
	defaults := cfg.DefaultsFor("stats")
	flags := flag.NewFlagSet("stats", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	database := flags.String("db", defaults.DB, "SQLite database")
	source := flags.String("source", defaults.Source, "source label filter")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil {
		return usageError(stderr, err.Error())
	}
	if flags.NArg() != 0 {
		return usageError(stderr, "stats does not accept positional arguments")
	}
	if *database == "" {
		return usageError(stderr, "stats requires --db")
	}
	db, err := storage.Open(*database)
	if err != nil {
		fmt.Fprintf(stderr, "clogs: %v\n", err)
		return ExitRuntime
	}
	defer db.Close()
	stats, err := report.CollectStats(context.Background(), db, *source)
	if err != nil {
		fmt.Fprintf(stderr, "clogs: stats: %v\n", err)
		return ExitRuntime
	}
	if *jsonOutput {
		if err := json.NewEncoder(stdout).Encode(stats); err != nil {
			fmt.Fprintf(stderr, "clogs: stats: %v\n", err)
			return ExitRuntime
		}
	} else {
		writeStats(stdout, stats)
	}
	return ExitSuccess
}

func writeStats(output io.Writer, stats report.Stats) {
	fmt.Fprintf(output, "Time range: %s to %s\nSource files: %d\nEvents by family:\n", textOrNone(stats.TimeStart), textOrNone(stats.TimeEnd), stats.SourceFiles)
	writeCounts(output, stats.Families)
	fmt.Fprintln(output, "Events by severity:")
	writeCounts(output, stats.Severities)
	fmt.Fprintln(output, "HTTP status classes:")
	writeCounts(output, stats.HTTPStatusClasses)
	fmt.Fprintln(output, "Top routes:")
	writeCounts(output, stats.Routes)
	fmt.Fprintln(output, "Top signatures:")
	writeCounts(output, stats.Signatures)
	fmt.Fprintln(output, "Exceptions:")
	writeCounts(output, stats.Exceptions)
	fmt.Fprintln(output, "Protocol types:")
	writeCounts(output, stats.Protocols)
	fmt.Fprintln(output, "Source labels:")
	writeCounts(output, stats.Sources)
	fmt.Fprintf(output, "Events with parse warnings: %d\n", stats.ParseWarnings)
}
func writeCounts(output io.Writer, values []report.Count) {
	if len(values) == 0 {
		fmt.Fprintln(output, "  (none)")
		return
	}
	for _, value := range values {
		fmt.Fprintf(output, "  %s: %d\n", value.Name, value.Count)
	}
}
func textOrNone(value *string) string {
	if value == nil {
		return "(none)"
	}
	return *value
}

func runIngest(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		writeIngestHelp(stdout)
		return ExitSuccess
	}
	cfg, cfgPath, err := loadConfig()
	if err != nil {
		return configUsageError(stderr, cfgPath, err)
	}
	defaults := cfg.DefaultsFor("ingest")
	flags := flag.NewFlagSet("ingest", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	server := flags.String("server", "", "server alias for download/db inference")
	defaultSource := "local"
	if defaults.Source != "" {
		defaultSource = defaults.Source
	}
	storeRawDefault := true
	if defaults.StoreRaw != nil {
		storeRawDefault = *defaults.StoreRaw
	}
	strictDefault := false
	if defaults.Strict != nil {
		strictDefault = *defaults.Strict
	}
	database := flags.String("db", defaults.DB, "SQLite destination database")
	source := flags.String("source", defaultSource, "source label")
	timezone := flags.String("timezone", defaults.Timezone, "IANA timezone for timezone-less logs")
	storeRaw := flags.Bool("store-raw", storeRawDefault, "store raw event text")
	strict := flags.Bool("strict", strictDefault, "roll back files with malformed records")
	input, flagArgs := "", args
	if !strings.HasPrefix(flagArgs[0], "-") {
		input, flagArgs = flagArgs[0], flagArgs[1:]
	}
	if err := flags.Parse(flagArgs); err != nil {
		return usageError(stderr, err.Error())
	}
	visited := map[string]bool{}
	flags.Visit(func(flag *flag.Flag) {
		visited[flag.Name] = true
	})
	if input == "" {
		switch flags.NArg() {
		case 0:
			if *server == "" {
				return usageError(stderr, "ingest requires exactly one file or directory")
			}
		case 1:
			input = flags.Arg(0)
		default:
			return usageError(stderr, "ingest requires exactly one file or directory")
		}
	} else if flags.NArg() != 0 {
		return usageError(stderr, "ingest requires exactly one file or directory")
	}
	if *server != "" {
		serverCfg := serverDefaults(cfg, *server)
		setIfUnsetString(timezone, visited, "timezone", serverCfg.Timezone)
		setIfUnsetString(source, visited, "source", *server)
		root := downloadRoot(cfg, serverCfg)
		inferredInput := false
		if input == "" {
			collection, err := inferLatestCollection(root, *server)
			if err != nil {
				return usageError(stderr, fmt.Sprintf("infer latest download collection for %s: %v", *server, err))
			}
			input = collection
			inferredInput = true
		}
		if *database == "" {
			*database = inferDatabasePath(input, *server, *source, databaseRoot(cfg))
		}
		if *database == "" {
			return usageError(stderr, "ingest requires --db")
		}
		if err := os.MkdirAll(filepath.Dir(*database), 0o700); err != nil {
			fmt.Fprintf(stderr, "clogs: create database directory: %v\n", err)
			return ExitRuntime
		}
		db, err := storage.Open(*database)
		if err != nil {
			fmt.Fprintf(stderr, "clogs: %v\n", err)
			return ExitRuntime
		}
		defer db.Close()
		summary, err := ingest.Run(context.Background(), db, ingest.Options{Input: input, Database: *database, Source: *source, Timezone: *timezone, StoreRaw: *storeRaw, Strict: *strict, RouteTemplates: defaults.RouteTemplates})
		fmt.Fprint(stdout, ingest.SummaryText(summary, *source, *database))
		if err != nil {
			fmt.Fprintf(stderr, "clogs: ingest: %v\n", err)
			return ExitRuntime
		}
		if inferredInput && cfg.Paths.SourceRoot != "" && databaseRoot(cfg) != "" {
			archived, archiveErr := moveCollectionToSourceRoot(input, cfg.Paths.SourceRoot, *server)
			if archiveErr != nil {
				fmt.Fprintf(stderr, "clogs: archive source collection: %v\n", archiveErr)
				return ExitRuntime
			}
			if archived != "" {
				fmt.Fprintf(stdout, "Archived source:  %s\n", archived)
			}
		}
		return ExitSuccess
	}
	if *database == "" {
		return usageError(stderr, "ingest requires --db")
	}
	if err := os.MkdirAll(filepath.Dir(*database), 0o700); err != nil {
		fmt.Fprintf(stderr, "clogs: create database directory: %v\n", err)
		return ExitRuntime
	}
	db, err := storage.Open(*database)
	if err != nil {
		fmt.Fprintf(stderr, "clogs: %v\n", err)
		return ExitRuntime
	}
	defer db.Close()
	summary, err := ingest.Run(context.Background(), db, ingest.Options{Input: input, Database: *database, Source: *source, Timezone: *timezone, StoreRaw: *storeRaw, Strict: *strict, RouteTemplates: defaults.RouteTemplates})
	fmt.Fprint(stdout, ingest.SummaryText(summary, *source, *database))
	if err != nil {
		fmt.Fprintf(stderr, "clogs: ingest: %v\n", err)
		return ExitRuntime
	}
	return ExitSuccess
}

func runParse(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		writeParseHelp(stdout)
		return ExitSuccess
	}
	cfg, cfgPath, err := loadConfig()
	if err != nil {
		return configUsageError(stderr, cfgPath, err)
	}
	defaults := cfg.DefaultsFor("parse")
	formatDefault := "ndjson"
	if defaults.Format != "" {
		formatDefault = defaults.Format
	}
	flags := flag.NewFlagSet("parse", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	format := flags.String("format", formatDefault, "output format")
	timezone := flags.String("timezone", defaults.Timezone, "IANA timezone for timezone-less logs")
	filePath, flagArgs := "", args
	if !strings.HasPrefix(flagArgs[0], "-") {
		filePath, flagArgs = flagArgs[0], flagArgs[1:]
	}
	if err := flags.Parse(flagArgs); err != nil {
		return usageError(stderr, err.Error())
	}
	if filePath == "" {
		if flags.NArg() != 1 {
			return usageError(stderr, "parse requires exactly one file")
		}
		filePath = flags.Arg(0)
	} else if flags.NArg() != 0 {
		return usageError(stderr, "parse requires exactly one file")
	}
	if *format != "ndjson" {
		return usageError(stderr, "parse currently supports only --format ndjson")
	}
	file, err := os.Open(filePath)
	if err != nil {
		fmt.Fprintf(stderr, "clogs: open %q: %v\n", filePath, err)
		return ExitRuntime
	}
	defer file.Close()
	prefix := make([]byte, 32<<10)
	count, err := file.Read(prefix)
	if err != nil && err != io.EOF {
		fmt.Fprintf(stderr, "clogs: read %q: %v\n", filePath, err)
		return ExitRuntime
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		fmt.Fprintf(stderr, "clogs: rewind %q: %v\n", filePath, err)
		return ExitRuntime
	}
	encoder := json.NewEncoder(stdout)
	if jvmmultiline.Detect(prefix[:count]) > 0 {
		if *timezone == "" {
			return usageError(stderr, "parse requires --timezone for JVM logs")
		}
		location, err := time.LoadLocation(*timezone)
		if err != nil {
			return usageError(stderr, fmt.Sprintf("invalid timezone %q: %v", *timezone, err))
		}
		result, err := jvmmultiline.Parse(context.Background(), file, jvmmultiline.Options{Timezone: location, SourcePath: filePath}, func(event model.Event) error { return encoder.Encode(event) })
		if err != nil {
			fmt.Fprintf(stderr, "clogs: parse %q: %v\n", filePath, err)
			return ExitRuntime
		}
		if result.OrphanLines > 0 {
			fmt.Fprintf(stderr, "clogs: skipped %d orphan line(s) before the first JVM event\n", result.OrphanLines)
		}
		return ExitSuccess
	}
	if catalina.Detect(prefix[:count]) > 0 {
		if *timezone == "" {
			return usageError(stderr, "parse requires --timezone for Catalina logs")
		}
		location, err := time.LoadLocation(*timezone)
		if err != nil {
			return usageError(stderr, fmt.Sprintf("invalid timezone %q: %v", *timezone, err))
		}
		result, err := catalina.Parse(context.Background(), file, catalina.Options{Timezone: location, SourcePath: filePath}, func(event model.Event) error { return encoder.Encode(event) })
		if err != nil {
			fmt.Fprintf(stderr, "clogs: parse %q: %v\n", filePath, err)
			return ExitRuntime
		}
		if result.OrphanLines > 0 {
			fmt.Fprintf(stderr, "clogs: skipped %d orphan line(s) before the first Catalina event\n", result.OrphanLines)
		}
		return ExitSuccess
	}
	if access.Detect(prefix[:count]) > 0 {
		result, err := access.Parse(context.Background(), file, access.Options{SourcePath: filePath, RouteTemplates: defaults.RouteTemplates}, func(event model.Event) error { return encoder.Encode(event) })
		if err != nil {
			fmt.Fprintf(stderr, "clogs: parse %q: %v\n", filePath, err)
			return ExitRuntime
		}
		for _, malformed := range result.Malformed {
			fmt.Fprintf(stderr, "clogs: skipped malformed access line %d: %s\n", malformed.Line, malformed.Error)
		}
		return ExitSuccess
	}
	fmt.Fprintf(stderr, "clogs: %q is not a recognized JVM, Catalina, or access log\n", filePath)
	return ExitRuntime
}

func runRemote(args []string, stdout, stderr io.Writer, service download.Service) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		writeRemoteHelp(stdout)
		return ExitSuccess
	}
	if args[0] != "list" && args[0] != "fetch" {
		return usageError(stderr, fmt.Sprintf("unknown remote command %q", args[0]))
	}
	flagArgs := args[1:]
	destination := ""
	if len(flagArgs) > 0 && !strings.HasPrefix(flagArgs[0], "-") {
		destination, flagArgs = flagArgs[0], flagArgs[1:]
	}
	cfg, cfgPath, err := loadConfig()
	if err != nil {
		return configUsageError(stderr, cfgPath, err)
	}
	defaults := cfg.Defaults
	flags := flag.NewFlagSet("remote "+args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	directory := flags.String("dir", defaults.Dir, "remote directory")
	outputDefault := "downloads"
	if defaults.Out != "" {
		outputDefault = defaults.Out
	} else if cfg.Paths.DownloadsRoot != "" {
		outputDefault = cfg.Paths.DownloadsRoot
	}
	output := flags.String("out", outputDefault, "collection parent directory")
	after := flags.String("after", defaults.After, "minimum remote modification time")
	before := flags.String("before", defaults.Before, "maximum remote modification time")
	on := flags.String("on", defaults.On, "remote modification calendar date")
	since := flags.String("since", defaults.Since, "recent modification duration")
	timezone := defaults.Timezone
	flags.StringVar(&timezone, "timezone", timezone, "IANA timezone for local date/time inputs")
	flags.StringVar(&timezone, "tz", timezone, "short alias for --timezone")
	var patterns overridePatterns
	patternDefaults := download.DefaultPatterns
	if len(defaults.Patterns) > 0 {
		patternDefaults = defaults.Patterns
	}
	patterns.Seed(patternDefaults)
	flags.Var(&patterns, "pattern", "filename glob; repeat to replace default patterns")
	if err := flags.Parse(flagArgs); err != nil {
		return usageError(stderr, err.Error())
	}
	visited := map[string]bool{}
	flags.Visit(func(flag *flag.Flag) {
		visited[flag.Name] = true
	})
	if destination == "" {
		if flags.NArg() != 1 {
			return usageError(stderr, "remote "+args[0]+" requires exactly one SSH destination")
		}
		destination = flags.Arg(0)
	} else if flags.NArg() != 0 {
		return usageError(stderr, "remote "+args[0]+" requires exactly one SSH destination")
	}
	serverDefaults := cfg.RemoteDefaults(destination)
	if !visited["dir"] && serverDefaults.Dir != "" {
		*directory = serverDefaults.Dir
	}
	if !visited["out"] && serverDefaults.Out != "" {
		*output = serverDefaults.Out
	}
	if !visited["after"] && serverDefaults.After != "" {
		*after = serverDefaults.After
	}
	if !visited["before"] && serverDefaults.Before != "" {
		*before = serverDefaults.Before
	}
	if !visited["on"] && serverDefaults.On != "" {
		*on = serverDefaults.On
	}
	if !visited["since"] && serverDefaults.Since != "" {
		*since = serverDefaults.Since
	}
	if !(visited["timezone"] || visited["tz"]) && serverDefaults.Timezone != "" {
		timezone = serverDefaults.Timezone
	}
	if !patterns.explicit && len(serverDefaults.Patterns) > 0 {
		patterns.Seed(serverDefaults.Patterns)
	}
	if *directory == "" {
		return usageError(stderr, "remote "+args[0]+" requires --dir")
	}
	if *output == "" {
		*output = "downloads"
	}
	now := time.Now()
	if service.Now != nil {
		now = service.Now()
	}
	// Every relative boundary, inventory comparison, collection name, and
	// manifest in this command uses one captured instant.
	service.Now = func() time.Time { return now }
	window, err := timewindow.Resolve(timewindow.Options{After: *after, Before: *before, On: *on, Since: *since, Timezone: timezone, Now: now, Local: time.Local})
	if err != nil {
		return usageError(stderr, err.Error())
	}
	request := download.ListRequest{Destination: destination, Directory: *directory, Patterns: patterns.Values(), Window: window}
	if args[0] == "list" {
		files, err := service.List(context.Background(), request)
		if err != nil {
			fmt.Fprintf(stderr, "clogs: %v\n", err)
			return ExitRuntime
		}
		for _, name := range files {
			fmt.Fprintln(stdout, name)
		}
		return ExitSuccess
	}
	result, err := service.Fetch(context.Background(), download.FetchRequest{ListRequest: request, OutputDirectory: *output})
	if err != nil {
		fmt.Fprintf(stderr, "clogs: %v\nCollection: %s\nManifest:   %s\n", err, result.Collection, result.Manifest)
		for _, failure := range result.Failures {
			fmt.Fprintf(stderr, "Failed: %s: %s\n", failure.Name, failure.Error)
		}
		return ExitRuntime
	}
	fmt.Fprintf(stdout, "Downloaded %d files from %s:%s\n", len(result.Files), request.Destination, request.Directory)
	if window != nil {
		fmt.Fprintf(stdout, "Modification window (UTC): %s\n", window.Summary())
	}
	fmt.Fprintf(stdout, "Collection: %s\nManifest:   %s\n", filepath.Clean(result.Collection), result.Manifest)
	return ExitSuccess
}

func runVersion(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintf(stdout, "clogs %s\n", BuildVersion)
		return ExitSuccess
	}
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		writeVersionHelp(stdout)
		return ExitSuccess
	}
	return usageError(stderr, "version does not accept positional arguments")
}

func usageError(stderr io.Writer, message string) int {
	fmt.Fprintf(stderr, "clogs: %s\nRun 'clogs --help' for usage.\n", message)
	return ExitUsage
}

func writeRootHelp(stdout io.Writer) {
	fmt.Fprint(stdout, `Usage:
  clogs <command> [options]

Available commands:
  version    print the application version
  fetch      remote fetch logs using config defaults
  list       remote list logs using config defaults
  remote     list and fetch logs over SFTP
  parse      inspect a local log as NDJSON
  ingest     ingest local log files into SQLite
  stats      summarize an ingested database
  export     export events from a SQLite database
  query      query and analyze a merged event timeline
  report     generate a self-contained incident report

Use "clogs help <command>" for command help.
`)
}

func writeParseHelp(stdout io.Writer) {
	fmt.Fprint(stdout, strings.TrimSpace(`Usage:
  clogs parse <file> [--timezone <IANA-name>] [--format ndjson]

Stream a recognized JVM, Catalina, or access log as newline-delimited JSON.
JVM and Catalina timestamps have no offset, so they require --timezone (for
example, America/Chicago); access logs contain an explicit numeric offset.
`)+"\n")
}

func writeIngestHelp(stdout io.Writer) {
	fmt.Fprint(stdout, strings.TrimSpace(`Usage:
  clogs ingest [<file-or-directory>] --db <database> --timezone <IANA-name> [--server <ssh-destination>] [--source <label>] [--store-raw=true|false] [--strict]

Recursively ingest recognized JVM, Catalina, and access logs into SQLite. JVM
and Catalina timezone-less timestamps require --timezone; access logs carry a
numeric offset.
--source defaults to "local". Raw evidence is retained by default; set
--store-raw=false to omit it. Exact duplicate source/path/content files and
unrecognized files are skipped. --strict rolls back an entire file on malformed
records or parser warnings.
If --server is set and the input path is omitted, Clogs uses the latest download
collection for that server. If --source is omitted, it falls back to the server
alias. With paths.db_root set, inferred databases go to
<db-root>/<server>/<collection>/<source>.db; otherwise they remain in the
collection directory. With paths.source_root and paths.db_root set, inferred
download collections move to <source-root>/<server>/<collection> after ingest.
`)+"\n")
}

func writeExportHelp(stdout io.Writer) {
	fmt.Fprint(stdout, strings.TrimSpace(`Usage:
  clogs export --db <database> [--format ndjson|csv] [--output <file>] [--source <label>] [--include-raw]

Export events in deterministic chronological/source order. Output defaults to
stdout; --output writes a user-only file. Raw evidence is omitted by default and
is included only with --include-raw. --source limits events to one source label.
`)+"\n")
}
func writeStatsHelp(stdout io.Writer) {
	fmt.Fprint(stdout, strings.TrimSpace(`Usage:
  clogs stats --db <database> [--source <label>] [--json]

Summarize canonical SQLite events. --source limits every count to one source;
--json emits a machine-readable deterministic summary. Human output is stdout.
`)+"\n")
}
func writeQueryHelp(stdout io.Writer) {
	fmt.Fprint(stdout, strings.TrimSpace(`Usage:
  clogs query --db <database> --around <RFC3339> [--server <ssh-destination>] [--before 2m] [--after 2m] [--format human|json]

Display a deterministic merged timeline and elapsed-time analyses. Filters:
--source, --family, --severity, --status (exact or 5xx), --route, --site, and
--signature. Analysis controls: --bucket, --quiet-period, --pre-window, and
--correlation-window. Analysis baselines use the selected time/source window;
other filters limit only the displayed timeline. Window end is clamped to the
latest available event for the selected source, and --around must not be after
that event. Temporal proximity is reported as correlation and never as causation.
If --server is set and --db is omitted, Clogs uses the latest inferred
collection under paths.db_root (or --out/downloads when db_root is unset) and
defaults --source to the server alias.
`)+"\n")
}

func writeReportHelp(stdout io.Writer) {
	fmt.Fprint(stdout, strings.TrimSpace(`Usage:
  clogs report incident --db <database> --around <RFC3339> --timezone <IANA-name> --output <file.html> [options]

Generate a deterministic, self-contained HTML incident report from the same
merged analysis contract used by clogs query. The report includes an event and
failure timeline, route impact, exception-to-failure proximity, an affected-site
heatmap, and expandable source evidence. Required --timezone controls display
only; timestamps were normalized when ingested.

Window options: --before, --after, and --bucket. Analysis options:
--quiet-period, --pre-window, and --correlation-window. --source limits the
report to one source label; --title replaces the default report heading. Window
end is clamped to the latest available event for the selected source, and
--around must not be after that event.
If --server is set and --db is omitted, Clogs uses the latest inferred
collection under paths.db_root (or --out/downloads when db_root is unset),
defaults --timezone from server config when available, and falls back to the
server alias for --source. If --output is omitted with --server, Clogs writes
to paths.reports_root/incident/<server>/<date>/<utc-stamp>-incident.html.
`)+"\n")
}

func writeRemoteHelp(stdout io.Writer) {
	fmt.Fprint(stdout, strings.TrimSpace(`Usage:
  clogs remote list <ssh-destination> --dir <remote-directory> [time-window] [--pattern <glob>...]
  clogs remote fetch <ssh-destination> --dir <remote-directory> [time-window] [--out <directory>] [--pattern <glob>...]

List or download matching regular files using the workstation's OpenSSH SFTP
configuration. Authentication, host-key verification, keys, agents, aliases,
and ProxyJump settings remain under OpenSSH's control.

Default patterns:
  application.log*
  access_log*.log
  catalina.*.log

One or more --pattern flags replace the default patterns. Fetch writes a new
timestamped collection below --out (default: ./downloads). Failed temporary
downloads are removed; a manifest records every completed or failed transfer.
If a nearest clogs.yml is present in the current directory or one of its
parents, its remote defaults apply first; explicit flags always override them.

Modification-time windows:
  --since 6h                     modified during the last six hours
  --after 21:30 --before 23:15   times today in the selected timezone
  --after "2026-08-06 21:30"      local date and time
  --on 2026-08-06                the complete local calendar day

Time-only values mean today; date-only boundaries mean midnight at the start of
that date. RFC3339 values remain supported. Inputs without an offset use the
workstation timezone unless --timezone or --tz supplies an IANA name. --on,
--since, and --after/--before are mutually exclusive. Filters apply to remote
modification times, not timestamps inside the logs.
`)+"\n")
}

func writeVersionHelp(stdout io.Writer) {
	fmt.Fprint(stdout, strings.TrimSpace(`Usage:
  clogs version

Print the Clogs build version. Source builds report "dev" unless VERSION is
provided to make build.
`)+"\n")
}
