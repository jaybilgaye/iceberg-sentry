// Package storage defines the Storage Abstraction Layer (SAL): a single
// streaming-iterator interface over S3, ADLS, GCS, HDFS, and the local
// filesystem. Phase 1 ships Local, S3, and HDFS implementations.
//
// The interface is intentionally narrow — the diagnostic engine only needs
// to open files for streaming reads and (for orphan detection in Phase 2)
// to list objects under a prefix.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// ErrNotFound is returned by Open when the requested object does not exist.
var ErrNotFound = errors.New("object not found")

// ObjectInfo is a minimal metadata record returned by List.
type ObjectInfo struct {
	URI       string
	Size      int64
	UpdatedAt int64 // unix millis; 0 if unknown
}

// Storage is implemented by every backend.
type Storage interface {
	// Open returns a ReadCloser for the object identified by uri (typically
	// a fully-qualified URI like s3://bucket/key or file:///path). Backends
	// must return ErrNotFound when the object is absent.
	Open(ctx context.Context, uri string) (io.ReadCloser, error)

	// List streams object metadata under the given URI prefix. The visit
	// callback should return a non-nil error to stop iteration early.
	List(ctx context.Context, prefix string, visit func(ObjectInfo) error) error

	// Scheme returns the URI scheme this backend serves (e.g. "s3", "file").
	Scheme() string
}

// Resolver dispatches Open/List calls to the registered backend whose Scheme
// matches the URI. It is the public entry point for the SAL.
type Resolver struct {
	backends map[string]Storage
}

// NewResolver returns a Resolver pre-populated with the provided backends.
// A backend can be registered for multiple aliases via Register.
func NewResolver(backends ...Storage) *Resolver {
	r := &Resolver{backends: map[string]Storage{}}
	for _, b := range backends {
		r.Register(b.Scheme(), b)
	}
	return r
}

// Register adds (or replaces) a backend under the given scheme.
func (r *Resolver) Register(scheme string, b Storage) {
	r.backends[strings.ToLower(scheme)] = b
}

// Open dispatches to the matching backend.
func (r *Resolver) Open(ctx context.Context, uri string) (io.ReadCloser, error) {
	b, err := r.lookup(uri)
	if err != nil {
		return nil, err
	}
	return b.Open(ctx, uri)
}

// List dispatches to the matching backend.
func (r *Resolver) List(ctx context.Context, prefix string, visit func(ObjectInfo) error) error {
	b, err := r.lookup(prefix)
	if err != nil {
		return err
	}
	return b.List(ctx, prefix, visit)
}

func (r *Resolver) lookup(uri string) (Storage, error) {
	scheme := SchemeOf(uri)
	if scheme == "" {
		// Bare paths default to local file storage if registered.
		scheme = "file"
	}
	if b, ok := r.backends[scheme]; ok {
		return b, nil
	}
	return nil, fmt.Errorf("no storage backend registered for scheme %q (uri=%q)", scheme, uri)
}

// SchemeOf returns the lowercased URI scheme. Returns "" for bare paths.
func SchemeOf(uri string) string {
	if i := strings.Index(uri, "://"); i > 0 {
		return strings.ToLower(uri[:i])
	}
	return ""
}

// SplitURI returns (scheme, host, path) for a URI. The leading slash on path
// is preserved when present.
func SplitURI(uri string) (scheme, host, path string, err error) {
	if !strings.Contains(uri, "://") {
		return "file", "", uri, nil
	}
	u, perr := url.Parse(uri)
	if perr != nil {
		return "", "", "", fmt.Errorf("parse %q: %w", uri, perr)
	}
	return strings.ToLower(u.Scheme), u.Host, u.Path, nil
}
