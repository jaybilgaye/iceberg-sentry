package scan

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hamba/avro/v2/ocf"

	"github.com/jaybilgaye/iceberg-sentry/internal/catalog"
	"github.com/jaybilgaye/iceberg-sentry/internal/health"
	"github.com/jaybilgaye/iceberg-sentry/internal/storage"
)

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

func writeAvro(t testing.TB, path, schema string, recs []map[string]any) {
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

// buildTableFixture writes a minimal v2 Iceberg table layout to disk and
// returns the catalog root. dataFileSize is the size, in bytes, attributed
// to each data file in the manifest entry.
func buildTableFixture(t testing.TB, dataFiles, deleteFiles int, dataFileSize int64) string {
	t.Helper()
	root := t.TempDir()
	tableDir := filepath.Join(root, "finance", "transactions")
	mdDir := filepath.Join(tableDir, "metadata")

	manifestPath := filepath.Join(mdDir, "m-100.avro")
	manifestEntries := make([]map[string]any, 0, dataFiles+deleteFiles)
	for i := 0; i < dataFiles; i++ {
		manifestEntries = append(manifestEntries, map[string]any{
			"status":          int32(1),
			"snapshot_id":     int64(100),
			"sequence_number": int64(1),
			"data_file": map[string]any{
				"content":            int32(0),
				"file_path":          fmt.Sprintf("data/%05d.parquet", i),
				"file_format":        "PARQUET",
				"record_count":       int64(100),
				"file_size_in_bytes": dataFileSize,
			},
		})
	}
	for i := 0; i < deleteFiles; i++ {
		manifestEntries = append(manifestEntries, map[string]any{
			"status":          int32(1),
			"snapshot_id":     int64(100),
			"sequence_number": int64(1),
			"data_file": map[string]any{
				"content":            int32(1),
				"file_path":          fmt.Sprintf("deletes/pos-%d.parquet", i),
				"file_format":        "PARQUET",
				"record_count":       int64(5),
				"file_size_in_bytes": int64(2048),
			},
		})
	}
	writeAvro(t, manifestPath, manifestEntrySchema, manifestEntries)

	manifestListPath := filepath.Join(mdDir, "snap-100.avro")
	writeAvro(t, manifestListPath, manifestListSchema, []map[string]any{{
		"manifest_path":        "file://" + manifestPath,
		"manifest_length":      int64(2048),
		"partition_spec_id":    int32(0),
		"content":              int32(0),
		"sequence_number":      int64(1),
		"min_sequence_number":  int64(1),
		"added_snapshot_id":    int64(100),
		"added_files_count":    int32(int32(dataFiles + deleteFiles)),
		"existing_files_count": int32(0),
		"deleted_files_count":  int32(0),
		"added_rows_count":     int64(int64(dataFiles * 100)),
		"existing_rows_count":  int64(0),
		"deleted_rows_count":   int64(0),
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

func TestEngineScanCriticalDeletes(t *testing.T) {
	root := buildTableFixture(t, 4, 6, 256*1024*1024) // 4 data files, 6 deletes -> ratio 1.5 -> CRITICAL
	cat := catalog.NewLocalFS(root)
	res := storage.NewResolver(storage.NewLocalFS())
	eng := NewEngine(cat, res)
	eng.Now = func() time.Time { return time.Now() }

	r, err := eng.Scan(context.Background(), catalog.TableID{Namespace: "finance", Name: "transactions"}, health.Defaults())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if r.Stats.DataFileCount != 4 {
		t.Errorf("data files = %d, want 4", r.Stats.DataFileCount)
	}
	if r.Stats.PositionDeleteFiles != 6 {
		t.Errorf("position deletes = %d, want 6", r.Stats.PositionDeleteFiles)
	}
	if r.Report.WorstSeverity != health.SeverityCritical {
		t.Errorf("worst = %s, want CRITICAL", r.Report.WorstSeverity)
	}
}

func TestEngineScanHealthyTable(t *testing.T) {
	root := buildTableFixture(t, 2, 0, 256*1024*1024)
	cat := catalog.NewLocalFS(root)
	res := storage.NewResolver(storage.NewLocalFS())
	eng := NewEngine(cat, res)

	r, err := eng.Scan(context.Background(), catalog.TableID{Namespace: "finance", Name: "transactions"}, health.Defaults())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if r.Report.WorstSeverity != health.SeverityOK {
		t.Errorf("worst = %s, want OK; report=%+v", r.Report.WorstSeverity, r.Report)
	}
	if r.Report.Score != r.Report.MaxScore {
		t.Errorf("score=%d max=%d; want full score", r.Report.Score, r.Report.MaxScore)
	}
}
