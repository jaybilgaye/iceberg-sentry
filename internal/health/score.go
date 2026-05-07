package health

import (
	"fmt"
	"time"
)

// Thresholds drive dimension scoring. Defaults match the values quoted in
// the spec; a Policy can override them per-table.
type Thresholds struct {
	MinFileSizeBytes   int64
	WarnFileSizeBytes  int64
	WarnManifestCount  int64
	CritManifestCount  int64
	WarnSnapshotAge    time.Duration
	CritSnapshotAge    time.Duration
	WarnSnapshotCount  int
	CritSnapshotCount  int
	WarnDeleteRatio    float64
	CritDeleteRatio    float64
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
	dims := []Dimension{
		fileSizeDimension(s, t),
		deleteAmplificationDimension(s, t),
		manifestDensityDimension(s, t),
		snapshotDimension(s, t),
		formatVersionDimension(s),
	}
	r := Report{
		TableID:       table,
		Catalog:       catalogName,
		FormatVersion: s.FormatVersion,
		SnapshotID:    s.SnapshotID,
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
