package writepattern

import (
	"testing"
	"time"

	"github.com/jaybilgaye/iceberg-sentry/internal/health"
	"github.com/jaybilgaye/iceberg-sentry/internal/iceberg"
)

func mkSnaps(intervals []time.Duration, addedFiles int64) []iceberg.Snapshot {
	now := time.Now().UnixMilli()
	out := make([]iceberg.Snapshot, len(intervals)+1)
	out[0] = iceberg.Snapshot{SnapshotID: 1, TimestampMs: now - sumMs(intervals)}
	for i, d := range intervals {
		out[i+1] = iceberg.Snapshot{
			SnapshotID:  int64(i + 2),
			TimestampMs: out[i].TimestampMs + d.Milliseconds(),
			Summary:     map[string]string{"added-data-files": "1"},
		}
		_ = addedFiles
	}
	return out
}

func sumMs(ds []time.Duration) int64 {
	var s int64
	for _, d := range ds {
		s += d.Milliseconds()
	}
	return s
}

func TestClassifyStreaming(t *testing.T) {
	intervals := make([]time.Duration, 30)
	for i := range intervals {
		intervals[i] = 60 * time.Second
	}
	r := Classify(mkSnaps(intervals, 1), Defaults())
	if r.Pattern != health.WritePatternStreaming {
		t.Errorf("pattern = %s, want streaming", r.Pattern)
	}
}

func TestClassifyBatch(t *testing.T) {
	intervals := make([]time.Duration, 20)
	for i := range intervals {
		intervals[i] = 2 * time.Hour
	}
	r := Classify(mkSnaps(intervals, 1), Defaults())
	if r.Pattern != health.WritePatternBatch {
		t.Errorf("pattern = %s, want batch", r.Pattern)
	}
}

func TestClassifyMixedHighCV(t *testing.T) {
	// Alternate very short and very long intervals — high CV.
	intervals := []time.Duration{
		30 * time.Second, 6 * time.Hour, 30 * time.Second, 8 * time.Hour,
		30 * time.Second, 12 * time.Hour, 30 * time.Second, 4 * time.Hour,
		30 * time.Second, 7 * time.Hour, 30 * time.Second, 9 * time.Hour,
	}
	r := Classify(mkSnaps(intervals, 1), Defaults())
	if r.Pattern != health.WritePatternMixed {
		t.Errorf("pattern = %s, want mixed", r.Pattern)
	}
}

func TestClassifyUnknown(t *testing.T) {
	r := Classify([]iceberg.Snapshot{{SnapshotID: 1, TimestampMs: 100}}, Defaults())
	if r.Pattern != health.WritePatternUnknown {
		t.Errorf("pattern = %s, want unknown", r.Pattern)
	}
}
