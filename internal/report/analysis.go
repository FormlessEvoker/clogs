package report

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

type QueryOptions struct {
	Around                                                           time.Time
	Before, After, Bucket, QuietPeriod, PreWindow, CorrelationWindow time.Duration
	Source, Family, Severity, Status, Route, Site, Signature         string
}
type QueryReport struct {
	WindowStart       string            `json:"window_start"`
	WindowEnd         string            `json:"window_end"`
	Around            string            `json:"around"`
	Filters           map[string]string `json:"filters"`
	Bucket            string            `json:"bucket"`
	QuietPeriod       string            `json:"quiet_period"`
	PreWindow         string            `json:"pre_window"`
	CorrelationWindow string            `json:"correlation_window"`
	SampleSize        int               `json:"sample_size"`
	BaselineSize      int               `json:"baseline_size"`
	Timeline          []Event           `json:"timeline"`
	Buckets           []Bucket          `json:"buckets"`
	Routes            []RouteRate       `json:"route_failure_rates"`
	RequestSignals    []RequestSignal   `json:"request_signals"`
	RequestBursts     []RequestBurst    `json:"request_bursts"`
	Signatures        []Count           `json:"signatures"`
	Onsets            []Onset           `json:"onsets"`
}
type Bucket struct {
	Start  string         `json:"start"`
	End    string         `json:"end"`
	Counts map[string]int `json:"counts"`
}
type RouteRate struct {
	Route       string  `json:"route"`
	Site        string  `json:"site,omitempty"`
	Requests    int     `json:"requests"`
	Failures    int     `json:"failures"`
	FailureRate float64 `json:"failure_rate"`
}
type RequestSignal struct {
	Route              string  `json:"route"`
	OnsetCount         int     `json:"onset_count"`
	SpikeCount         int     `json:"spike_count"`
	PreRequests        int     `json:"pre_requests"`
	PriorRequests      int     `json:"prior_requests"`
	PreResponseBytes   int64   `json:"pre_response_bytes"`
	PriorResponseBytes int64   `json:"prior_response_bytes"`
	PreToPrior         float64 `json:"pre_to_prior_ratio"`
	BytesPreToPrior    float64 `json:"bytes_pre_to_prior_ratio"`
}
type RequestBurst struct {
	Fingerprint        string  `json:"fingerprint"`
	OnsetCount         int     `json:"onset_count"`
	SpikeCount         int     `json:"spike_count"`
	PreRequests        int     `json:"pre_requests"`
	PriorRequests      int     `json:"prior_requests"`
	PreResponseBytes   int64   `json:"pre_response_bytes"`
	PriorResponseBytes int64   `json:"prior_response_bytes"`
	PreToPrior         float64 `json:"pre_to_prior_ratio"`
	BytesPreToPrior    float64 `json:"bytes_pre_to_prior_ratio"`
}
type Onset struct {
	OccurredAt           string         `json:"occurred_at"`
	Family               string         `json:"family"`
	Signature            string         `json:"signature"`
	ExceptionClass       string         `json:"exception_class,omitempty"`
	PreCounts            map[string]int `json:"pre_counts"`
	PreSignatures        []Count        `json:"pre_signatures"`
	NearbyRequests       int            `json:"nearby_requests"`
	NearbyFailures       int            `json:"nearby_failures"`
	CorrelationStatement string         `json:"correlation_statement,omitempty"`
}

func Analyze(ctx context.Context, db *sql.DB, options QueryOptions) (QueryReport, error) {
	start, requestedEnd := options.Around.Add(-options.Before), options.Around.Add(options.After)
	effectiveEnd := requestedEnd
	latestOccurredAtNS, err := latestOccurredAtNS(ctx, db, options.Source)
	if err != nil {
		return QueryReport{}, err
	}
	if latestOccurredAtNS.Valid {
		latest := time.Unix(0, latestOccurredAtNS.Int64)
		if options.Around.After(latest) {
			return QueryReport{}, fmt.Errorf("--around must not be after the latest available event for the selected source: around=%s latest=%s", options.Around.UTC().Format(time.RFC3339Nano), latest.UTC().Format(time.RFC3339Nano))
		}
		if latest.Before(effectiveEnd) {
			effectiveEnd = latest
		}
	}
	report := QueryReport{WindowStart: start.UTC().Format(time.RFC3339Nano), WindowEnd: effectiveEnd.UTC().Format(time.RFC3339Nano), Around: options.Around.UTC().Format(time.RFC3339Nano), Filters: filters(options), Bucket: options.Bucket.String(), QuietPeriod: options.QuietPeriod.String(), PreWindow: options.PreWindow.String(), CorrelationWindow: options.CorrelationWindow.String()}
	startNS, endNS := start.UnixNano(), effectiveEnd.UnixNano()
	events, err := eventsBetween(ctx, db, options.Source, false, &startNS, &endNS)
	if err != nil {
		return report, err
	}
	var baseline []Event
	for _, event := range events {
		if event.OccurredAtNS >= start.UnixNano() && event.OccurredAtNS <= effectiveEnd.UnixNano() {
			baseline = append(baseline, event)
			if matches(event, options) {
				report.Timeline = append(report.Timeline, event)
			}
		}
	}
	report.BaselineSize, report.SampleSize = len(baseline), len(report.Timeline)
	report.Buckets = buckets(baseline, start, effectiveEnd, options.Bucket)
	report.Routes = routeRates(baseline)
	report.RequestSignals = requestSignals(baseline, options)
	report.RequestBursts = requestBursts(baseline, options)
	report.Signatures = signatureCounts(baseline)
	report.Onsets = onsets(baseline, options)
	return report, nil
}

