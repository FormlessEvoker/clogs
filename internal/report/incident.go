package report

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"text/template"
	"time"
)

const maxIncidentEvidence = 500

type IncidentHTMLConfig struct {
	Timezone string
	Title    string
}

type incidentView struct {
	Title, Timezone, Window, Around, Source, Bucket, Correlation string
	BaselineSize, HTTPRequests, HTTPFailures, OOMEvents          int
	OOMOnsets, CorrelatedOnsets, EvidenceTotal                   int
	IncidentDuration, FirstFailure, LastFailure, Recovery        string
	Observation                                                  string
	OverviewSVG, CorrelationSVG, HeatmapSVG                      string
	Routes                                                       []incidentRoute
	RoutePressure                                                []incidentPressureSignal
	RequestBursts                                                []incidentPressureSignal
	Evidence                                                     []incidentEvidence
	EvidenceLimited                                              bool
}

type incidentRoute struct {
	Name, Width, Rate  string
	Requests, Failures int
}
type incidentPressureSignal struct {
	Name, Ratio, ByteRatio               string
	OnsetCount, SpikeCount               int
	PreRequests, PriorRequests           int
	PreResponseBytes, PriorResponseBytes string
}

type incidentEvidence struct {
	Time, Family, Kind, Summary, Source, Details string
}

type incidentBucket struct {
	Start                         time.Time
	Requests, Failures, OOMEvents int
}

type incidentSite struct {
	Name   string
	Counts []int
	Total  int
}

type correlationPoint struct {
	Delta time.Duration
}

// WriteIncidentHTML renders a deterministic, self-contained incident report.
// It consumes QueryReport so the visualization uses the same analysis contract
// as the query command.
func WriteIncidentHTML(output io.Writer, analysis QueryReport, config IncidentHTMLConfig) error {
	location, err := time.LoadLocation(config.Timezone)
	if err != nil {
		return fmt.Errorf("load display timezone: %w", err)
	}
	view, err := buildIncidentView(analysis, location, config)
	if err != nil {
		return err
	}
	return incidentTemplate.Execute(output, view)
}

