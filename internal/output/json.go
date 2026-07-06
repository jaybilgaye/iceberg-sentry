package output

import (
	"encoding/json"
	"io"

	"github.com/jaybilgaye/iceberg-sentry/internal/health"
)

func renderJSON(w io.Writer, r health.Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}
