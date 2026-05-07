// Package health computes the Iceberg Sentry table health score and its
// constituent dimensions.
//
// The composite score is a weighted sum of dimension scores (0..max). Each
// dimension reports its own findings, severity, and recommended remediation
// command. Phase 1 ships these dimensions:
//
//	file_size           weight 20
//	delete_amplification weight 20
//	manifest_density    weight 15
//	snapshot            weight 15
//	format_version      weight 5
//
// Partition skew (15) and schema evolution (10) are deferred to Phase 2.
package health

// Severity classifies a finding.
type Severity string

const (
	SeverityOK       Severity = "OK"
	SeverityInfo     Severity = "INFO"
	SeverityWarning  Severity = "WARNING"
	SeverityCritical Severity = "CRITICAL"
)

// rank lets us compute the worst severity across dimensions.
func (s Severity) rank() int {
	switch s {
	case SeverityCritical:
		return 4
	case SeverityWarning:
		return 3
	case SeverityInfo:
		return 2
	default:
		return 1
	}
}

// Dimension is one component of the composite health score.
type Dimension struct {
	Name        string   `json:"name"`
	Score       int      `json:"score"`
	MaxScore    int      `json:"max_score"`
	Severity    Severity `json:"severity"`
	Summary     string   `json:"summary"`
	Remediation string   `json:"remediation,omitempty"`
}

// Report is the full output of a scan.
type Report struct {
	TableID        string      `json:"table"`
	Catalog        string      `json:"catalog"`
	FormatVersion  int         `json:"format_version"`
	SnapshotID     int64       `json:"snapshot_id"`
	Score          int         `json:"score"`
	MaxScore       int         `json:"max_score"`
	WorstSeverity  Severity    `json:"worst_severity"`
	Dimensions     []Dimension `json:"dimensions"`
	ScanDurationMS int64       `json:"scan_duration_ms"`
	WastageBytes   int64       `json:"estimated_wastage_bytes"`
}

// Stats is the aggregate input to scoring. It is filled in by the scan engine
// from manifest-list + manifest-file traversal.
type Stats struct {
	FormatVersion int
	SnapshotID    int64

	DataFileCount        int64
	DataFileTotalBytes   int64
	DataFileSizes        []int64 // sampled or full; keep small to bound memory
	SmallFileCountUnder64 int64
	SmallFileCountUnder128 int64

	PositionDeleteFiles  int64
	EqualityDeleteFiles  int64
	DeleteFileTotalBytes int64

	ManifestFileCount     int64
	ManifestListFileCount int64

	SnapshotCount       int
	OldestSnapshotAgeMs int64
	NewestSnapshotAgeMs int64
}

// HasDataFiles is a convenience accessor used by several dimensions.
func (s *Stats) HasDataFiles() bool { return s.DataFileCount > 0 }