func buildIncidentView(analysis QueryReport, location *time.Location, config IncidentHTMLConfig) (incidentView, error) {
	start, err := time.Parse(time.RFC3339Nano, analysis.WindowStart)
	if err != nil {
		return incidentView{}, fmt.Errorf("parse analysis start: %w", err)
	}
	end, err := time.Parse(time.RFC3339Nano, analysis.WindowEnd)
	if err != nil {
		return incidentView{}, fmt.Errorf("parse analysis end: %w", err)
	}
	bucketSize, err := time.ParseDuration(analysis.Bucket)
	if err != nil || bucketSize <= 0 {
		return incidentView{}, fmt.Errorf("invalid analysis bucket %q", analysis.Bucket)
	}
	correlationWindow, err := time.ParseDuration(analysis.CorrelationWindow)
	if err != nil || correlationWindow < 0 {
		return incidentView{}, fmt.Errorf("invalid correlation window %q", analysis.CorrelationWindow)
	}
	if !end.After(start) {
		return incidentView{}, fmt.Errorf("analysis window must have positive duration")
	}
	if bucketCount := int(math.Ceil(float64(end.Sub(start)) / float64(bucketSize))); bucketCount > 2000 {
		return incidentView{}, fmt.Errorf("analysis produces %d buckets; increase --bucket to keep the report at or below 2000", bucketCount)
	}

	title := config.Title
	if title == "" {
		title = "Clogs incident report"
	}
	view := incidentView{
		Title: title, Timezone: config.Timezone, Bucket: analysis.Bucket,
		Correlation: analysis.CorrelationWindow, BaselineSize: analysis.BaselineSize,
		Window: formatLocal(start, location) + " – " + formatLocal(end, location),
	}
	if around, parseErr := time.Parse(time.RFC3339Nano, analysis.Around); parseErr == nil {
		view.Around = formatLocal(around, location)
	}
	view.Source = analysis.Filters["source"]
	if view.Source == "" {
		view.Source = "all sources"
	}

	buckets := makeIncidentBuckets(start, end, bucketSize)
	var failures, oomEvents []Event
	var shutdowns, startups []Event
	for _, event := range analysis.Timeline {
		at := time.Unix(0, event.OccurredAtNS)
		index := int(at.Sub(start) / bucketSize)
		if index >= 0 && index < len(buckets) {
			if event.Status != nil {
				buckets[index].Requests++
				view.HTTPRequests++
				if *event.Status >= 500 {
					buckets[index].Failures++
					view.HTTPFailures++
					failures = append(failures, event)
				}
			}
			if event.ExceptionClass == "java.lang.OutOfMemoryError" {
				buckets[index].OOMEvents++
				view.OOMEvents++
				oomEvents = append(oomEvents, event)
			}
		}
		if isShutdown(event) {
			shutdowns = append(shutdowns, event)
		}
		if isStartup(event) {
			startups = append(startups, event)
		}
	}

	if len(failures) > 0 {
		first := time.Unix(0, failures[0].OccurredAtNS)
		last := time.Unix(0, failures[len(failures)-1].OccurredAtNS)
		view.FirstFailure = formatLocal(first, location)
		view.LastFailure = formatLocal(last, location)
		view.IncidentDuration = formatDuration(last.Sub(first))
	} else {
		view.FirstFailure, view.LastFailure, view.IncidentDuration = "None", "None", "n/a"
	}
	if len(startups) > 0 {
		view.Recovery = formatLocal(time.Unix(0, startups[len(startups)-1].OccurredAtNS), location)
	} else {
		view.Recovery = "Not observed"
	}

	var points []correlationPoint
	for _, onset := range analysis.Onsets {
		if onset.ExceptionClass != "java.lang.OutOfMemoryError" {
			continue
		}
		view.OOMOnsets++
		onsetTime, parseErr := time.Parse(time.RFC3339Nano, onset.OccurredAt)
		if parseErr != nil {
			continue
		}
		if delta, ok := nearestFailure(onsetTime, failures, correlationWindow); ok {
			view.CorrelatedOnsets++
			points = append(points, correlationPoint{Delta: delta})
		}
	}
	if view.OOMOnsets > 0 {
		view.Observation = fmt.Sprintf("%d of %d distinct out-of-memory onsets had an HTTP failure within ±%s. Temporal proximity supports correlation; inspect the evidence sequence for causal exception text.", view.CorrelatedOnsets, view.OOMOnsets, analysis.CorrelationWindow)
	} else {
		view.Observation = "No out-of-memory onset was detected in this window."
	}

	view.Routes = incidentRoutes(analysis.Timeline)
	view.RoutePressure = incidentPressureSignals(analysis.RequestSignals)
	view.RequestBursts = incidentBurstSignals(analysis.RequestBursts)
	sites := incidentSites(failures, start, bucketSize, len(buckets))
	view.OverviewSVG = overviewSVG(buckets, shutdowns, startups, start, end, location)
	view.CorrelationSVG = correlationSVG(points, correlationWindow)
	view.HeatmapSVG = heatmapSVG(sites, buckets, location)
	view.Evidence, view.EvidenceTotal = incidentEvidenceRows(analysis.Timeline, location)
	view.EvidenceLimited = view.EvidenceTotal > len(view.Evidence)
	escapeIncidentView(&view)
	return view, nil
}

func makeIncidentBuckets(start, end time.Time, size time.Duration) []incidentBucket {
	var result []incidentBucket
	for at := start; at.Before(end); at = at.Add(size) {
		result = append(result, incidentBucket{Start: at})
	}
	if len(result) == 0 {
		result = append(result, incidentBucket{Start: start})
	}
	return result
}

func nearestFailure(onset time.Time, failures []Event, window time.Duration) (time.Duration, bool) {
	var nearest time.Duration
	found := false
	for _, event := range failures {
		delta := time.Unix(0, event.OccurredAtNS).Sub(onset)
		if absDuration(delta) <= window && (!found || absDuration(delta) < absDuration(nearest)) {
			nearest, found = delta, true
		}
	}
	return nearest, found
}

