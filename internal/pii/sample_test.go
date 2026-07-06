package pii

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/parquet-go/parquet-go"
)

// row is the test schema — three string columns. `name` carries plain text,
// `email` carries genuine emails, `card` carries Luhn-valid card numbers.
type row struct {
	Name  string `parquet:"name"`
	Email string `parquet:"email"`
	Card  string `parquet:"card"`
}

func writeParquet(t *testing.T) ReadAtCloser {
	t.Helper()
	var rows []row
	for i := 0; i < 100; i++ {
		rows = append(rows, row{
			Name:  "Alice",
			Email: fmt.Sprintf("user%d@example.com", i),
			Card:  "4111 1111 1111 1111",
		})
	}
	var buf bytes.Buffer
	if err := parquet.Write[row](&buf, rows); err != nil {
		t.Fatalf("write parquet: %v", err)
	}
	return &readAtBuf{data: buf.Bytes()}
}

type readAtBuf struct{ data []byte }

func (r *readAtBuf) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(r.data)) {
		return 0, io.EOF
	}
	n := copy(p, r.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
func (r *readAtBuf) Close() error { return nil }
func (r *readAtBuf) Size() int64  { return int64(len(r.data)) }

func TestSampleParquetDetectsEmailAndCard(t *testing.T) {
	pf := writeParquet(t)
	a := NewAggregator()
	opts := SampleDefaults()
	opts.RowGroupFraction = 1.0 // deterministic for tests
	opts.RowsPerGroup = 100

	if err := SampleParquet(context.Background(), pf, "ns.t", a, opts); err != nil {
		t.Fatalf("sample: %v", err)
	}
	got := a.Findings("ns.t", 0.5)
	if len(got) == 0 {
		t.Fatalf("expected findings; got none")
	}

	have := map[Type]bool{}
	for _, f := range got {
		have[f.PIIType] = true
	}
	if !have[TypeEmail] {
		t.Errorf("missing EMAIL finding: %+v", got)
	}
	if !have[TypeCreditCard] {
		t.Errorf("missing CREDIT_CARD finding: %+v", got)
	}
}
