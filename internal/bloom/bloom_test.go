package bloom

import (
	"fmt"
	"testing"
)

func TestBasicMembership(t *testing.T) {
	f, err := New(1_000, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{"alpha", "beta", "gamma", "s3://bucket/prefix/a.parquet"}
	for _, k := range keys {
		f.Add(k)
	}
	for _, k := range keys {
		if !f.Test(k) {
			t.Errorf("Test(%q) = false, want true", k)
		}
	}
	if f.Len() != uint64(len(keys)) {
		t.Errorf("Len = %d, want %d", f.Len(), len(keys))
	}
}

func TestFalsePositiveBound(t *testing.T) {
	const n = 5_000
	f, err := New(n, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		f.Add(fmt.Sprintf("in/%d", i))
	}
	const probes = 10_000
	var fp int
	for i := 0; i < probes; i++ {
		if f.Test(fmt.Sprintf("out/%d", i)) {
			fp++
		}
	}
	// Allow 3x the target rate for test stability — Bloom math is probabilistic.
	if rate := float64(fp) / probes; rate > 0.03 {
		t.Errorf("false positive rate %.3f exceeds 0.03 (fp=%d)", rate, fp)
	}
}

func TestNewValidatesArgs(t *testing.T) {
	if _, err := New(0, 0.01); err == nil {
		t.Error("expected error for n=0")
	}
	if _, err := New(10, 0); err == nil {
		t.Error("expected error for p=0")
	}
	if _, err := New(10, 1); err == nil {
		t.Error("expected error for p=1")
	}
}