func incidentRoutes(events []Event) []incidentRoute {
	type totals struct{ requests, failures int }
	values := map[string]totals{}
	for _, event := range events {
		if event.Status == nil {
			continue
		}
		name := event.RouteTemplate
		if name == "" {
			name = event.Path
		}
		if name == "" {
			name = "(unclassified)"
		}
		value := values[name]
		value.requests++
		if *event.Status >= 500 {
			value.failures++
		}
		values[name] = value
	}
	var result []incidentRoute
	maxFailures := 0
	for name, value := range values {
		if value.failures == 0 {
			continue
		}
		if value.failures > maxFailures {
			maxFailures = value.failures
		}
		result = append(result, incidentRoute{Name: name, Requests: value.requests, Failures: value.failures, Rate: fmt.Sprintf("%.1f%%", float64(value.failures)*100/float64(value.requests))})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Failures != result[j].Failures {
			return result[i].Failures > result[j].Failures
		}
		return result[i].Name < result[j].Name
	})
	for index := range result {
		result[index].Width = fmt.Sprintf("%.1f%%", float64(result[index].Failures)*100/float64(maxFailures))
	}
	return result
}

func incidentSites(failures []Event, start time.Time, size time.Duration, bucketCount int) []incidentSite {
	values := map[string][]int{}
	for _, event := range failures {
		name := event.Site
		if name == "" {
			name = "(no site)"
		}
		counts := values[name]
		if counts == nil {
			counts = make([]int, bucketCount)
		}
		index := int(time.Unix(0, event.OccurredAtNS).Sub(start) / size)
		if index >= 0 && index < len(counts) {
			counts[index]++
		}
		values[name] = counts
	}
	result := make([]incidentSite, 0, len(values))
	for name, counts := range values {
		total := 0
		for _, count := range counts {
			total += count
		}
		result = append(result, incidentSite{Name: name, Counts: counts, Total: total})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Total != result[j].Total {
			return result[i].Total > result[j].Total
		}
		return result[i].Name < result[j].Name
	})
	return result
}

func incidentPressureSignals(signals []RequestSignal) []incidentPressureSignal {
	const maxSignals = 12
	if len(signals) > maxSignals {
		signals = signals[:maxSignals]
	}
	result := make([]incidentPressureSignal, 0, len(signals))
	for _, signal := range signals {
		result = append(result, incidentPressureSignal{
			Name:               signal.Route,
			OnsetCount:         signal.OnsetCount,
			SpikeCount:         signal.SpikeCount,
			PreRequests:        signal.PreRequests,
			PriorRequests:      signal.PriorRequests,
			PreResponseBytes:   formatBytes(signal.PreResponseBytes),
			PriorResponseBytes: formatBytes(signal.PriorResponseBytes),
			Ratio:              formatRatio(signal.PreToPrior),
			ByteRatio:          formatRatio(signal.BytesPreToPrior),
		})
	}
	return result
}

func incidentBurstSignals(signals []RequestBurst) []incidentPressureSignal {
	const maxSignals = 12
	if len(signals) > maxSignals {
		signals = signals[:maxSignals]
	}
	result := make([]incidentPressureSignal, 0, len(signals))
	for _, signal := range signals {
		result = append(result, incidentPressureSignal{
			Name:               signal.Fingerprint,
			OnsetCount:         signal.OnsetCount,
			SpikeCount:         signal.SpikeCount,
			PreRequests:        signal.PreRequests,
			PriorRequests:      signal.PriorRequests,
			PreResponseBytes:   formatBytes(signal.PreResponseBytes),
			PriorResponseBytes: formatBytes(signal.PriorResponseBytes),
			Ratio:              formatRatio(signal.PreToPrior),
			ByteRatio:          formatRatio(signal.BytesPreToPrior),
		})
	}
	return result
}

