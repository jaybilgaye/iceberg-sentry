// Package writepattern classifies how a table is being written to.
//
// Inputs are the most recent N snapshot timestamps and (optionally) the
// per-commit file counts pulled from snapshot.summary["added-files"].
// Outputs are one of the health.WritePattern constants.
//
// Heuristic (spec §2.6, §9.5):
//
//	streaming: high commit frequency, low files-per-commit variance,
//	           short median interval (< 5 minutes by default)
//	batch:     low commit frequency, high files-per-commit, long interval
//	           (> 1 hour by default)
//	mixed:     evidence of both — interval CV > 1.0
//	unknown:   not enough data
package writepattern

import (
	"strconv"
	"time"

	"github.com/jaybilgaye/iceberg-sentry/internal/health"
	"github.com/jaybilgaye/iceberg-sentry/internal/iceberg"
)

// Defaults are tuned to the spec's reference thresholds and chosen so a
// 50-commit window with median commit interval ≤ 5 minutes classifies as
// streaming.
type Thresholds struct {
	MaxWindow            int
	StreamingMaxInterval time.Duration
	BatchMinInterval     time.Duration
}

// Defaults returns the standard tunables.
func Defaults() Thresholds {
	return Thresholds{
		MaxWindow:            50,
		StreamingMaxInterval: 5 * time.Minute,
		BatchMinInterval:     1 * time.Hour,
	}
}

// Result is the classifier output.
type Result struct {
	Pattern             health.WritePattern
	AvgCommitIntervalMs int64
	AvgFilesPerCommit   float64
}

// Classify analyses the trailing window of snapshots.
func Classify(snaps []iceberg.Snapshot, th Thresholds) Result {
	if len(snaps) < 2 {
		return Result{Pattern: health.WritePatternUnknown}
	}

	// Iceberg metadata may list snapshots oldest-first; ensure we sort by ts.
	tsAsc := make([]int64, 0, len(snaps))
	filesAsc := make([]int64, 0, len(snaps))
	for _, s := range snaps {
		tsAsc = append(tsAsc, s.TimestampMs)
		filesAsc = append(filesAsc, summaryFiles(s.Summary))
	}
	// Insertion sort by timestamp (small N) keeping file counts aligned.
	for i := 1; i < len(tsAsc); i++ {
		for j := i; j > 0 && tsAsc[j-1] > tsAsc[j]; j-- {
			tsAsc[j-1], tsAsc[j] = tsAsc[j], tsAsc[j-1]
			filesAsc[j-1], filesAsc[j] = filesAsc[j], filesAsc[j-1]
		}
	}

	// Trim to the trailing window.
	if len(tsAsc) > th.MaxWindow {
		tsAsc = tsAsc[len(tsAsc)-th.MaxWindow:]
		filesAsc = filesAsc[len(filesAsc)-th.MaxWindow:]
	}

	// Inter-commit intervals.
	var sumDelta int64
	deltas := make([]int64, 0, len(tsAsc)-1)
	for i := 1; i < len(tsAsc); i++ {
		d := tsAsc[i] - tsAsc[i-1]
		if d < 0 {
			d = 0
		}
		deltas = append(deltas, d)
		sumDelta += d
	}
	avgInterval := sumDelta / int64(len(deltas))
	medianInterval := medianInt64(deltas)
	intervalCV := coefficientOfVariation(deltas)

	// Files per commit.
	var totalFiles int64
	var contributingCommits int
	for _, f := range filesAsc {
		if f > 0 {
			totalFiles += f
			contributingCommits++
		}
	}
	var avgFilesPerCommit float64
	if contributingCommits > 0 {
		avgFilesPerCommit = float64(totalFiles) / float64(contributingCommits)
	}

	pat := health.WritePatternUnknown
	median := time.Duration(medianInterval) * time.Millisecond
	switch {
	case intervalCV > 1.0:
		pat = health.WritePatternMixed
	case median > 0 && median <= th.StreamingMaxInterval:
		pat = health.WritePatternStreaming
	case median >= th.BatchMinInterval:
		pat = health.WritePatternBatch
	default:
		pat = health.WritePatternMixed
	}

	return Result{
		Pattern:             pat,
		AvgCommitIntervalMs: avgInterval,
		AvgFilesPerCommit:   avgFilesPerCommit,
	}
}

func summaryFiles(m map[string]string) int64 {
	if m == nil {
		return 0
	}
	for _, key := range []string{"added-data-files", "added-files-count", "added-files"} {
		if v, ok := m[key]; ok {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				return n
			}
		}
	}
	return 0
}

func medianInt64(xs []int64) int64 {
	if len(xs) == 0 {
		return 0
	}
	cp := make([]int64, len(xs))
	copy(cp, xs)
	for i := 1; i < len(cp); i++ {
		for j := i; j > 0 && cp[j-1] > cp[j]; j-- {
			cp[j-1], cp[j] = cp[j], cp[j-1]
		}
	}
	mid := len(cp) / 2
	if len(cp)%2 == 1 {
		return cp[mid]
	}
	return (cp[mid-1] + cp[mid]) / 2
}

func coefficientOfVariation(xs []int64) float64 {
	if len(xs) < 2 {
		return 0
	}
	var sum float64
	for _, x := range xs {
		sum += float64(x)
	}
	mean := sum / float64(len(xs))
	if mean == 0 {
		return 0
	}
	var variance float64
	for _, x := range xs {
		d := float64(x) - mean
		variance += d * d
	}
	stddev := variance / float64(len(xs))
	if stddev <= 0 {
		return 0
	}
	// Approx sqrt without importing math (kept minimal for the package).
	z := stddev / 2
	for i := 0; i < 12; i++ {
		z = (z + stddev/z) / 2
	}
	return z / mean
}
