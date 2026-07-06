package output

import (
	"fmt"
	"io"
	"strings"

	"github.com/jaybilgaye/iceberg-sentry/internal/health"
)

const ruler = "  ─────────────────────────────────────────────────────────────────"

func renderText(w io.Writer, r health.Report) error {
	indicator := "OK"
	switch r.WorstSeverity {
	case health.SeverityCritical:
		indicator = "CRITICAL"
	case health.SeverityWarning:
		indicator = "WARNING"
	case health.SeverityInfo:
		indicator = "INFO"
	}

	if _, err := fmt.Fprintf(w, "\n  Table: %s  │  Score: %d/%d  %s\n", r.TableID, r.Score, r.MaxScore, indicator); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, ruler); err != nil {
		return err
	}

	for _, d := range r.Dimensions {
		tag := fmt.Sprintf("[%s]", d.Severity)
		if _, err := fmt.Fprintf(w, "  %-12s %-22s %2d/%-2d  %s\n", tag, d.Name, d.Score, d.MaxScore, d.Summary); err != nil {
			return err
		}
		if d.Remediation != "" {
			if _, err := fmt.Fprintf(w, "  %12s   → %s\n", "", d.Remediation); err != nil {
				return err
			}
		}
	}

	if _, err := fmt.Fprintln(w, ruler); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "  Catalog: %s  │  Snapshot: %d  │  Format: v%d  │  Scan: %dms\n\n",
		r.Catalog, r.SnapshotID, r.FormatVersion, r.ScanDurationMS); err != nil {
		return err
	}
	return nil
}

// Truncate is exported so callers can format remediation strings consistently.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimRight(s[:n-1], " ") + "…"
}
