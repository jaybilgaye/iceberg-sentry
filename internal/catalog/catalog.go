// Package catalog defines the Catalog interface and Phase 1 adapters
// (LocalFS, AWS Glue, Hive Metastore) used by iceberg-sentry to resolve
// Iceberg table metadata locations.
//
// The interface is deliberately narrow: a catalog's job is to give us back
// the URI of the table's current vN.metadata.json. Everything else is
// handled by the metadata parser and storage layer.
package catalog

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrTableNotFound is returned when a catalog has no entry for the requested
// table identifier.
var ErrTableNotFound = errors.New("table not found in catalog")

// TableID identifies a table within a catalog (typically namespace + name).
type TableID struct {
	Namespace string
	Name      string
}

func (t TableID) String() string {
	if t.Namespace == "" {
		return t.Name
	}
	return t.Namespace + "." + t.Name
}

// ParseTableID accepts forms like "ns.tbl", "ns/tbl", or "tbl" (with empty
// namespace). The first separator wins.
func ParseTableID(s string) (TableID, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return TableID{}, fmt.Errorf("empty table identifier")
	}
	for _, sep := range []string{".", "/"} {
		if i := strings.Index(s, sep); i > 0 {
			return TableID{Namespace: s[:i], Name: s[i+1:]}, nil
		}
	}
	return TableID{Name: s}, nil
}

// TableEntry is what a Catalog returns. Phase 1 only needs the metadata
// location; future phases may extend with table properties for write-pattern
// detection or with explicit branch refs.
type TableEntry struct {
	ID               TableID
	MetadataLocation string
	Properties       map[string]string
}

// Catalog is implemented by every catalog backend.
type Catalog interface {
	// Name returns a human-readable label for diagnostic output.
	Name() string

	// LoadTable resolves the table's current metadata location.
	LoadTable(ctx context.Context, id TableID) (*TableEntry, error)

	// ListTables enumerates tables under the given namespace. An empty
	// namespace means "all visible namespaces".
	ListTables(ctx context.Context, namespace string) ([]TableID, error)
}
