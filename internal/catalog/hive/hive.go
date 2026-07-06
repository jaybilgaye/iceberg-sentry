// Package hive is a Hive Metastore (HMS) catalog adapter.
//
// Only the RPCs iceberg-sentry actually needs are implemented: get_table to
// resolve a table's metadata_location, and get_all_tables for listing. The
// adapter speaks Thrift binary protocol over TCP via the apache/thrift Go
// runtime; this avoids pulling thrift-generated code (thousands of lines)
// for two RPCs.
//
// Authentication: Phase 1 supports plain (no auth) and SASL/Kerberos
// transports if a custom thrift.TTransport factory is provided via the
// Transport option. See docs/hive-metastore.md for Kerberos setup.
package hive

import (
	"context"
	"errors"
	"fmt"

	"github.com/apache/thrift/lib/go/thrift"

	"github.com/jaybilgaye/iceberg-sentry/internal/catalog"
)

// Hive is the catalog adapter.
type Hive struct {
	host        string
	port        int
	transportFn func() (thrift.TTransport, error)
	protocolFn  func(thrift.TTransport) thrift.TProtocol
}

// Option configures the adapter.
type Option func(*Hive)

// WithTransport overrides the TCP transport factory (e.g. for SASL).
func WithTransport(fn func() (thrift.TTransport, error)) Option {
	return func(h *Hive) { h.transportFn = fn }
}

// WithProtocol overrides the protocol factory (default: TBinaryProtocol).
func WithProtocol(fn func(thrift.TTransport) thrift.TProtocol) Option {
	return func(h *Hive) { h.protocolFn = fn }
}

// New builds a Hive Metastore catalog adapter for the given host:port.
func New(host string, port int, opts ...Option) *Hive {
	h := &Hive{host: host, port: port}
	for _, o := range opts {
		o(h)
	}
	if h.transportFn == nil {
		h.transportFn = func() (thrift.TTransport, error) {
			return thrift.NewTSocketConf(addr(h.host, h.port), nil), nil
		}
	}
	if h.protocolFn == nil {
		h.protocolFn = func(t thrift.TTransport) thrift.TProtocol {
			return thrift.NewTBinaryProtocolConf(t, nil)
		}
	}
	return h
}

func addr(host string, port int) string {
	return fmt.Sprintf("%s:%d", host, port)
}

// Name returns the catalog label.
func (h *Hive) Name() string { return fmt.Sprintf("hive:%s:%d", h.host, h.port) }

func (h *Hive) dial() (*thriftClient, error) {
	t, err := h.transportFn()
	if err != nil {
		return nil, fmt.Errorf("hive transport: %w", err)
	}
	if err := t.Open(); err != nil {
		return nil, fmt.Errorf("hive open: %w", err)
	}
	return &thriftClient{
		transport: t,
		protocol:  h.protocolFn(t),
	}, nil
}

// LoadTable resolves metadata_location via get_table.
func (h *Hive) LoadTable(ctx context.Context, id catalog.TableID) (*catalog.TableEntry, error) {
	c, err := h.dial()
	if err != nil {
		return nil, err
	}
	defer c.Close()

	params, err := c.getTableParameters(ctx, id.Namespace, id.Name)
	if err != nil {
		if errors.Is(err, errNoSuchObject) {
			return nil, fmt.Errorf("%s: %w", id, catalog.ErrTableNotFound)
		}
		return nil, fmt.Errorf("hive get_table %s: %w", id, err)
	}
	loc, ok := lookupMetadataLocation(params)
	if !ok {
		return nil, fmt.Errorf("%s: hive table has no metadata_location parameter (is it an Iceberg table?)", id)
	}
	return &catalog.TableEntry{
		ID:               id,
		MetadataLocation: loc,
		Properties:       params,
	}, nil
}

// ListTables enumerates table names in a database via get_all_tables. No
// Iceberg-vs-Hive filtering is applied here; LoadTable will surface a clear
// error for non-Iceberg tables.
func (h *Hive) ListTables(ctx context.Context, namespace string) ([]catalog.TableID, error) {
	if namespace == "" {
		return nil, fmt.Errorf("hive catalog requires a non-empty namespace for listing")
	}
	c, err := h.dial()
	if err != nil {
		return nil, err
	}
	defer c.Close()

	names, err := c.getAllTables(ctx, namespace)
	if err != nil {
		return nil, fmt.Errorf("hive get_all_tables %s: %w", namespace, err)
	}
	out := make([]catalog.TableID, 0, len(names))
	for _, n := range names {
		out = append(out, catalog.TableID{Namespace: namespace, Name: n})
	}
	return out, nil
}

func lookupMetadataLocation(params map[string]string) (string, bool) {
	for _, key := range []string{"metadata_location", "metadata-location", "MetadataLocation"} {
		if v, ok := params[key]; ok && v != "" {
			return v, true
		}
	}
	return "", false
}
