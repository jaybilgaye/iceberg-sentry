package output

import (
	"io"

	"github.com/jaybilgaye/iceberg-sentry/internal/health"
	"github.com/jaybilgaye/iceberg-sentry/internal/metrics"
)

func renderPrometheus(w io.Writer, r health.Report) error {
	return metrics.Render(w, r)
}
