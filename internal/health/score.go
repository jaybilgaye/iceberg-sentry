package health

import (
	"fmt"
	"time"
)

// Thresholds drive dimension scoring. Defaults match the values quoted in
// the spec; a Policy can override them per-table.
type Thresholds struct {
	MinFileSizeBytes  int64
	WarnFileSizeBytes int64
	WarnManifestCount int64
	CritManifestCount int64
	WarnSnapshotAge   time.Duration
	CritSnapshotAge   time.Duration
	WarnSnapshotCount int
	CritSnapshotCount int
	WarnDeleteRatio   float64
	CritDeleteRatio   float64
}

// Defaults returns the spec-default thresholds.
func Defaults() Thresholds {
	return Thresholds{
		MinFileSizeBytes:  128 * 1024 * 1024,
		WarnFileSizeBytes: 64 * 1024 * 1024,
		WarnManifestCount: 2_000,
		CritManifestCount: 5_000,
		WarnSnapshotAge:   30 * 24 * time.Hour,
		CritSnapshotAge:   90 * 24 * time.Hour,
		WarnSnapshotCount: 100,
		CritSnapshotCount: 500,
		WarnDeleteRatio:   0.10,
		CritDeleteRatio:   0.25,
	}
}

// Score computes the composite health report.
func Score(table, catalogName string, s *Stats, t Thresholds) Report {
	// Streaming write patterns get more lenient file-size thresholds —
	// flink/streaming-ingested tables have legitimately small files until
	// downstream compaction lands.
	effective := t
	if s.WritePattern == WritePatternStreaming {
		effective.MinFileSizeBytes = t.MinFileSizeBytes / 4
		if effective.MinFileSizeBytes < 16*1024*1024 {
			effective.MinFileSizeBytes = 16 * 1024 * 1024
		}
	}

	dims := []Dimension{
		fileSizeDimension(s, effective),
		deleteAmplificationDimension(s, effective),
		manifestDensityDimension(s, effective),
		snapshotDimension(s, effective),
		partitionSkewDimension(s),
		formatVersionDimension(s),
	}
	r := Report{
		TableID:       table,
		Catalog:       catalogName,
		FormatVersion: s.FormatVersion,
		SnapshotID:    s.SnapshotID,
		WritePattern:  string(s.WritePattern),
		Dimensions:    dims,
	}
	worst := SeverityOK
	for _, d := range dims {
		r.Score += d.Score
		r.MaxScore += d.MaxScore
		if d.Severity.rank() > worst.rank() {
			worst = d.Severity
		}
	}
	r.WorstSeverity = worst
	return r
}

func fileSizeDimension(s *Stats, t Thresholds) Dimension {
	const max = 20
	d := Dimension{Name: "file_size", MaxScore: max, Severity: SeverityOK, Score: max}
	if !s.HasDataFiles() {
		d.Summary = "no data files"
		return d
	}
	frac := float64(s.SmallFileCountUnder128) / float64(s.DataFileCount)
	switch {
	case frac >= 0.5:
		d.Severity = SeverityCritical
		d.Score = 8
		d.Summary = fmt.Sprintf("%.0f%% of files below %dMB", frac*100, t.MinFileSizeBytes/(1024*1024))
		d.Remediation = "rewrite_data_files (target=512MB) recommended"
	case frac >= 0.25:
		d.Severity = SeverityWarning
		d.Score = 12
		d.Summary = fmt.Sprintf("%.0f%% of files below %dMB", frac*100, t.MinFileSizeBytes/(1024*1024))
		d.Remediation = "schedule rewrite_data_files when convenient"
	default:
		d.Summary = fmt.Sprintf("%.0f%% of files below %dMB", frac*100, t.MinFileSizeBytes/(1024*1024))
	}
	return d
}

func deleteAmplificationDimension(s *Stats, t Thresholds) Dimension {
	const max = 20
	d := Dimension{Name: "delete_amplification", MaxScore: max, Severity: SeverityOK, Score: max}
	if s.FormatVersion < 2 {
		d.Summary = "delete files not applicable for v1 tables"
		return d
	}
	if s.PositionDeleteFiles+s.EqualityDeleteFiles == 0 {
		d.Summary = "no delete files present"
		return d
	}
	if !s.HasDataFiles() {
		d.Summary = "delete files present but no data files (table may be empty)"
		return d
	}
	totalDeletes := s.PositionDeleteFiles + s.EqualityDeleteFiles
	ratio := float64(totalDeletes) / float64(s.DataFileCount)
	switch {
	case ratio >= t.CritDeleteRatio:
		d.Severity = SeverityCritical
		d.Score = 6
		d.Remediation = "rewrite_position_deletes / rewrite_data_files urgent"
	case ratio >= t.WarnDeleteRatio:
		d.Severity = SeverityWarning
		d.Score = 12
		d.Remediation = "schedule delete-file compaction"
	}
	d.Summary = fmt.Sprintf(
		"%d delete files (%d position, %d equality); ratio %.1f%%",
		totalDeletes, s.PositionDeleteFiles, s.EqualityDeleteFiles, ratio*100,
	)
	return d
}