func incidentEvidenceRows(events []Event, location *time.Location) ([]incidentEvidence, int) {
	var rows []incidentEvidence
	for _, event := range events {
		kind := ""
		summary := firstLine(event.Message)
		switch {
		case event.Status != nil && *event.Status >= 500:
			kind = fmt.Sprintf("HTTP %d", *event.Status)
			summary = strings.TrimSpace(event.Method + " " + event.RouteTemplate)
			if event.Site != "" {
				summary += " · " + event.Site
			}
		case event.ExceptionClass != "":
			kind = "Exception"
		case isShutdown(event):
			kind = "Shutdown"
		case isStartup(event):
			kind = "Recovery"
		default:
			continue
		}
		details := event.Message
		if event.StackTrace != "" && !strings.Contains(details, event.StackTrace) {
			details += "\n" + event.StackTrace
		}
		rows = append(rows, incidentEvidence{
			Time: formatLocal(time.Unix(0, event.OccurredAtNS), location), Family: event.Family,
			Kind: kind, Summary: summary, Source: fmt.Sprintf("%s:%d", event.SourcePath, event.SourceLineStart), Details: details,
		})
	}
	total := len(rows)
	if len(rows) > maxIncidentEvidence {
		rows = rows[:maxIncidentEvidence]
	}
	return rows, total
}

func overviewSVG(buckets []incidentBucket, shutdowns, startups []Event, start, end time.Time, location *time.Location) string {
	const width, height = 1100, 300
	const left, right, top, bottom = 54.0, 18.0, 24.0, 44.0
	plotWidth, plotHeight := width-left-right, height-top-bottom
	maxRequests, maxFailures, maxOOM := 1, 1, 1
	for _, bucket := range buckets {
		maxRequests = maxInt(maxRequests, bucket.Requests)
		maxFailures = maxInt(maxFailures, bucket.Failures)
		maxOOM = maxInt(maxOOM, bucket.OOMEvents)
	}
	barWidth := plotWidth / float64(len(buckets))
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="chart" role="img" aria-label="HTTP requests, failures, out-of-memory events, and service lifecycle over time" viewBox="0 0 %d %d">`, width, height)
	fmt.Fprintf(&b, `<line class="axis" x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f"/>`, left, top+plotHeight, left+plotWidth, top+plotHeight)
	for index, bucket := range buckets {
		x := left + float64(index)*barWidth
		requestHeight := float64(bucket.Requests) / float64(maxRequests) * plotHeight
		failureHeight := float64(bucket.Failures) / float64(maxFailures) * plotHeight * .72
		oomHeight := float64(bucket.OOMEvents) / float64(maxOOM) * plotHeight * .5
		fmt.Fprintf(&b, `<rect class="requests" x="%.2f" y="%.2f" width="%.2f" height="%.2f"><title>%s: %d requests</title></rect>`, x, top+plotHeight-requestHeight, math.Max(barWidth-.5, .5), requestHeight, escapeHTML(formatLocal(bucket.Start, location)), bucket.Requests)
		if bucket.Failures > 0 {
			fmt.Fprintf(&b, `<rect class="failures" x="%.2f" y="%.2f" width="%.2f" height="%.2f"><title>%s: %d failures</title></rect>`, x, top+plotHeight-failureHeight, math.Max(barWidth-.5, .5), failureHeight, escapeHTML(formatLocal(bucket.Start, location)), bucket.Failures)
		}
		if bucket.OOMEvents > 0 {
			fmt.Fprintf(&b, `<circle class="oom" cx="%.2f" cy="%.2f" r="4"><title>%s: %d out-of-memory events</title></circle>`, x+barWidth/2, top+plotHeight-oomHeight, escapeHTML(formatLocal(bucket.Start, location)), bucket.OOMEvents)
		}
	}
	for _, marker := range shutdowns {
		x := left + float64(marker.OccurredAtNS-start.UnixNano())/float64(end.UnixNano()-start.UnixNano())*plotWidth
		fmt.Fprintf(&b, `<line class="shutdown" x1="%.2f" y1="%.1f" x2="%.2f" y2="%.1f"><title>Service shutdown: %s</title></line>`, x, top, x, top+plotHeight, escapeHTML(formatLocal(time.Unix(0, marker.OccurredAtNS), location)))
	}
	for _, marker := range startups {
		x := left + float64(marker.OccurredAtNS-start.UnixNano())/float64(end.UnixNano()-start.UnixNano())*plotWidth
		fmt.Fprintf(&b, `<line class="startup" x1="%.2f" y1="%.1f" x2="%.2f" y2="%.1f"><title>Service recovery: %s</title></line>`, x, top, x, top+plotHeight, escapeHTML(formatLocal(time.Unix(0, marker.OccurredAtNS), location)))
	}
	fmt.Fprintf(&b, `<text class="label" x="%.1f" y="%d">%s</text><text class="label end" x="%.1f" y="%d">%s</text>`, left, height-12, escapeHTML(start.In(location).Format("15:04:05")), left+plotWidth, height-12, escapeHTML(end.In(location).Format("15:04:05 MST")))
	b.WriteString(`<g class="legend"><rect class="requests" x="58" y="7" width="12" height="8"/><text x="75" y="15">requests</text><rect class="failures" x="150" y="7" width="12" height="8"/><text x="167" y="15">5xx</text><circle class="oom" cx="225" cy="11" r="4"/><text x="235" y="15">OOM</text><line class="shutdown" x1="285" y1="5" x2="285" y2="17"/><text x="294" y="15">shutdown</text><line class="startup" x1="375" y1="5" x2="375" y2="17"/><text x="384" y="15">recovery</text></g></svg>`)
	return b.String()
}

