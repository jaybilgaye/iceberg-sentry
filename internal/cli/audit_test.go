package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hamba/avro/v2/ocf"

	"github.com/jaybilgaye/iceberg-sentry/internal/exitcode"
	"github.com/jaybilgaye/iceberg-sentry/internal/health"
)

// These schemas are duplicated from the scan package tests intentionally —
// the CLI test is a black-box exercise of the user-facing commands and
// shouldn't share internal test helpers.
const manifestListSchema = `{
  "type": "record",
  "name": "manifest_file",
  "fields": [
    {"name": "manifest_path",       "type": "string"},
    {"name": "manifest_length",     "type": "long"},
    {"name": "partition_spec_id",   "type": "int"},
    {"name": "content",             "type": "int"},
    {"name": "sequence_number",     "type": "long"},
    {"name": "min_sequence_number", "type": "long"},
    {"name": "added_snapshot_id",   "type": "long"},
    {"name": "added_files_count",   "type": "int"},
    {"name": "existing_files_count","type": "int"},
    {"name": "deleted_files_count", "type": "int"},
    {"name": "added_rows_count",    "type": "long"},
    {"name": "existing_rows_count", "type": "long"},
    {"name": "deleted_rows_count",  "type": "long"}
  ]
}`

const manifestEntrySchema = `{
  "type": "record",
  "name": "manifest_entry",
  "fields": [
    {"name": "status",          "type": "int"},
    {"name": "snapshot_id",     "type": "long"},
    {"name": "sequence_number", "type": "long"},
    {"name": "data_file", "type": {
      "type": "record",
      "name": "data_file",
      "fields": [
        {"name": "content",            "type": "int"},
        {"name": "file_path",          "type": "string"},
        {"name": "file_format",        "type": "string"},
        {"name": "record_count",       "type": "long"},
        {"name": "file_size_in_bytes", "type": "long"}
      ]
    }}
  ]
}`

func writeAvro(t *testing.T, path, schema string, recs []map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	enc, err := ocf.NewEncoder(schema, f)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
}

func buildHealthyFixture(t *testing.T) string {
	root := t.TempDir()
	tableDir := filepath.Join(root, "finance", "transactions")
	mdDir := filepath.Join(tableDir, "metadata")

	manifestPath := filepath.Join(mdDir, "m-100.avro")
	writeAvro(t, manifestPath, manifestEntrySchema, []map[string]any{{
		"status":          int32(1),
		"snapshot_id":     int64(100),
		"sequence_number": int64(1),
		"data_file": map[string]any{
			"content":            int32(0),
			"file_path":          "data/00000.parquet",
			"file_format":        "PARQUET",
			"record_count":       int64(1000),
			"file_size_in_bytes": int64(256 * 1024 * 1024),
		},
	}})

	manifestListPath := filepath.Join(mdDir, "snap-100.avro")
	writeAvro(t, manifestListPath, manifestListSchema, []map[string]any{{
		"manifest_path":         "file://" + manifestPath,
		"manifest_length":       int64(2048),
		"partition_spec_id":     int32(0),
		"content":               int32(0),
		"sequence_number":       int64(1),
		"min_sequence_number":   int64(1),
		"added_snapshot_id":     int64(100),
		"added_files_count":     int32(1),
		"existing_files_count":  int32(0),
		"deleted_files_count":   int32(0),
		"added_rows_count":      int64(1000),
		"existing_rows_count":   int64(0),
		"deleted_rows_count":    int64(0),
	}})

	mdJSON := fmt.Sprintf(`{
  "format-version": 2,
  "table-uuid": "00000000-0000-0000-0000-000000000001",
  "location": "file://%s",
  "last-updated-ms": %d,
  "last-column-id": 1,
  "current-schema-id": 0,
  "schemas": [{"schema-id": 0, "type": "struct", "fields": []}],
  "partition-specs": [{"spec-id": 0, "fields": []}],
  "default-spec-id": 0,
  "last-partition-id": 0,
  "default-sort-order-id": 0,
  "sort-orders": [{"order-id": 0, "fields": []}],
  "current-snapshot-id": 100,
  "snapshots": [
    {"snapshot-id": 100, "sequence-number": 1, "timestamp-ms": %d,
     "manifest-list": "file://%s"}
  ]
}`, tableDir, time.Now().UnixMilli(), time.Now().UnixMilli(), manifestListPath)
	if err := os.WriteFile(filepath.Join(mdDir, "v1.metadata.json"), []byte(mdJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestAuditJSONHealthy(t *testing.T) {
	root := buildHealthyFixture(t)
	var stdout, stderr bytes.Buffer
	flags := &auditFlags{
		catalogKind: "localfs",
		catalogRoot: root,
		tableID:     "finance.transactions",
		format:      "json",
		failOn:      "critical",
	}
	err := runAudit(context.Background(), &stdout, &stderr, flags)
	if err != nil {
		t.Fatalf("runAudit: %v (stderr=%s)", err, stderr.String())
	}
	var report health.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, stdout.String())
	}
	if report.WorstSeverity != health.SeverityOK {
		t.Errorf("worst = %s, want OK; report=%+v", report.WorstSeverity, report)
	}
	if !strings.HasPrefix(report.TableID, "finance.transactions") {
		t.Errorf("table id = %q", report.TableID)
	}
}

func TestAuditExitCodeForUnknownCatalog(t *testing.T) {
	flags := &auditFlags{
		catalogKind: "nope",
		tableID:     "x.y",
	}
	err := runAudit(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, flags)
	ce, ok := err.(*codedError)
	if !ok {
		t.Fatalf("err = %v (%T)", err, err)
	}
	if ce.code != exitcode.ConnectionFail {
		t.Errorf("code = %d, want %d", ce.code, exitcode.ConnectionFail)
	}
}
