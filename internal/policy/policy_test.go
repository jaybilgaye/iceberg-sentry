package policy

import (
	"strings"
	"testing"
	"time"

	"github.com/jaybilgaye/iceberg-sentry/internal/health"
)

const sample = `
version: "1.0"
default_catalog: localfs
policies:
  - name: finance-compliance
    target_namespace: "finance.*"
    min_file_size_mb: 256
    max_manifest_files: 1000
    max_snapshot_age: 14d
    delete_file_ratio_warn: 0.05
    delete_file_ratio_fail: 0.20
    min_health_score: 80
  - name: raw-relaxed
    target_namespace: "raw.*"
    min_file_size_mb: 32
    max_snapshot_age: 7d
`

func TestParseAndMatch(t *testing.T) {
	f, err := Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if f.Version != "1.0" || len(f.Policies) != 2 {
		t.Fatalf("unexpected file %+v", f)
	}
	if p := f.MatchTable("finance"); p == nil || p.Name != "finance-compliance" {
		t.Errorf("finance match = %+v", p)
	}
	if p := f.MatchTable("finance.transactions"); p == nil || p.Name != "finance-compliance" {
		t.Errorf("finance.transactions match = %+v", p)
	}
	if p := f.MatchTable("raw.clicks"); p == nil || p.Name != "raw-relaxed" {
		t.Errorf("raw.clicks match = %+v", p)
	}
	if p := f.MatchTable("nope"); p != nil {
		t.Errorf("nope unexpectedly matched %+v", p)
	}
}

func TestApplyThresholds(t *testing.T) {
	f, err := Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	p := f.MatchTable("finance.transactions")
	got, err := p.ApplyThresholds(health.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if got.MinFileSizeBytes != 256*1024*1024 {
		t.Errorf("min size = %d", got.MinFileSizeBytes)
	}
	if got.WarnSnapshotAge != 14*24*time.Hour {
		t.Errorf("warn age = %s", got.WarnSnapshotAge)
	}
	if got.WarnDeleteRatio != 0.05 || got.CritDeleteRatio != 0.20 {
		t.Errorf("delete ratios = %v / %v", got.WarnDeleteRatio, got.CritDeleteRatio)
	}
}

func TestParseRequiresVersion(t *testing.T) {
	_, err := Parse(strings.NewReader("policies: []"))
	if err == nil {
		t.Fatal("expected error")
	}
}