func correlationSVG(points []correlationPoint, window time.Duration) string {
	const width, height = 700, 220
	const left, right, top, bottom = 50.0, 24.0, 24.0, 42.0
	plotWidth, plotHeight := width-left-right, height-top-bottom
	var b strings.Builder
	b.WriteString(`<svg class="chart correlation" role="img" aria-label="Nearest HTTP failure relative to each out-of-memory onset" viewBox="0 0 700 220">`)
	fmt.Fprintf(&b, `<line class="axis" x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f"/>`, left, top+plotHeight, left+plotWidth, top+plotHeight)
	middle := left + plotWidth/2
	fmt.Fprintf(&b, `<line class="zero" x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f"/><text class="label" x="%.1f" y="18">OOM onset</text>`, middle, top, middle, top+plotHeight, middle-30)
	if window > 0 {
		for index, point := range points {
			x := middle + float64(point.Delta)/float64(window)*(plotWidth/2)
			y := top + 10 + float64(index%12)*(plotHeight-20)/11
			fmt.Fprintf(&b, `<circle class="correlation-point" cx="%.2f" cy="%.2f" r="4"><title>Nearest failure: %s from OOM onset</title></circle>`, x, y, escapeHTML(signedDuration(point.Delta)))
		}
	}
	fmt.Fprintf(&b, `<text class="label" x="%.1f" y="%d">−%s</text><text class="label end" x="%.1f" y="%d">+%s</text></svg>`, left, height-12, escapeHTML(window.String()), left+plotWidth, height-12, escapeHTML(window.String()))
	return b.String()
}

