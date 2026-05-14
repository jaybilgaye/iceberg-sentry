package migration

import (
	"context"
	"testing"

	"github.com/jaybilgaye/iceberg-sentry/internal/iceberg"
	"github.com/jaybilgaye/iceberg-sentry/internal/storage"
)

func TestAuditHDFSPropertyAndPathHigh(t *testing.T) {
	md := &iceberg.TableMetadata{
		FormatVersion: 2,
		Location:      "hdfs://nn/user/hive/warehouse/finance/transactions",
		Properties: map[string]string{
			"write.metadata.path": "/tmp/iceberg-meta",
		},
		Snapshots: []iceberg.Snapshot{
			{SnapshotID: 1, ManifestList: "hdfs://nn/user/hive/warehouse/finance/transactions/metadata/snap-1.avro"},
		},
		CurrentSnapshotID: 1,
	}
	r, err := Audit(context.Background(), "finance.transactions", md, "hdfs://nn/.../v1.metadata.json", storage.NewResolver())
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if r.Risk != RiskHigh {
		t.Errorf("risk = %s, want HIGH", r.Risk)
	}
	codes := map[string]int{}
	for _, f := range r.Findings {
		codes[f.Code]++
	}
	for _, want := range []string{"HDFS_PATH", "HDFS_PROPERTY"} {
		if codes[want] == 0 {
			t.Errorf("missing finding code %s; have %+v", want, codes)
		}
	}
}

func TestAuditV1IsMedium(t *testing.T) {
	md := &iceberg.TableMetadata{
		FormatVersion: 1,
		Location:      "s3://bucket/finance/transactions",
		Snapshots: []iceberg.Snapshot{{SnapshotID: 1, ManifestList: "s3://bucket/m/snap.avro"}},
		CurrentSnapshotID: 1,
	}
	r, _ := Audit(context.Background(), "finance.transactions", md, "s3://bucket/m/v1.metadata.json", storage.NewResolver())
	if r.Risk != RiskMedium {
		t.Errorf("risk = %s, want MEDIUM", r.Risk)
	}
}

func TestAuditCleanS3IsLow(t *testing.T) {
	md := &iceberg.TableMetadata{
		FormatVersion: 2,
		Location:      "s3://bucket/finance/transactions",
		Properties:    map[string]string{"write.format.default": "parquet"},
		Snapshots: []iceberg.Snapshot{{SnapshotID: 1, ManifestList: "s3://bucket/m/snap.avro"}},
		CurrentSnapshotID: 1,
	}
	r, _ := Audit(context.Background(), "finance.transactions", md, "s3://bucket/m/v1.metadata.json", storage.NewResolver())
	if r.Risk != RiskLow {
		t.Errorf("risk = %s, want LOW; findings=%+v", r.Risk, r.Findings)
	}
}
