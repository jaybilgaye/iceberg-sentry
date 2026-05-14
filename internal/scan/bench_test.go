package scan

import (
	"context"
	"testing"
	"time"

	"github.com/jaybilgaye/iceberg-sentry/internal/catalog"
	"github.com/jaybilgaye/iceberg-sentry/internal/health"
	"github.com/jaybilgaye/iceberg-sentry/internal/storage"
)

// BenchmarkScanSmallTable exercises the end-to-end scan path against a
// fixture small enough to fit in tmpfs. The spec targets < 10s for 1,000
// manifests — this benchmark intentionally uses ~50 data files; a CI
// dashboard tracks the per-op time and fails on > 20% regression.
func BenchmarkScanSmallTable(b *testing.B) {
	root := buildTableFixture(b, 50, 0, 256*1024*1024)
	cat := catalog.NewLocalFS(root)
	st := storage.NewResolver(storage.NewLocalFS())
	eng := NewEngine(cat, st)
	eng.Now = func() time.Time { return time.Now() }

	id := catalog.TableID{Namespace: "finance", Name: "transactions"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := eng.Scan(context.Background(), id, health.Defaults())
		if err != nil {
			b.Fatal(err)
		}
	}
}
