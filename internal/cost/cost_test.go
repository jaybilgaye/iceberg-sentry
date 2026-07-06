package cost

import (
	"testing"
	"time"

	"github.com/jaybilgaye/iceberg-sentry/internal/iceberg"
)

func TestTimelineCumulativeAndTierCandidate(t *testing.T) {
	now := time.Now()
	md := &iceberg.TableMetadata{
		Snapshots: []iceberg.Snapshot{
			{SnapshotID: 1, TimestampMs: now.Add(-200 * 24 * time.Hour).UnixMilli(), Summary: map[string]string{"added-files-size": "1073741824"}}, // 1 GB
			{SnapshotID: 2, TimestampMs: now.Add(-100 * 24 * time.Hour).UnixMilli(), Summary: map[string]string{"added-files-size": "2147483648"}}, // 2 GB
			{SnapshotID: 3, TimestampMs: now.Add(-1 * 24 * time.Hour).UnixMilli(), Summary: map[string]string{"added-files-size": "536870912"}},    // 0.5 GB
		},
	}
	points := Timeline(md, Default(), 30)
	if len(points) != 3 {
		t.Fatalf("expected 3 points, got %d", len(points))
	}
	if got := points[2].CumulativeGB; got < 3.49 || got > 3.51 {
		t.Errorf("cumulative GB = %v, want ~3.5", got)
	}
	if !points[0].TierCandidate || !points[1].TierCandidate || points[2].TierCandidate {
		t.Errorf("tier candidate flags = %v/%v/%v, want true/true/false",
			points[0].TierCandidate, points[1].TierCandidate, points[2].TierCandidate)
	}
}

func TestReclaimableUSD(t *testing.T) {
	// 100 GB at $0.023/GB-month = $2.30/month
	got := Reclaimable(100*1024*1024*1024, Default())
	if got < 2.29 || got > 2.31 {
		t.Errorf("got %v, want ~$2.30", got)
	}
}
