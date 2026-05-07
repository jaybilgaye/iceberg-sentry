package iceberg

import (
	"context"
	"strings"
	"testing"
)

const v2Metadata = `{
  "format-version": 2,
  "table-uuid": "8b2c7d8e-1234-4abc-8def-9876543210ab",
  "location": "file:///tmp/iceberg/finance/transactions",
  "last-updated-ms": 1714900000000,
  "last-column-id": 5,
  "current-schema-id": 0,
  "schemas": [{"schema-id": 0, "type": "struct", "fields": [
    {"id": 1, "name": "id", "required": true, "type": "long"}
  ]}],
  "partition-specs": [{"spec-id": 0, "fields": []}],
  "default-spec-id": 0,
  "last-partition-id": 999,
  "default-sort-order-id": 0,
  "sort-orders": [{"order-id": 0, "fields": []}],
  "current-snapshot-id": 100,
  "snapshots": [
    {"snapshot-id": 100, "sequence-number": 1, "timestamp-ms": 1714000000000,
     "manifest-list": "file:///tmp/iceberg/finance/transactions/metadata/snap-100.avro"}
  ]
}`

func TestReadTableMetadataV2(t *testing.T) {
	md, err := ReadTableMetadata(context.Background(), strings.NewReader(v2Metadata))
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if md.FormatVersion != FormatV2 {
		t.Errorf("format-version = %d, want 2", md.FormatVersion)
	}
	if md.CurrentSnapshotID != 100 {
		t.Errorf("current-snapshot-id = %d, want 100", md.CurrentSnapshotID)
	}
	if cs := md.CurrentSnapshot(); cs == nil || cs.SnapshotID != 100 {
		t.Errorf("CurrentSnapshot returned %v", cs)
	}
}

func TestReadTableMetadataMissingVersion(t *testing.T) {
	_, err := ReadTableMetadata(context.Background(), strings.NewReader(`{"location":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "format-version") {
		t.Fatalf("expected error about format-version, got %v", err)
	}
}

func TestReadTableMetadataUnsupportedVersion(t *testing.T) {
	_, err := ReadTableMetadata(context.Background(), strings.NewReader(`{"format-version": 9}`))
	if err == nil || !strings.Contains(err.Error(), "unsupported format-version") {
		t.Fatalf("expected unsupported error, got %v", err)
	}
}