func latestOccurredAtNS(ctx context.Context, db *sql.DB, source string) (sql.NullInt64, error) {
	var latest sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT MAX(e.occurred_at_ns) FROM events e JOIN source_files sf ON sf.id=e.source_file_id WHERE (?='' OR sf.source_label=?)`, source, source).Scan(&latest); err != nil {
		return sql.NullInt64{}, err
	}
	return latest, nil
}
func filters(o QueryOptions) map[string]string {
	values := map[string]string{}
	for key, value := range map[string]string{"source": o.Source, "family": o.Family, "severity": o.Severity, "status": o.Status, "route": o.Route, "site": o.Site, "signature": o.Signature} {
		if value != "" {
			values[key] = value
		}
	}
	return values
}
func matches(e Event, o QueryOptions) bool {
	if o.Family != "" && e.Family != o.Family || o.Severity != "" && e.Severity != o.Severity || o.Route != "" && e.RouteTemplate != o.Route || o.Site != "" && e.Site != o.Site || o.Signature != "" && e.Signature != o.Signature {
		return false
	}
	if o.Status != "" {
		if e.Status == nil {
			return false
		}
		if strings.HasSuffix(o.Status, "xx") {
			class, err := strconv.Atoi(strings.TrimSuffix(o.Status, "xx"))
			if err != nil || int(*e.Status)/100 != class {
				return false
			}
		} else {
			status, err := strconv.Atoi(o.Status)
			if err != nil || int(*e.Status) != status {
				return false
			}
		}
	}
	return true
}
func buckets(events []Event, start, end time.Time, size time.Duration) []Bucket {
	if size <= 0 {
		return nil
	}
	var result []Bucket
	for at := start; at.Before(end) || at.Equal(end); at = at.Add(size) {
		stop := at.Add(size)
		if stop.After(end) {
			stop = end
		}
		counts := map[string]int{}
		for _, e := range events {
			if e.OccurredAtNS >= at.UnixNano() && (e.OccurredAtNS < stop.UnixNano() || (stop.Equal(end) && e.OccurredAtNS <= stop.UnixNano())) {
				counts[e.Family]++
			}
		}
		result = append(result, Bucket{at.UTC().Format(time.RFC3339Nano), stop.UTC().Format(time.RFC3339Nano), counts})
		if stop.Equal(end) {
			break
		}
	}
	return result
}
func routeRates(events []Event) []RouteRate {
	type key struct{ route, site string }
	totals := map[key]*RouteRate{}
	for _, e := range events {
		if e.Status == nil {
			continue
		}
		k := key{e.RouteTemplate, e.Site}
		value := totals[k]
		if value == nil {
			value = &RouteRate{Route: k.route, Site: k.site}
			totals[k] = value
		}
		value.Requests++
		if *e.Status >= 500 {
			value.Failures++
		}
	}
	result := make([]RouteRate, 0, len(totals))
	for _, v := range totals {
		v.FailureRate = float64(v.Failures) / float64(v.Requests)
		result = append(result, *v)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Failures != result[j].Failures {
			return result[i].Failures > result[j].Failures
		}
		if result[i].Route != result[j].Route {
			return result[i].Route < result[j].Route
		}
		return result[i].Site < result[j].Site
	})
	return result
}

func requestSignals(events []Event, options QueryOptions) []RequestSignal {
	signals := requestPressureSignals(events, options, requestRouteName)
	result := make([]RequestSignal, 0, len(signals))
	for _, signal := range signals {
		ratio, byteRatio := requestRatios(signal.PreRequests, signal.PriorRequests, signal.PreResponseBytes, signal.PriorResponseBytes)
		result = append(result, RequestSignal{
			Route:              signal.Name,
			OnsetCount:         signal.OnsetCount,
			SpikeCount:         signal.SpikeCount,
			PreRequests:        signal.PreRequests,
			PriorRequests:      signal.PriorRequests,
			PreResponseBytes:   signal.PreResponseBytes,
			PriorResponseBytes: signal.PriorResponseBytes,
			PreToPrior:         ratio,
			BytesPreToPrior:    byteRatio,
		})
	}
	return result
}

func requestBursts(events []Event, options QueryOptions) []RequestBurst {
	signals := requestPressureSignals(events, options, requestFingerprint)
	result := make([]RequestBurst, 0, len(signals))
	for _, signal := range signals {
		ratio, byteRatio := requestRatios(signal.PreRequests, signal.PriorRequests, signal.PreResponseBytes, signal.PriorResponseBytes)
		result = append(result, RequestBurst{
			Fingerprint:        signal.Name,
			OnsetCount:         signal.OnsetCount,
			SpikeCount:         signal.SpikeCount,
			PreRequests:        signal.PreRequests,
			PriorRequests:      signal.PriorRequests,
			PreResponseBytes:   signal.PreResponseBytes,
			PriorResponseBytes: signal.PriorResponseBytes,
			PreToPrior:         ratio,
			BytesPreToPrior:    byteRatio,
		})
	}
	return result
}

type requestPressureTotals struct {
	onset, spike, pre, prior int
	preBytes, priorBytes     int64
}

type requestPressureSignal struct {
	Name               string
	OnsetCount         int
	SpikeCount         int
	PreRequests        int
	PriorRequests      int
	PreResponseBytes   int64
	PriorResponseBytes int64
}

func requestPressureSignals(events []Event, options QueryOptions, keyFn func(Event) string) []requestPressureSignal {
	if options.PreWindow <= 0 {
		return nil
	}
	byKey := map[string]requestPressureTotals{}
	var oomOnsets []int64
	var accessEvents []Event
	var accessTimes []int64
	for _, event := range events {
		if event.Status != nil {
			accessEvents = append(accessEvents, event)
			accessTimes = append(accessTimes, event.OccurredAtNS)
		}
		if event.ExceptionClass != "java.lang.OutOfMemoryError" {
			continue
		}
		oomOnsets = append(oomOnsets, event.OccurredAtNS)
	}
	for _, onsetNS := range oomOnsets {
		preStart := onsetNS - options.PreWindow.Nanoseconds()
		priorStart := preStart - options.PreWindow.Nanoseconds()
		preCounts := map[string]requestPressureTotals{}
		priorCounts := map[string]requestPressureTotals{}
		preFrom := sort.Search(len(accessTimes), func(i int) bool { return accessTimes[i] >= preStart })
		preTo := sort.Search(len(accessTimes), func(i int) bool { return accessTimes[i] >= onsetNS })
		priorFrom := sort.Search(len(accessTimes), func(i int) bool { return accessTimes[i] >= priorStart })
		for index := preFrom; index < preTo; index++ {
			name := keyFn(accessEvents[index])
			value := preCounts[name]
			value.pre++
			value.preBytes += responseBytes(accessEvents[index])
			preCounts[name] = value
		}
		for index := priorFrom; index < preFrom; index++ {
			name := keyFn(accessEvents[index])
			value := priorCounts[name]
			value.prior++
			value.priorBytes += responseBytes(accessEvents[index])
			priorCounts[name] = value
		}
		for name, pre := range preCounts {
			prior := priorCounts[name]
			value := byKey[name]
			value.pre += pre.pre
			value.prior += prior.prior
			value.preBytes += pre.preBytes
			value.priorBytes += prior.priorBytes
			if prior.prior == 0 {
				value.onset++
			} else if pre.pre > prior.prior {
				value.spike++
			}
			byKey[name] = value
		}
		for name, prior := range priorCounts {
			if _, exists := preCounts[name]; exists {
				continue
			}
			value := byKey[name]
			value.prior += prior.prior
			value.priorBytes += prior.priorBytes
			byKey[name] = value
		}
	}
	result := make([]requestPressureSignal, 0, len(byKey))
	for name, value := range byKey {
		result = append(result, requestPressureSignal{
			Name:               name,
			OnsetCount:         value.onset,
			SpikeCount:         value.spike,
			PreRequests:        value.pre,
			PriorRequests:      value.prior,
			PreResponseBytes:   value.preBytes,
			PriorResponseBytes: value.priorBytes,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].OnsetCount != result[j].OnsetCount {
			return result[i].OnsetCount > result[j].OnsetCount
		}
		if result[i].SpikeCount != result[j].SpikeCount {
			return result[i].SpikeCount > result[j].SpikeCount
		}
		if result[i].PreRequests != result[j].PreRequests {
			return result[i].PreRequests > result[j].PreRequests
		}
		if result[i].PreResponseBytes != result[j].PreResponseBytes {
			return result[i].PreResponseBytes > result[j].PreResponseBytes
		}
		return result[i].Name < result[j].Name
	})
	return result
}

func requestRouteName(event Event) string {
	name := event.RouteTemplate
	if name == "" {
		name = event.Path
	}
	if name == "" {
		return "(unclassified)"
	}
	return name
}

func requestFingerprint(event Event) string {
	if event.Method == "" && event.RawTarget == "" && event.RouteTemplate == "" && event.Path == "" {
		return "(unclassified)"
	}
	target := event.RawTarget
	if target == "" {
		target = event.Path
	}
	if target == "" {
		target = event.RouteTemplate
	}
	parts := []string{event.Method, target}
	if event.Site != "" {
		parts = append(parts, "site="+event.Site)
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func requestRatios(pre, prior int, preBytes, priorBytes int64) (float64, float64) {
	ratio := float64(pre)
	if prior > 0 {
		ratio = float64(pre) / float64(prior)
	}
	byteRatio := float64(preBytes)
	if priorBytes > 0 {
		byteRatio = float64(preBytes) / float64(priorBytes)
	}
	return ratio, byteRatio
}

func responseBytes(event Event) int64 {
	if event.ResponseBytes == nil {
		return 0
	}
	return *event.ResponseBytes
}
func signatureCounts(events []Event) []Count {
	values := map[string]int64{}
	for _, e := range events {
		values[e.Signature]++
	}
	result := make([]Count, 0, len(values))
	for name, count := range values {
		result = append(result, Count{Name: name, Count: count})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Count != result[j].Count {
			return result[i].Count > result[j].Count
		}
		return result[i].Name < result[j].Name
	})
	return result
}
func isError(e Event) bool {
	return e.Family == "catalina" && (e.Severity == "SEVERE" || e.ExceptionClass != "") || e.Family == "jvm-multiline" && (e.Severity == "ERROR" || e.Severity == "SEVERE")
}
func onsets(events []Event, o QueryOptions) []Onset {
	last := map[string]int64{}
	var result []Onset
	for index, e := range events {
		if !isError(e) {
			continue
		}
		previous, seen := last[e.Signature]
		last[e.Signature] = e.OccurredAtNS
		if seen && time.Duration(e.OccurredAtNS-previous) < o.QuietPeriod {
			continue
		}
		onset := Onset{OccurredAt: e.OccurredAt, Family: e.Family, Signature: e.Signature, ExceptionClass: e.ExceptionClass, PreCounts: map[string]int{}}
		signatures := map[string]int64{}
		for _, prior := range events[:index] {
			if prior.OccurredAtNS >= e.OccurredAtNS-o.PreWindow.Nanoseconds() {
				onset.PreCounts[prior.Family]++
				signatures[prior.Signature]++
			}
		}
		for name, count := range signatures {
			onset.PreSignatures = append(onset.PreSignatures, Count{Name: name, Count: count})
		}
		sort.Slice(onset.PreSignatures, func(i, j int) bool {
			if onset.PreSignatures[i].Count != onset.PreSignatures[j].Count {
				return onset.PreSignatures[i].Count > onset.PreSignatures[j].Count
			}
			return onset.PreSignatures[i].Name < onset.PreSignatures[j].Name
		})
		if e.ExceptionClass != "" {
			for _, candidate := range events {
				if candidate.Status == nil {
					continue
				}
				delta := candidate.OccurredAtNS - e.OccurredAtNS
				if delta < 0 {
					delta = -delta
				}
				if time.Duration(delta) <= o.CorrelationWindow {
					onset.NearbyRequests++
					if *candidate.Status >= 500 {
						onset.NearbyFailures++
					}
				}
			}
			onset.CorrelationStatement = fmt.Sprintf("%d HTTP request(s), including %d failure(s), occurred within %s of this exception onset; temporal proximity does not establish causation", onset.NearbyRequests, onset.NearbyFailures, o.CorrelationWindow)
		}
		result = append(result, onset)
	}
	return result
}