func heatmapSVG(sites []incidentSite, buckets []incidentBucket, location *time.Location) string {
	if len(sites) == 0 {
		return `<svg class="chart" role="img" aria-label="No affected sites" viewBox="0 0 700 90"><text class="empty" x="20" y="48">No site-attributed HTTP failures in this window.</text></svg>`
	}
	const width, left, right, rowHeight = 1100, 190.0, 45.0, 25.0
	height := 48 + float64(len(sites))*rowHeight + 38
	cellWidth := (width - left - right) / float64(len(buckets))
	maxCount := 1
	for _, site := range sites {
		for _, count := range site.Counts {
			maxCount = maxInt(maxCount, count)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="chart heatmap" role="img" aria-label="HTTP failures by site and time bucket" viewBox="0 0 %d %.0f">`, width, height)
	for row, site := range sites {
		y := 35 + float64(row)*rowHeight
		fmt.Fprintf(&b, `<text class="site-label" x="%.1f" y="%.1f">%s (%d)</text>`, left-10, y+16, escapeHTML(site.Name), site.Total)
		for column, count := range site.Counts {
			x := left + float64(column)*cellWidth
			opacity := .06
			if count > 0 {
				opacity = .2 + .8*float64(count)/float64(maxCount)
			}
			fmt.Fprintf(&b, `<rect class="heat-cell" x="%.2f" y="%.2f" width="%.2f" height="%.2f" opacity="%.2f"><title>%s · %s · %d failure(s)</title></rect>`, x, y, math.Max(cellWidth-.6, .5), rowHeight-2, opacity, escapeHTML(site.Name), escapeHTML(formatLocal(buckets[column].Start, location)), count)
		}
	}
	fmt.Fprintf(&b, `<text class="label" x="%.1f" y="%.1f">%s</text><text class="label end" x="%.1f" y="%.1f">%s</text></svg>`, left, height-10, escapeHTML(buckets[0].Start.In(location).Format("15:04")), width-right, height-10, escapeHTML(buckets[len(buckets)-1].Start.In(location).Format("15:04 MST")))
	return b.String()
}

func escapeIncidentView(view *incidentView) {
	for _, value := range []*string{&view.Title, &view.Timezone, &view.Window, &view.Around, &view.Source, &view.Bucket, &view.Correlation, &view.IncidentDuration, &view.FirstFailure, &view.LastFailure, &view.Recovery, &view.Observation} {
		*value = escapeHTML(*value)
	}
	for index := range view.Routes {
		view.Routes[index].Name = escapeHTML(view.Routes[index].Name)
	}
	for index := range view.RoutePressure {
		view.RoutePressure[index].Name = escapeHTML(view.RoutePressure[index].Name)
		view.RoutePressure[index].Ratio = escapeHTML(view.RoutePressure[index].Ratio)
		view.RoutePressure[index].PreResponseBytes = escapeHTML(view.RoutePressure[index].PreResponseBytes)
		view.RoutePressure[index].PriorResponseBytes = escapeHTML(view.RoutePressure[index].PriorResponseBytes)
	}
	for index := range view.RequestBursts {
		view.RequestBursts[index].Name = escapeHTML(view.RequestBursts[index].Name)
		view.RequestBursts[index].Ratio = escapeHTML(view.RequestBursts[index].Ratio)
		view.RequestBursts[index].PreResponseBytes = escapeHTML(view.RequestBursts[index].PreResponseBytes)
		view.RequestBursts[index].PriorResponseBytes = escapeHTML(view.RequestBursts[index].PriorResponseBytes)
	}
	for index := range view.Evidence {
		row := &view.Evidence[index]
		for _, value := range []*string{&row.Time, &row.Family, &row.Kind, &row.Summary, &row.Source, &row.Details} {
			*value = escapeHTML(*value)
		}
	}
}

func escapeHTML(value string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;", "'", "&#39;").Replace(value)
}

func isShutdown(event Event) bool {
	return strings.Contains(event.Logger, "StandardService.stopInternal") || strings.HasPrefix(event.Message, "Stopping service")
}

func isStartup(event Event) bool {
	return strings.Contains(event.Logger, "Catalina.start") && strings.Contains(event.Message, "Server startup")
}

func formatLocal(value time.Time, location *time.Location) string {
	return value.In(location).Format("2006-01-02 15:04:05.000 MST")
}

func firstLine(value string) string {
	if index := strings.IndexByte(value, '\n'); index >= 0 {
		return value[:index]
	}
	return value
}

func formatDuration(value time.Duration) string {
	if value < time.Minute {
		return value.Round(time.Millisecond).String()
	}
	return value.Round(time.Second).String()
}

func formatBytes(value int64) string {
	if value <= 0 {
		return "0 B"
	}
	units := []struct {
		name string
		size float64
	}{{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}}
	for _, unit := range units {
		if value >= int64(unit.size) {
			return fmt.Sprintf("%.1f %s", float64(value)/unit.size, unit.name)
		}
	}
	return fmt.Sprintf("%d B", value)
}

func formatRatio(value float64) string {
	if value <= 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.2fx", value)
}

func signedDuration(value time.Duration) string {
	if value >= 0 {
		return "+" + value.Round(time.Millisecond).String()
	}
	return "−" + (-value).Round(time.Millisecond).String()
}

func absDuration(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

var incidentTemplate = template.Must(template.New("incident").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}}</title>
<style>
:root{color-scheme:dark;--bg:#091016;--panel:#111b23;--panel2:#16232d;--text:#e9f0f4;--muted:#93a6b2;--grid:#334653;--blue:#4aa8d8;--red:#f05d5e;--purple:#bd78ed;--amber:#f5b942;--green:#55c995}*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);font:15px/1.45 ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}main{max-width:1480px;margin:auto;padding:36px 28px 64px}header{display:flex;justify-content:space-between;gap:24px;align-items:flex-end;margin-bottom:24px}h1{font-size:30px;margin:0 0 5px;letter-spacing:-.03em}h2{font-size:18px;margin:0 0 16px}p{margin:4px 0;color:var(--muted)}.timezone{color:var(--amber);font-weight:650}.cards{display:grid;grid-template-columns:repeat(5,minmax(140px,1fr));gap:12px;margin:20px 0}.card,.panel{background:linear-gradient(145deg,var(--panel2),var(--panel));border:1px solid #243640;border-radius:12px;box-shadow:0 10px 35px #0003}.card{padding:16px}.metric{font-size:27px;font-weight:750;letter-spacing:-.03em}.caption{color:var(--muted);font-size:12px;text-transform:uppercase;letter-spacing:.06em}.panel{padding:20px;margin:12px 0}.grid{display:grid;grid-template-columns:1.05fr .95fr;gap:12px}.chart{width:100%;height:auto;overflow:visible}.axis{stroke:var(--grid);stroke-width:1}.requests{fill:var(--blue);opacity:.25}.failures{fill:var(--red);opacity:.85}.oom{fill:var(--purple);stroke:#f0d9ff;stroke-width:.5}.shutdown{stroke:var(--amber);stroke-width:2;stroke-dasharray:4 3}.startup{stroke:var(--green);stroke-width:2}.zero{stroke:var(--purple);stroke-width:1.5}.correlation-point{fill:var(--red);opacity:.82}.label,.legend text,.site-label,.empty{fill:var(--muted);font-size:12px}.label.end{text-anchor:end}.site-label{text-anchor:end}.heat-cell{fill:var(--red)}.route,.signal{display:grid;grid-template-columns:minmax(230px,1fr) 82px 72px 92px 92px 86px 130px;gap:12px;align-items:center;margin:10px 0}.route-name{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.bar{height:9px;background:#263843;border-radius:9px;overflow:hidden}.bar span{display:block;height:100%;background:var(--red);border-radius:9px}.number{text-align:right;font-variant-numeric:tabular-nums}.observation{padding:12px 14px;border-left:3px solid var(--purple);background:#0c151c;color:#c9d6dd;margin-top:12px}.evidence{border-top:1px solid #273a45;padding:9px 0}.evidence summary{display:grid;grid-template-columns:205px 82px 100px minmax(220px,1fr) 230px;gap:10px;cursor:pointer;align-items:start}.family,.kind{font-size:12px;text-transform:uppercase;letter-spacing:.04em}.family{color:var(--blue)}.kind{color:var(--red)}.source{color:var(--muted);overflow-wrap:anywhere}.details{white-space:pre-wrap;overflow-wrap:anywhere;background:#071016;border:1px solid #22343e;padding:12px;border-radius:7px;max-height:360px;overflow:auto}footer{margin-top:22px;color:var(--muted);font-size:12px}@media(max-width:900px){main{padding:22px 14px}.cards{grid-template-columns:repeat(2,1fr)}.grid{grid-template-columns:1fr}.route,.signal{grid-template-columns:1fr 76px 66px 58px}.evidence summary{grid-template-columns:1fr}.source{display:none}}@media print{:root{color-scheme:light;--bg:#fff;--panel:#fff;--panel2:#fff;--text:#101820;--muted:#52636d;--grid:#ccd5da}main{padding:0}.panel,.card{box-shadow:none;break-inside:avoid}.evidence:not([open]) .details{display:none}}
</style>
</head>
<body><main>
<header><div><h1>{{.Title}}</h1><p>{{.Window}}</p><p>Centered at {{.Around}} · source: {{.Source}}</p></div><div><div class="caption">Display timezone</div><div class="timezone">{{.Timezone}}</div></div></header>
<section class="cards" aria-label="Incident summary">
<div class="card"><div class="metric">{{.IncidentDuration}}</div><div class="caption">Failure span</div></div>
<div class="card"><div class="metric">{{.HTTPFailures}}</div><div class="caption">HTTP 5xx / {{.HTTPRequests}} requests</div></div>
<div class="card"><div class="metric">{{.OOMEvents}}</div><div class="caption">OOM records / {{.OOMOnsets}} onsets</div></div>
<div class="card"><div class="metric">{{.CorrelatedOnsets}} / {{.OOMOnsets}}</div><div class="caption">OOM onsets near failures</div></div>
<div class="card"><div class="metric">{{.BaselineSize}}</div><div class="caption">Events analyzed</div></div>
</section>
<section class="panel"><h2>Unified incident timeline</h2><p>Elapsed-time buckets: {{.Bucket}}. Request, failure, and OOM tracks use independent scales so low-frequency failures remain visible.</p>{{.OverviewSVG}}<p>First failure: {{.FirstFailure}} · Last failure: {{.LastFailure}} · Recovery: {{.Recovery}}</p></section>
<div class="grid">
<section class="panel"><h2>Route impact</h2><p>Failure counts and rates include every HTTP request in the selected window.</p>{{range .Routes}}<div class="route"><div><div class="route-name" title="{{.Name}}">{{.Name}}</div><div class="bar"><span style="width:{{.Width}}"></span></div></div><div class="number">{{.Failures}} failed</div><div class="number">{{.Requests}} total</div><div class="number">{{.Rate}}</div></div>{{else}}<p>No HTTP failures in this window.</p>{{end}}</section>
<section class="panel"><h2>OOM-to-failure proximity</h2><p>Each point is the nearest HTTP 5xx response to one OOM onset within ±{{.Correlation}}.</p>{{.CorrelationSVG}}<div class="observation">{{.Observation}}</div></section>
</div>
<section class="panel"><h2>Pre-OOM route pressure</h2><p>Counts every request, including successes. Compares the pre-window with the immediately preceding window of equal size.</p>{{range .RoutePressure}}<div class="signal"><div class="route-name" title="{{.Name}}">{{.Name}}</div><div class="number">{{.OnsetCount}} onset</div><div class="number">{{.SpikeCount}} spike</div><div class="number">{{.PreRequests}}</div><div class="number">{{.PriorRequests}}</div><div class="number">{{.Ratio}}</div><div class="number" title="pre / prior response bytes">{{.PreResponseBytes}} / {{.PriorResponseBytes}}</div></div>{{else}}<p>No route-pressure signals were detected for OOM onsets in this window.</p>{{end}}</section>
<section class="panel"><h2>Repeated exact calls</h2><p>Same method, target, and site repeated rapidly before OOM onsets.</p>{{range .RequestBursts}}<div class="signal"><div class="route-name" title="{{.Name}}">{{.Name}}</div><div class="number">{{.OnsetCount}} onset</div><div class="number">{{.SpikeCount}} spike</div><div class="number">{{.PreRequests}}</div><div class="number">{{.PriorRequests}}</div><div class="number">{{.Ratio}}</div><div class="number" title="pre / prior response bytes">{{.PreResponseBytes}} / {{.PriorResponseBytes}}</div></div>{{else}}<p>No repeated exact-call bursts were detected for OOM onsets in this window.</p>{{end}}</section>
<section class="panel"><h2>Affected sites over time</h2><p>Rows are ordered by total failures. Darker cells contain more failures in the {{.Bucket}} bucket.</p>{{.HeatmapSVG}}</section>
<section class="panel"><h2>Evidence sequence</h2><p>Failures, exceptions, shutdown, and recovery events in deterministic source order. Expand a row for the recorded message or stack trace.</p>{{range .Evidence}}<details class="evidence"><summary><time>{{.Time}}</time><span class="family">{{.Family}}</span><span class="kind">{{.Kind}}</span><span>{{.Summary}}</span><span class="source">{{.Source}}</span></summary><pre class="details">{{.Details}}</pre></details>{{else}}<p>No incident evidence selected in this window.</p>{{end}}{{if .EvidenceLimited}}<p>Showing the first {{len .Evidence}} of {{.EvidenceTotal}} evidence events.</p>{{end}}</section>
<footer>Generated by Clogs from the same merged query and analysis contract used by <code>clogs query</code>. Temporal proximity is correlation, not proof of causation. All times are displayed in {{.Timezone}}.</footer>
</main></body></html>`))
