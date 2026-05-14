// Package output renders a health.Report in one of the formats supported by
// the iceberg-sentry CLI. Phase 1 implements text and json. Future phases
// add SARIF, Prometheus, and Markdown.
package output

import (
	"fmt"
	"io"
	"strings"

	"github.com/jaybilgaye/iceberg-sentry/internal/health"
)

// Format identifies an output format.
type Format string

const (
	FormatText  Format = "text"
	FormatJSON  Format = "json"
	FormatSARIF Format = "sarif"
)

// ParseFormat resolves the user-supplied format string.
func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "text":
		return FormatText, nil
	case "json":
		return FormatJSON, nil
	case "sarif":
		return FormatSARIF, nil
	default:
		return "", fmt.Errorf("unsupported output format %q (text|json|sarif)", s)
	}
}

// Render writes r to w in the requested format.
func Render(w io.Writer, r health.Report, fmtKind Format) error {
	switch fmtKind {
	case FormatJSON:
		return renderJSON(w, r)
	case FormatSARIF:
		return renderSARIF(w, r)
	default:
		return renderText(w, r)
	}
}
