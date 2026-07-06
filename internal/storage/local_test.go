package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalFSOpenAndList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata", "v1.metadata.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	fs := NewLocalFS()
	rc, err := fs.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	body, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "{}" {
		t.Errorf("got %q want %q", body, "{}")
	}

	var seen []string
	if err := fs.List(context.Background(), dir, func(o ObjectInfo) error {
		seen = append(seen, o.URI)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0] != path {
		t.Errorf("list = %v", seen)
	}
}

func TestLocalFSNotFound(t *testing.T) {
	fs := NewLocalFS()
	_, err := fs.Open(context.Background(), "/tmp/definitely-does-not-exist-iceberg-sentry")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestResolverDispatch(t *testing.T) {
	r := NewResolver(NewLocalFS())
	if _, err := r.Open(context.Background(), "s3://nope/key"); err == nil {
		t.Errorf("expected dispatch error for unregistered scheme")
	}
}
