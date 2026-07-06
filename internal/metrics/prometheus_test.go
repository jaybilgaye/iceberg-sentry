package metrics

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jaybilgaye/iceberg-sentry/internal/health"
)

func sampleReport() health.Report {
	return health.Report{
		TableID:        "finance.transactions",
		Catalog:        "rest",
		FormatVersion:  2,
		SnapshotID:     100,
		WritePattern:   "streaming",
		Score:          78,
		MaxScore:       90,
		WorstSeverity:  health.SeverityCritical,
		ScanDurationMS: 42,
		WastageBytes:   2048,
		Dimensions: []health.Dimension{
			{Name: "file_size", Score: 8, MaxScore: 20, Severity: health.SeverityCritical, Summary: "small"},
			{Name: "snapshot", Score: 15, MaxScore: 15, Severity: health.SeverityOK, Summary: "ok"},
		},
	}
}

func TestRenderEmitsExpectedSamples(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, sampleReport()); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()

	must := []string{
		`# HELP iceberg_table_health_score`,
		`# TYPE iceberg_table_health_score gauge`,
		`iceberg_table_health_score{branch=` + "``" + `,catalog="rest"`, // branch is empty so omitted
		`iceberg_table_dimension_score{`,
		`dimension="file_size"`,
		`iceberg_table_dimension_severity{`,
		`} 3`, // CRITICAL maps to 3
		`iceberg_table_scan_duration_ms{`,
		`} 42`,
		`iceberg_table_estimated_wastage_bytes{`,
		`} 2048`,
		`write_pattern="streaming"`,
	}
	// Branch is empty → no branch label; remove that probe.
	must[2] = `iceberg_table_health_score{catalog="rest",format_version="2",table="finance.transactions",write_pattern="streaming"} 78`
	for _, s := range must {
		if !strings.Contains(out, s) {
			t.Errorf("missing %q in output:\n%s", s, out)
		}
	}
}
