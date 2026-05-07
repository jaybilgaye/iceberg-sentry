package catalog

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LocalFS is a directory-backed catalog used for local development, testing,
// and air-gapped deployments. The directory layout is:
//
//	<root>/<namespace>/<table>/metadata/vN.metadata.json
//
// LoadTable returns a pointer to the highest-numbered metadata.json under
// the table's metadata directory. This mirrors what HadoopCatalog produces.
type LocalFS struct {
	Root string
}

// NewLocalFS returns a filesystem-backed catalog rooted at root.
func NewLocalFS(root string) *LocalFS {
	return &LocalFS{Root: root}
}

// Name returns the catalog label.
func (l *LocalFS) Name() string { return fmt.Sprintf("localfs:%s", l.Root) }

func (l *LocalFS) tableDir(id TableID) string {
	if id.Namespace == "" {
		return filepath.Join(l.Root, id.Name)
	}
	return filepath.Join(l.Root, id.Namespace, id.Name)
}

// LoadTable resolves the latest metadata.json on disk.
func (l *LocalFS) LoadTable(_ context.Context, id TableID) (*TableEntry, error) {
	dir := filepath.Join(l.tableDir(id), "metadata")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%s: %w", id, ErrTableNotFound)
		}
		return nil, err
	}
	var candidates []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".metadata.json") {
			candidates = append(candidates, name)
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%s: %w", id, ErrTableNotFound)
	}
	sort.Strings(candidates) // lexical sort is sufficient for vN.metadata.json
	latest := candidates[len(candidates)-1]
	return &TableEntry{
		ID:               id,
		MetadataLocation: filepath.Join(dir, latest),
	}, nil
}

// ListTables walks the catalog root and returns every directory containing a
// metadata/ subdirectory.
func (l *LocalFS) ListTables(_ context.Context, namespace string) ([]TableID, error) {
	root := l.Root
	if namespace != "" {
		root = filepath.Join(l.Root, namespace)
	}
	var out []TableID
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() || d.Name() != "metadata" {
			return nil
		}
		tableDir := filepath.Dir(path)
		rel, err := filepath.Rel(l.Root, tableDir)
		if err != nil {
			return err
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		var id TableID
		switch len(parts) {
		case 1:
			id = TableID{Name: parts[0]}
		case 2:
			id = TableID{Namespace: parts[0], Name: parts[1]}
		default:
			id = TableID{Namespace: strings.Join(parts[:len(parts)-1], "."), Name: parts[len(parts)-1]}
		}
		out = append(out, id)
		return filepath.SkipDir
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return out, nil
}
