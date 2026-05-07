package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// LocalFS is a Storage backend rooted at the local filesystem. It accepts
// both bare paths and file:// URIs.
type LocalFS struct{}

// NewLocalFS returns a new local-filesystem backend.
func NewLocalFS() *LocalFS { return &LocalFS{} }

// Scheme returns "file".
func (l *LocalFS) Scheme() string { return "file" }

func (l *LocalFS) toPath(uri string) (string, error) {
	if strings.HasPrefix(uri, "file://") {
		_, _, p, err := SplitURI(uri)
		if err != nil {
			return "", err
		}
		return p, nil
	}
	return uri, nil
}

// Open returns a ReadCloser for the local file.
func (l *LocalFS) Open(_ context.Context, uri string) (io.ReadCloser, error) {
	p, err := l.toPath(uri)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%s: %w", uri, ErrNotFound)
		}
		return nil, err
	}
	return f, nil
}

// List walks the directory tree under prefix, emitting one ObjectInfo per regular file.
func (l *LocalFS) List(_ context.Context, prefix string, visit func(ObjectInfo) error) error {
	root, err := l.toPath(prefix)
	if err != nil {
		return err
	}
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		uri := path
		if strings.HasPrefix(prefix, "file://") {
			uri = "file://" + path
		}
		return visit(ObjectInfo{
			URI:       uri,
			Size:      info.Size(),
			UpdatedAt: info.ModTime().UnixMilli(),
		})
	})
}
