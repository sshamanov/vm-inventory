// Package history teaches the observation index about instances that have gone
// away (§15.4).
//
// The live refresh queries Prometheus for what is being scraped right now, so
// an instance that departs leaves no trace in the index at all — there would be
// nothing for the wider views to dim. This package asks Prometheus which series
// were sampled at any point in the retention window and adds the ones the index
// does not know about as retired entries, dated by their newest sample.
package history

import (
	"context"
	"log/slog"
	"math"
	"sort"
	"time"

	"vm-inventory/internal/backend/index"
	"vm-inventory/internal/backend/prometheus"
	"vm-inventory/internal/shared"
)

// The recent past earns an hourly resolution: a departure noticed this week is
// worth dating to the hour. The tail is deliberately coarse, because a
// subquery costs one evaluation per series per step and a year of hourly steps
// over every resource series runs past the client's 30-second timeout (§15.4).
const (
	fineLookback = 7 * 24 * time.Hour
	fineStep     = time.Hour
	maxSteps     = 200
)

// Pass is one subquery over the retention window.
type Pass struct {
	Lookback time.Duration
	Step     time.Duration
}

// Passes returns the subqueries covering a retention window: a fine pass over
// the recent week, then a coarse pass over the whole window. Both merge by
// last-seen, so the coarse pass can never date a recent departure less
// precisely than the fine pass already did.
func Passes(retention time.Duration) []Pass {
	if retention <= fineLookback {
		return []Pass{{Lookback: retention, Step: fineStep}}
	}
	return []Pass{
		{Lookback: fineLookback, Step: fineStep},
		{Lookback: retention, Step: prometheus.LastSeenStep(retention, maxSteps)},
	}
}

// Backfill runs every pass for the identity metrics and merges the result into
// the index. It is meant to run in the background: it is several Prometheus
// queries, and nothing about serving the UI should wait for them. It is safe to
// run concurrently with the live refresh, because the index applies a
// back-filled observation only when it is newer than the one already recorded.
func Backfill(ctx context.Context, client *prometheus.Client, idx *index.ObservationIndex, logger *slog.Logger) {
	for _, t := range targets(idx) {
		for _, pass := range Passes(shared.RetentionWindow) {
			qr, err := client.QueryInstant(ctx, prometheus.QueryLastSeen(t.metric, pass.Lookback, pass.Step))
			if err != nil {
				logger.Warn("history backfill query failed",
					"metric", t.metric,
					"lookback", pass.Lookback.String(),
					"error", err,
				)
				continue
			}
			logger.Debug("history backfill pass complete",
				"metric", t.metric,
				"lookback", pass.Lookback.String(),
				"step", pass.Step.String(),
				"series", len(qr.Data.Result),
				"applied", t.apply(qr.Data.Result),
			)
		}
	}
}

type target struct {
	metric string
	apply  func([]prometheus.MetricResult) int
}

func targets(idx *index.ObservationIndex) []target {
	return []target{
		{shared.MetricHostInfo, applyHosts(idx)},
		{shared.MetricResourceInfo, applyResources(idx)},
	}
}

// dated pairs a result with the sample time it reported, for oldest-first
// ordering. One identity can own several historical series — a kernel or OS
// upgrade re-labels the series, so the retired one and its replacement carry
// the same host_id with different labels — and applying them oldest first
// leaves the record holding the newest labels.
type dated struct {
	seen time.Time
	pos  int
}

func datedByLastSeen(results []prometheus.MetricResult) []dated {
	ordered := make([]dated, 0, len(results))
	for i, r := range results {
		seen := unixSeconds(prometheus.ParseValue(r))
		if seen.IsZero() {
			continue
		}
		ordered = append(ordered, dated{seen: seen, pos: i})
	}
	sort.Slice(ordered, func(a, b int) bool { return ordered[a].seen.Before(ordered[b].seen) })
	return ordered
}

func applyHosts(idx *index.ObservationIndex) func([]prometheus.MetricResult) int {
	return func(results []prometheus.MetricResult) int {
		applied := 0
		for _, d := range datedByLastSeen(results) {
			if idx.BackfillHost(prometheus.DecodeHostInfo(results[d.pos]), d.seen) {
				applied++
			}
		}
		return applied
	}
}

func applyResources(idx *index.ObservationIndex) func([]prometheus.MetricResult) int {
	return func(results []prometheus.MetricResult) int {
		applied := 0
		for _, d := range datedByLastSeen(results) {
			if idx.BackfillResource(prometheus.DecodeResourceInfo(results[d.pos]), d.seen) {
				applied++
			}
		}
		return applied
	}
}

// unixSeconds converts a timestamp() value — seconds with a fractional part —
// to a time. A zero means the series carried no usable sample, which callers
// drop rather than date to 1970.
func unixSeconds(v float64) time.Time {
	if v <= 0 || math.IsNaN(v) {
		return time.Time{}
	}
	sec, frac := math.Modf(v)
	return time.Unix(int64(sec), int64(frac*float64(time.Second))).UTC()
}
