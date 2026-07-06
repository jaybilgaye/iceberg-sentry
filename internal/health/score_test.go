package health

import "testing"

func TestScoreHealthyV2Table(t *testing.T) {
	s := &Stats{
		FormatVersion:          2,
		SnapshotID:             42,
		DataFileCount:          100,
		DataFileTotalBytes:     100 * 256 * 1024 * 1024,
		SmallFileCountUnder128: 5,
		ManifestFileCount:      8,
		SnapshotCount:          5,
		OldestSnapshotAgeMs:    int64((24 * 7) * 60 * 60 * 1000),
	}
	r := Score("a.b", "test", s, Defaults())
	if r.WorstSeverity != SeverityOK {
		t.Errorf("worst severity = %s, want OK", r.WorstSeverity)
	}
	if r.Score != r.MaxScore {
		t.Errorf("score=%d max=%d for healthy table; want full score", r.Score, r.MaxScore)
	}
}

func TestScoreCriticalDeleteAmplification(t *testing.T) {
	s := &Stats{
		FormatVersion:       2,
		DataFileCount:       100,
		PositionDeleteFiles: 60,
		EqualityDeleteFiles: 10,
	}
	r := Score("a.b", "test", s, Defaults())
	var dim *Dimension
	for i := range r.Dimensions {
		if r.Dimensions[i].Name == "delete_amplification" {
			dim = &r.Dimensions[i]
			break
		}
	}
	if dim == nil {
		t.Fatal("missing delete_amplification dimension")
	}
	if dim.Severity != SeverityCritical {
		t.Errorf("severity = %s, want CRITICAL", dim.Severity)
	}
	if r.WorstSeverity != SeverityCritical {
		t.Errorf("worst = %s, want CRITICAL", r.WorstSeverity)
	}
}

func TestScoreFormatV1IsInfo(t *testing.T) {
	s := &Stats{FormatVersion: 1, DataFileCount: 1, ManifestFileCount: 1, SnapshotCount: 1}
	r := Score("a.b", "test", s, Defaults())
	for _, d := range r.Dimensions {
		if d.Name == "format_version" {
			if d.Severity != SeverityInfo {
				t.Errorf("v1 severity = %s, want INFO", d.Severity)
			}
			return
		}
	}
	t.Fatal("format_version dimension missing")
}

func TestScoreManifestDensityCritical(t *testing.T) {
	s := &Stats{FormatVersion: 2, DataFileCount: 1, ManifestFileCount: 9_999}
	r := Score("a.b", "test", s, Defaults())
	for _, d := range r.Dimensions {
		if d.Name == "manifest_density" {
			if d.Severity != SeverityCritical {
				t.Errorf("severity = %s, want CRITICAL", d.Severity)
			}
			return
		}
	}
	t.Fatal("manifest_density dimension missing")
}
