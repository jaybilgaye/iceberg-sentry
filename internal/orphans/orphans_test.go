package orphans

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hamba/avro/v2/ocf"

	"github.com/jaybilgaye/iceberg-sentry/internal/iceberg"
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

func writeAvro(t *testing.T, path, schema string, recs []map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	enc, err := ocf.NewEncoder(schema, &buf)
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
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// buildOrphanFixture creates a v2 table layout with two referenced data
// files and one orphan parquet file. Returns the metadata file path and the
// orphan's path.
func buildOrphanFixture(t *testing.T) (metadataURI, dataDir, orphanPath string) {
	t.Helper()
	root := t.TempDir()
	mdDir := filepath.Join(root, "metadata")
	dataDir = filepath.Join(root, "data")

	// Two real referenced data files.
	for _, name := range []string{"part-001.parquet", "part-002.parquet"} {
		p := filepath.Join(dataDir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("fake parquet"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// One unreferenced (orphan) file, backdated past the 24h grace period.
	orphanPath = filepath.Join(dataDir, "orphan-999.parquet")
	if err := os.WriteFile(orphanPath, []byte("orphan parquet"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(orphanPath, old, old); err != nil {
		t.Fatal(err)
	}

	manifestPath := filepath.Join(mdDir, "m-100.avro")
	writeAvro(t, manifestPath, manifestEntrySchema, []map[string]any{
		entryForDataFile(filepath.Join(dataDir, "part-001.parquet")),
		entryForDataFile(filepath.Join(dataDir, "part-002.parquet")),
	})

	listPath := filepath.Join(mdDir, "snap-100.avro")
	writeAvro(t, listPath, manifestListSchema, []map[string]any{
		{
			"manifest_path":         manifestPath,
			"manifest_length":       int64(1024),
			"partition_spec_id":     int32(0),
			"content":               int32(0),
			"sequence_number":       int64(1),
			"min_sequence_number":   int64(1),
			"added_snapshot_id":     int64(100),
			"added_files_count":     int32(2),
			"existing_files_count":  int32(0),
			"deleted_files_count":   int32(0),
			"added_rows_count":      int64(2),
			"existing_rows_count":   int64(0),
			"deleted_rows_count":    int64(0),
		},
	})

	metadataURI = filepath.Join(mdDir, "v1.metadata.json")
	body := fmt.Sprintf(`{
  "format-version": 2,
  "table-uuid": "11111111-1111-1111-1111-111111111111",
  "location": "%s",
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
     "manifest-list": "%s"}
  ]
}`, root, time.Now().UnixMilli(), time.Now().UnixMilli(), listPath)
	if err := os.WriteFile(metadataURI, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return metadataURI, dataDir, orphanPath
}

func entryForDataFile(path string) map[string]any {
	return map[string]any{
		"status":          int32(1),
		"snapshot_id":     int64(100),
		"sequence_number": int64(1),
		"data_file": map[string]any{
			"content":            int32(0),
			"file_path":          path,
			"file_format":        "PARQUET",
			"record_count":       int64(1),
			"file_size_in_bytes": int64(64 * 1024 * 1024),
		},
	}
}

func TestScanFindsBackdatedOrphan(t *testing.T) {
	mdURI, dataDir, orphanPath := buildOrphanFixture(t)
	st := storage.NewResolver(storage.NewLocalFS())

	rc, err := st.Open(context.Background(), mdURI)
	if err != nil {
		t.Fatal(err)
	}
	md, err := iceberg.ReadTableMetadata(context.Background(), rc)
	_ = rc.Close()
	if err != nil {
		t.Fatal(err)
	}

	rep, err := Scan(context.Background(), md, mdURI, dataDir, st, Options{
		GracePeriod: 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Candidates) != 1 || rep.Candidates[0].URI != orphanPath {
		t.Fatalf("expected one orphan at %s, got %+v", orphanPath, rep.Candidates)
	}
	if rep.TotalBytes <= 0 {
		t.Errorf("total bytes = %d", rep.TotalBytes)
	}
}

func TestGracePeriodSkipsFreshOrphan(t *testing.T) {
	mdURI, dataDir, orphanPath := buildOrphanFixture(t)
	// Forward-date the orphan to within the grace window.
	now := time.Now()
	if err := os.Chtimes(orphanPath, now, now); err != nil {
		t.Fatal(err)
	}

	st := storage.NewResolver(storage.NewLocalFS())
	rc, _ := st.Open(context.Background(), mdURI)
	md, _ := iceberg.ReadTableMetadata(context.Background(), rc)
	_ = rc.Close()

	rep, err := Scan(context.Background(), md, mdURI, dataDir, st, Options{
		GracePeriod: 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Candidates) != 0 {
		t.Errorf("expected no orphans within grace period, got %+v", rep.Candidates)
	}
}
