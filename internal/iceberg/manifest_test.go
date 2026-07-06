package iceberg

import (
	"bytes"
	"context"
	"testing"

	"github.com/hamba/avro/v2/ocf"
)

// Minimal subset of the Iceberg manifest_list Avro schema. We use the actual
// field names so the parser exercises the production code path.
const manifestListSchema = `{
  "type": "record",
  "name": "manifest_file",
  "fields": [
    {"name": "manifest_path",      "type": "string"},
    {"name": "manifest_length",    "type": "long"},
    {"name": "partition_spec_id",  "type": "int"},
    {"name": "content",            "type": "int"},
    {"name": "sequence_number",    "type": "long"},
    {"name": "min_sequence_number","type": "long"},
    {"name": "added_snapshot_id",  "type": "long"},
    {"name": "added_files_count",  "type": "int"},
    {"name": "existing_files_count","type": "int"},
    {"name": "deleted_files_count","type": "int"},
    {"name": "added_rows_count",   "type": "long"},
    {"name": "existing_rows_count","type": "long"},
    {"name": "deleted_rows_count", "type": "long"}
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

func writeManifestList(t *testing.T, entries []map[string]any) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc, err := ocf.NewEncoder(manifestListSchema, &buf)
	if err != nil {
		t.Fatalf("encoder: %v", err)
	}
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("close encoder: %v", err)
	}
	return buf.Bytes()
}

func writeManifestFile(t *testing.T, entries []map[string]any) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc, err := ocf.NewEncoder(manifestEntrySchema, &buf)
	if err != nil {
		t.Fatalf("encoder: %v", err)
	}
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("close encoder: %v", err)
	}
	return buf.Bytes()
}

func TestReadManifestList(t *testing.T) {
	body := writeManifestList(t, []map[string]any{
		{
			"manifest_path":        "manifests/m1.avro",
			"manifest_length":      int64(1024),
			"partition_spec_id":    int32(0),
			"content":              int32(0),
			"sequence_number":      int64(1),
			"min_sequence_number":  int64(1),
			"added_snapshot_id":    int64(100),
			"added_files_count":    int32(3),
			"existing_files_count": int32(0),
			"deleted_files_count":  int32(0),
			"added_rows_count":     int64(30),
			"existing_rows_count":  int64(0),
			"deleted_rows_count":   int64(0),
		},
		{
			"manifest_path":        "manifests/m2.avro",
			"manifest_length":      int64(2048),
			"partition_spec_id":    int32(0),
			"content":              int32(1),
			"sequence_number":      int64(2),
			"min_sequence_number":  int64(2),
			"added_snapshot_id":    int64(101),
			"added_files_count":    int32(2),
			"existing_files_count": int32(0),
			"deleted_files_count":  int32(0),
			"added_rows_count":     int64(20),
			"existing_rows_count":  int64(0),
			"deleted_rows_count":   int64(0),
		},
	})
	mfs, err := ReadManifestList(context.Background(), bytes.NewReader(body))
	if err != nil {
		t.Fatalf("read manifest list: %v", err)
	}
	if len(mfs) != 2 {
		t.Fatalf("got %d manifests, want 2", len(mfs))
	}
	if mfs[0].Content != ManifestContentData || mfs[1].Content != ManifestContentDeletes {
		t.Errorf("content classification wrong: %+v", mfs)
	}
}

func TestReadManifestFile(t *testing.T) {
	body := writeManifestFile(t, []map[string]any{
		{
			"status":          int32(1),
			"snapshot_id":     int64(100),
			"sequence_number": int64(1),
			"data_file": map[string]any{
				"content":            int32(0),
				"file_path":          "data/00000.parquet",
				"file_format":        "PARQUET",
				"record_count":       int64(10),
				"file_size_in_bytes": int64(50 * 1024 * 1024),
			},
		},
		{
			"status":          int32(1),
			"snapshot_id":     int64(100),
			"sequence_number": int64(1),
			"data_file": map[string]any{
				"content":            int32(1),
				"file_path":          "deletes/pos.parquet",
				"file_format":        "PARQUET",
				"record_count":       int64(2),
				"file_size_in_bytes": int64(2048),
			},
		},
	})

	var data, posDel int
	err := ReadManifestFile(context.Background(), bytes.NewReader(body), func(df DataFile) error {
		switch df.Content {
		case FileContentData:
			data++
		case FileContentPositionDeletes:
			posDel++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read manifest file: %v", err)
	}
	if data != 1 || posDel != 1 {
		t.Errorf("data=%d posDel=%d, want 1/1", data, posDel)
	}
}