func manifestDensityDimension(s *Stats, t Thresholds) Dimension {
	const max = 15
	d := Dimension{Name: "manifest_density", MaxScore: max, Severity: SeverityOK, Score: max}
	switch {
	case s.ManifestFileCount >= t.CritManifestCount:
		d.Severity = SeverityCritical
		d.Score = 5
		d.Remediation = "rewrite_manifests recommended"
	case s.ManifestFileCount >= t.WarnManifestCount:
		d.Severity = SeverityWarning
		d.Score = 9
		d.Remediation = "consider rewrite_manifests"
	}
	d.Summary = fmt.Sprintf("%d manifest files", s.ManifestFileCount)
	return d
}

func snapshotDimension(s *Stats, t Thresholds) Dimension {
	const max = 15
	d := Dimension{Name: "snapshot", MaxScore: max, Severity: SeverityOK, Score: max}
	oldest := time.Duration(s.OldestSnapshotAgeMs) * time.Millisecond
	switch {
	case oldest >= t.CritSnapshotAge || s.SnapshotCount >= t.CritSnapshotCount:
		d.Severity = SeverityCritical
		d.Score = 5
		d.Remediation = "expire_snapshots --older-than=30d"
	case oldest >= t.WarnSnapshotAge || s.SnapshotCount >= t.WarnSnapshotCount:
		d.Severity = SeverityWarning
		d.Score = 10
		d.Remediation = "schedule expire_snapshots"
	}
	d.Summary = fmt.Sprintf("%d snapshots; oldest %s", s.SnapshotCount, formatDuration(oldest))
	return d
}

func partitionSkewDimension(s *Stats) Dimension {
	const max = 15
	d := Dimension{Name: "partition_skew", MaxScore: max, Severity: SeverityOK, Score: max}
	if len(s.Partitions) < 2 {
		d.Summary = "single partition or unpartitioned — skew not applicable"
		return d
	}
	cv, hot, sparse := skewMetrics(s.Partitions)
	switch {
	case cv >= 1.0:
		d.Severity = SeverityCritical
		d.Score = 5
		d.Remediation = "re-partition hot keys or bucket the partition column"
	case cv >= 0.5:
		d.Severity = SeverityWarning
		d.Score = 9
		d.Remediation = "consider bucketing or re-partitioning hot keys"
	}
	d.Summary = fmt.Sprintf("CV=%.2f across %d partitions (%d hot, %d sparse)", cv, len(s.Partitions), hot, sparse)
	return d
}

// skewMetrics returns (coefficient of variation, hot count, sparse count).
// Hot partitions are >2× the median; sparse are <10% of the median.
func skewMetrics(parts []PartitionStats) (float64, int, int) {
	n := float64(len(parts))
	if n == 0 {
		return 0, 0, 0
	}
	var sum float64
	for _, p := range parts {
		sum += float64(p.Bytes)
	}
	mean := sum / n
	if mean == 0 {
		return 0, 0, 0
	}
	var variance float64
	for _, p := range parts {
		d := float64(p.Bytes) - mean
		variance += d * d
	}
	stddev := 0.0
	if n > 1 {
		stddev = sqrtNonNeg(variance / n)
	}
	cv := stddev / mean

	median := medianBytes(parts)
	var hot, sparse int
	for _, p := range parts {
		switch {
		case median > 0 && float64(p.Bytes) > 2*float64(median):
			hot++
		case median > 0 && float64(p.Bytes) < 0.1*float64(median):
			sparse++
		}
	}
	return cv, hot, sparse
}

func medianBytes(parts []PartitionStats) int64 {
	if len(parts) == 0 {
		return 0
	}
	vals := make([]int64, len(parts))
	for i, p := range parts {
		vals[i] = p.Bytes
	}
	// Insertion sort is fine — partition counts here are small (hundreds, not
	// millions; manifest aggregation pre-groups).
	for i := 1; i < len(vals); i++ {
		for j := i; j > 0 && vals[j-1] > vals[j]; j-- {
			vals[j-1], vals[j] = vals[j], vals[j-1]
		}
	}
	mid := len(vals) / 2
	if len(vals)%2 == 1 {
		return vals[mid]
	}
	return (vals[mid-1] + vals[mid]) / 2
}

func sqrtNonNeg(x float64) float64 {
	if x <= 0 {
		return 0
	}
	// Avoid pulling in math for one call — Newton's method converges fast.
	z := x / 2
	for i := 0; i < 12; i++ {
		z = (z + x/z) / 2
	}
	return z
}

func formatVersionDimension(s *Stats) Dimension {
	const max = 5
	d := Dimension{Name: "format_version", MaxScore: max, Severity: SeverityOK, Score: max}
	switch s.FormatVersion {
	case 1:
		d.Severity = SeverityInfo
		d.Score = 3
		d.Summary = "Iceberg v1 — v2 upgrade available"
		d.Remediation = "ALTER TABLE ... SET TBLPROPERTIES ('format-version'='2')"
	case 2:
		d.Summary = "Iceberg v2"
	case 3:
		d.Summary = "Iceberg v3 (latest)"
	default:
		d.Severity = SeverityInfo
		d.Score = 3
		d.Summary = fmt.Sprintf("unknown format-version %d", s.FormatVersion)
	}
	return d
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	days := int(d.Hours() / 24)
	if days >= 1 {
		return fmt.Sprintf("%dd", days)
	}
	hours := int(d.Hours())
	if hours >= 1 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}
