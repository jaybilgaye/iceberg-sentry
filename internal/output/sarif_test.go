package output

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/jaybilgaye/iceberg-sentry/internal/health"
)

func TestSARIFShapeAndLevels(t *testing.T) {
	r := health.Report{
		TableID:       "finance.transactions",
		Catalog:       "rest",
		FormatVersion: 2,
		SnapshotID:    100,
		Score:         60,
		MaxScore:      100,
		WorstSeverity: health.SeverityCritical,
		Dimensions: []health.Dimension{
			{Name: "file_size", Score: 8, MaxScore: 20, Severity: health.SeverityCritical, Summary: "small files", Remediation: "rewrite_data_files"},
			{Name: "snapshot", Score: 15, MaxScore: 15, Severity: health.SeverityOK, Summary: "ok"},
		},
	}
	var buf bytes.Buffer
	if err := Render(&buf, r, FormatSARIF); err != nil {
		t.Fatalf("render: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, buf.String())
	}
	if got["version"] != "2.1.0" {
		t.Errorf("version = %v", got["version"])
	}
	runs := got["runs"].([]any)
	if len(runs) != 1 {
		t.Fatalf("runs = %d", len(runs))
	}
	results := runs[0].(map[string]any)["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("expected 1 result (OK dim filtered out), got %d", len(results))
	}
	res := results[0].(map[string]any)
	if res["level"] != "error" {
		t.Errorf("level = %v, want error", res["level"])
	}
}
