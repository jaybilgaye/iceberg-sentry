// Package cost models per-byte storage cost for the supported backends
// and turns Iceberg metadata into the spec's Cost Optimization Insights:
//
//   - Reclaimable storage (orphan files + over-retained snapshots)
//   - Per-snapshot cost timeline
//   - Tiered-storage candidates (cold-tier transition recommendations)
//
// Cost rates default to AWS S3 Standard list prices ($0.023/GB-month).
// They are deliberately quoted as "estimates based on list-price rates" in
// the report; production deployments should override via Provider.Rate().
package cost

import (
	"sort"
	"time"

	"github.com/jaybilgaye/iceberg-sentry/internal/iceberg"
)

// Provider returns a cost rate ($/GB-month) for the requested storage class.
type Provider interface {
	Name() string
	// Rate returns $ / GB / month for the named class (e.g. "standard",
	// "cold"). Unknown classes return the default class price.
	Rate(class string) float64
}

// Default returns a list-price-based provider for AWS S3 Standard.
func Default() Provider {
	return &staticProvider{name: "s3-standard", rates: map[string]float64{
		"standard":          0.023,
		"infrequent-access": 0.0125,
		"glacier-instant":   0.004,
		"glacier-deep":      0.00099,
	}}
}

// CustomRate returns a provider with the supplied $/GB/month standard-class rate.
func CustomRate(name string, standardRate float64) Provider {
	return &staticProvider{name: name, rates: map[string]float64{"standard": standardRate}}
}

type staticProvider struct {
	name  string
	rates map[string]float64
}

func (p *staticProvider) Name() string { return p.name }
func (p *staticProvider) Rate(class string) float64 {
	if r, ok := p.rates[class]; ok {
		return r
	}
	return p.rates["standard"]
}

// SnapshotPoint is one point on the Snapshot Cost Timeline.
type SnapshotPoint struct {
	SnapshotID    int64     `json:"snapshot_id"`
	Timestamp     time.Time `json:"timestamp"`
	AddedBytes    int64     `json:"added_bytes"`
	RemovedBytes  int64     `json:"removed_bytes"`
	CumulativeGB  float64   `json:"cumulative_gb"`
	MonthlyUSD    float64   `json:"monthly_usd"`
	TierCandidate bool      `json:"tier_candidate,omitempty"`
}

// Timeline produces the cost trajectory for a table. Bytes are derived from
// snapshot.summary keys when present (added-files-size / removed-files-size)
// which Iceberg writers populate. Snapshots without sizes contribute 0.
//
// coldTierAgeDays marks any snapshot older than the cutoff as a cold-tier
// transition candidate.
func Timeline(md *iceberg.TableMetadata, p Provider, coldTierAgeDays int) []SnapshotPoint {
	snaps := append([]iceberg.Snapshot(nil), md.Snapshots...)
	sort.Slice(snaps, func(i, j int) bool { return snaps[i].TimestampMs < snaps[j].TimestampMs })
	rate := p.Rate("standard")
	points := make([]SnapshotPoint, 0, len(snaps))
	var cumulative int64
	now := time.Now()
	for _, s := range snaps {
		added := summaryInt64(s.Summary, "added-files-size")
		removed := summaryInt64(s.Summary, "removed-files-size")
		cumulative += added - removed
		if cumulative < 0 {
			cumulative = 0
		}
		ts := time.UnixMilli(s.TimestampMs)
		ageDays := int(now.Sub(ts).Hours() / 24)
		points = append(points, SnapshotPoint{
			SnapshotID:    s.SnapshotID,
			Timestamp:     ts,
			AddedBytes:    added,
			RemovedBytes:  removed,
			CumulativeGB:  bytesToGB(cumulative),
			MonthlyUSD:    bytesToGB(cumulative) * rate,
			TierCandidate: coldTierAgeDays > 0 && ageDays >= coldTierAgeDays,
		})
	}
	return points
}

// Reclaimable returns the estimated monthly cost of the bytes in `wastageBytes`
// at the provider's standard-class rate. wastageBytes is typically the
// orphans.Report.TotalBytes value plus an estimate of over-retained snapshot
// data when relevant.
func Reclaimable(wastageBytes int64, p Provider) float64 {
	return bytesToGB(wastageBytes) * p.Rate("standard")
}

func bytesToGB(b int64) float64 { return float64(b) / (1024 * 1024 * 1024) }

func summaryInt64(m map[string]string, key string) int64 {
	if m == nil {
		return 0
	}
	v, ok := m[key]
	if !ok || v == "" {
		return 0
	}
	var n int64
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int64(c-'0')
	}
	return n
}
