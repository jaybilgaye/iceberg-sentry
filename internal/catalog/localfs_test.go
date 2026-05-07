package catalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalFSLoadTableLatest(t *testing.T) {
	root := t.TempDir()
	tableDir := filepath.Join(root, "finance", "transactions", "metadata")
	if err := os.MkdirAll(tableDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"v1.metadata.json", "v2.metadata.json", "v3.metadata.json"} {
		if err := os.WriteFile(filepath.Join(tableDir, n), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cat := NewLocalFS(root)
	entry, err := cat.LoadTable(context.Background(), TableID{Namespace: "finance", Name: "transactions"})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if filepath.Base(entry.MetadataLocation) != "v3.metadata.json" {
		t.Errorf("got %s, want v3.metadata.json", entry.MetadataLocation)
	}
}

func TestLocalFSMissingTable(t *testing.T) {
	cat := NewLocalFS(t.TempDir())
	_, err := cat.LoadTable(context.Background(), TableID{Namespace: "x", Name: "y"})
	if !errors.Is(err, ErrTableNotFound) {
		t.Errorf("err = %v, want ErrTableNotFound", err)
	}
}

func TestLocalFSListTables(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{
		"finance/transactions/metadata",
		"finance/orders/metadata",
		"raw/clicks/metadata",
	} {
		if err := os.MkdirAll(filepath.Join(root, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cat := NewLocalFS(root)
	got, err := cat.ListTables(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("got %d tables, want 3: %+v", len(got), got)
	}

	got, err = cat.ListTables(context.Background(), "finance")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("got %d, want 2: %+v", len(got), got)
	}
}

func TestParseTableID(t *testing.T) {
	cases := []struct{ in, ns, name string }{
		{"a.b", "a", "b"},
		{"a/b", "a", "b"},
		{"x", "", "x"},
		{"a.b.c", "a", "b.c"},
	}
	for _, c := range cases {
		got, err := ParseTableID(c.in)
		if err != nil {
			t.Errorf("%s: %v", c.in, err)
			continue
		}
		if got.Namespace != c.ns || got.Name != c.name {
			t.Errorf("%s -> %+v, want ns=%s name=%s", c.in, got, c.ns, c.name)
		}
	}
}
