# Analysis contract

`report.Analyze` produces an evidence-oriented `QueryReport`. This document
records the current observable semantics so analysis code can be extracted
without changing results. It does not define or change incident HTML rendering.

## Inputs, window, and baseline

- The requested interval is `[around-before, around+after]`; both endpoints are
  included. `window_end` is clamped down to the latest event for the selected
  source when that event is earlier than the requested end.
- `around` exactly equal to the selected source's latest event is valid. A later
  center fails with `--around must not be after the latest available event...`.
  If the selected source has no events, no latest-event validation or end clamp
  is applied.
- Analysis reads only the selected source and time window. This unfiltered set
  is the baseline: `baseline_size`, buckets, routes, request signals/bursts,
  signature counts, and onsets all use it. Family, severity, status, route,
  site, and signature filters apply only to `timeline` and `sample_size`.
- `filters` contains only nonempty supplied values. Status accepts an exact
  integer or an `Nxx` class; events without a status never match a status filter.

Covered by `TestAnalysisContractBaselineIsTimeSourceOnlyAndTimelineIsFiltered`,
`TestAnalysisContractClampsAndAcceptsCenterAtLatestEvent`, and
`TestAnalyzeTimelineFilters`.

## Buckets and aggregate frequencies

- A positive bucket duration partitions the window in chronological order.
  Every bucket is `[start, end)` except the final bucket, which includes its
  end. A zero-width window yields one zero-width final bucket; a nonpositive
  bucket duration yields no buckets.
- Route aggregates include events with any HTTP status, group by
  `(route_template, site)`, and count failures where `status >= 500`. Their
  rate is `failures / requests`. Routes sort by failures descending, route
  ascending, then site ascending.
- Signature frequencies include every baseline event, including an empty
  signature, and sort by frequency descending then signature ascending.

Covered by `TestAnalysisContractBucketsIncludeStartBoundariesAndFinalEnd` and
`TestAnalysisContractRoutesAndSignaturesCountBaselineAndBreakTies`.

## Onsets, windows, and proximity

- Eligible errors are Catalina events with `SEVERE` severity or a nonempty
  exception class, and JVM events with `ERROR` or `SEVERE` severity. Onset
  suppression is per signature: an eligible error is suppressed only when the
  elapsed time since the immediately preceding eligible error for that
  signature is strictly less than `quiet_period`. Equality starts a new onset;
  suppressed errors still update the preceding timestamp.
- Each onset's pre-window considers earlier baseline entries with timestamps
  `>= onset-pre_window`. It includes an earlier entry at the exact onset time;
  it excludes an entry just before the lower boundary. Family pre-counts and
  signature frequencies retain those entries. Pre-signature ties sort by name.
- Nearby request samples are only status-bearing events and are counted when
  `abs(request_time-onset_time) <= correlation_window`; failures are status
  `>= 500`. A correlation statement is emitted only when the onset has an
  exception class and always says temporal proximity does not establish
  causation. It makes no causal claim.
- Pre-OOM request-pressure windows are calculated for every baseline event
  whose exception class is exactly `java.lang.OutOfMemoryError`. The pre range
  is `[onset-pre_window, onset)` and prior is the immediately preceding equal-size
  window. They include all status-bearing requests, regardless of success.
  A route/fingerprint with no prior requests gets an onset count; otherwise it
  gets a spike count only if pre requests are strictly greater than prior.
  Count/byte ratios use the pre value itself when the prior denominator is zero.

Covered by `TestAnalysisContractQuietPreOnsetAndCorrelationBoundaries`,
`TestAnalysisContractAnalyzeQuietPeriodEligibilityAndSignatureIsolation`, and
`TestAnalysisContractPressureWindowsAreHalfOpenAndSorted`.

## Ordering and ties

- Database event/timeline order is deterministic: occurrence nanoseconds,
  source label, relative path, source line start, then event id. Buckets and
  onsets retain that chronological order.
- Request signals and request bursts sort by onset count, spike count, pre
  request count, pre response bytes (all descending), then name ascending.
  Signature and pre-signature ties sort by name ascending.
- Route rows sort by failures descending, route ascending, then site ascending.
  This makes same-route, same-failure rows deterministic across sites.

Covered by `TestAnalysisContractTimelineTieOrderUsesEventOrder`,
`TestAnalysisContractRoutesAndSignaturesCountBaselineAndBreakTies`, and
`TestAnalysisContractPressureWindowsAreHalfOpenAndSorted`.

## Intentional compatibility difference

Route rows that previously tied on both failure count and route but differed by
site did not have a guaranteed relative order. B06 intentionally makes those
rows sort by site ascending. This is the only analysis-result ordering change
introduced by the task; it does not change HTML rendering.
