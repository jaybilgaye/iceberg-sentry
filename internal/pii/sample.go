package pii

import (
	"context"
	"fmt"
	"io"
	"math/rand"

	"github.com/parquet-go/parquet-go"

	"github.com/jaybilgaye/iceberg-sentry/internal/storage"
)

// SampleOptions tunes the row-group sampler.
type SampleOptions struct {
	// RowGroupFraction is the probability that a given row group is read
	// (0..1). Default 0.05 (5%) matches spec §2.8.
	RowGroupFraction float64
	// RowsPerGroup caps how many rows from a single group are scanned.
	RowsPerGroup int
	// Rand can be nil; tests provide a deterministic source.
	Rand *rand.Rand
}

// Defaults returns the spec defaults.
func SampleDefaults() SampleOptions {
	return SampleOptions{
		RowGroupFraction: 0.05,
		RowsPerGroup:     1024,
	}
}

// ReadAtCloser is the union storage backends need to expose for Parquet
// random access. The Resolver's ReadAt facility is implemented per backend;
// here we accept anything that lets parquet-go seek inside a single file.
type ReadAtCloser interface {
	io.ReaderAt
	io.Closer
	Size() int64
}

// SampleParquet streams string-typed columns from a Parquet file, samples
// rows from a fraction of row groups, and records each value into the
// aggregator. Non-string columns are skipped (PII patterns target text).
func SampleParquet(
	_ context.Context,
	r ReadAtCloser,
	table string,
	agg *Aggregator,
	opts SampleOptions,
) error {
	if opts.RowGroupFraction <= 0 {
		opts.RowGroupFraction = 0.05
	}
	if opts.RowsPerGroup <= 0 {
		opts.RowsPerGroup = 1024
	}
	rng := opts.Rand
	if rng == nil {
		rng = rand.New(rand.NewSource(1))
	}

	pf, err := parquet.OpenFile(r, r.Size())
	if err != nil {
		return fmt.Errorf("open parquet: %w", err)
	}

	for _, rg := range pf.RowGroups() {
		if rng.Float64() > opts.RowGroupFraction {
			continue
		}
		if err := sampleRowGroup(rg, table, agg, opts); err != nil {
			return err
		}
	}
	return nil
}

func sampleRowGroup(rg parquet.RowGroup, _table string, agg *Aggregator, opts SampleOptions) error {
	schema := rg.Schema()
	cols := schema.Columns()
	for i, col := range cols {
		if !isStringColumn(schema, col) {
			continue
		}
		chunk := rg.ColumnChunks()[i]
		colName := joinPath(col)
		pages := chunk.Pages()
		defer pages.Close()

		seen := 0
		for seen < opts.RowsPerGroup {
			page, err := pages.ReadPage()
			if err != nil {
				if err == io.EOF {
					break
				}
				return err
			}
			values := page.Values()
			buf := make([]parquet.Value, 256)
			for seen < opts.RowsPerGroup {
				n, err := values.ReadValues(buf)
				for k := 0; k < n; k++ {
					if buf[k].IsNull() {
						continue
					}
					agg.Record(colName, buf[k].String())
					seen++
					if seen >= opts.RowsPerGroup {
						break
					}
				}
				if err != nil {
					if err == io.EOF {
						break
					}
					return err
				}
			}
			parquet.Release(page)
		}
	}
	return nil
}

func isStringColumn(schema *parquet.Schema, path []string) bool {
	leaf, ok := schema.Lookup(path...)
	if !ok || leaf.Node == nil {
		return false
	}
	t := leaf.Node.Type()
	if t == nil {
		return false
	}
	return t.Kind() == parquet.ByteArray
}

func joinPath(p []string) string {
	if len(p) == 1 {
		return p[0]
	}
	out := p[0]
	for i := 1; i < len(p); i++ {
		out += "." + p[i]
	}
	return out
}

// storageReadAt wraps a storage backend object into the ReadAtCloser
// interface parquet-go expects. We materialise the object once on Open so
// random access works regardless of backend. Phase 3 will add HTTP Range
// Requests for S3 to avoid this materialisation cost.
type storageReadAt struct {
	data []byte
}

// OpenForSampling fetches the object via the storage resolver and returns a
// ReadAtCloser. The full payload is held in memory — appropriate for the
// per-Parquet-file PII sampler (Parquet files in Iceberg are typically
// 64MB–1GB).
func OpenForSampling(ctx context.Context, st *storage.Resolver, uri string) (ReadAtCloser, error) {
	rc, err := st.Open(ctx, uri)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	body, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	return &storageReadAt{data: body}, nil
}

func (s *storageReadAt) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off >= int64(len(s.data)) {
		return 0, io.EOF
	}
	n := copy(p, s.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (s *storageReadAt) Close() error { s.data = nil; return nil }
func (s *storageReadAt) Size() int64  { return int64(len(s.data)) }
