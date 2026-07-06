// Package metrics renders iceberg-sentry health.Reports as Prometheus
// exposition-format text and supports two delivery modes:
//
//  1. push gateway — write_text + HTTP PUT to a configured pushgateway URL.
//     Used by short-lived CronJob / CLI invocations.
//  2. serve mode — long-lived HTTP server on /metrics that re-runs the audit
//     on each scrape. Used by Cloudera Manager / Grafana scraping deployments.
//
// We intentionally do not depend on github.com/prometheus/client_golang —
// the metrics surface is small and stable, and avoiding the dep keeps the
// binary < 20MB.
package metrics

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/jaybilgaye/iceberg-sentry/internal/health"
)

// Render writes the report as Prometheus exposition format. One sample per
// dimension is emitted in addition to the table-level aggregates.
func Render(w io.Writer, reports ...health.Report) error {
	bw := bufio.NewWriter(w)
	header := func(name, help, typ string) {
		fmt.Fprintf(bw, "# HELP %s %s\n", name, help)
		fmt.Fprintf(bw, "# TYPE %s %s\n", name, typ)
	}
	header("iceberg_table_health_score", "Composite Iceberg Sentry health score (0..max).", "gauge")
	for _, r := range reports {
		fmt.Fprintf(bw, "iceberg_table_health_score{%s} %d\n", labels(r), r.Score)
	}
	header("iceberg_table_health_max_score", "Maximum possible health score for the dimensions evaluated.", "gauge")
	for _, r := range reports {
		fmt.Fprintf(bw, "iceberg_table_health_max_score{%s} %d\n", labels(r), r.MaxScore)
	}
	header("iceberg_table_dimension_score", "Per-dimension health score.", "gauge")
	for _, r := range reports {
		for _, d := range r.Dimensions {
			fmt.Fprintf(bw, "iceberg_table_dimension_score{%s,dimension=%q} %d\n", labels(r), d.Name, d.Score)
		}
	}
	header("iceberg_table_dimension_max_score", "Per-dimension maximum possible health score.", "gauge")
	for _, r := range reports {
		for _, d := range r.Dimensions {
			fmt.Fprintf(bw, "iceberg_table_dimension_max_score{%s,dimension=%q} %d\n", labels(r), d.Name, d.MaxScore)
		}
	}
	header("iceberg_table_dimension_severity", "Per-dimension severity (0=OK,1=INFO,2=WARNING,3=CRITICAL).", "gauge")
	for _, r := range reports {
		for _, d := range r.Dimensions {
			fmt.Fprintf(bw, "iceberg_table_dimension_severity{%s,dimension=%q} %d\n", labels(r), d.Name, sevValue(d.Severity))
		}
	}
	header("iceberg_table_scan_duration_ms", "Wall-clock duration of the most recent scan.", "gauge")
	for _, r := range reports {
		fmt.Fprintf(bw, "iceberg_table_scan_duration_ms{%s} %d\n", labels(r), r.ScanDurationMS)
	}
	header("iceberg_table_estimated_wastage_bytes", "Storage wastage estimate (orphan + over-retained snapshots).", "gauge")
	for _, r := range reports {
		fmt.Fprintf(bw, "iceberg_table_estimated_wastage_bytes{%s} %d\n", labels(r), r.WastageBytes)
	}
	return bw.Flush()
}

func labels(r health.Report) string {
	parts := []string{
		fmt.Sprintf("table=%q", r.TableID),
		fmt.Sprintf("catalog=%q", r.Catalog),
		fmt.Sprintf("format_version=%q", fmt.Sprint(r.FormatVersion)),
	}
	if r.Branch != "" {
		parts = append(parts, fmt.Sprintf("branch=%q", r.Branch))
	}
	if r.WritePattern != "" {
		parts = append(parts, fmt.Sprintf("write_pattern=%q", r.WritePattern))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func sevValue(s health.Severity) int {
	switch s {
	case health.SeverityCritical:
		return 3
	case health.SeverityWarning:
		return 2
	case health.SeverityInfo:
		return 1
	}
	return 0
}

// Push uploads the exposition payload to a Prometheus push gateway.
// jobName is the value of the `job=` label on the push URL path. instance
// is optional; pass "" to omit.
func Push(ctx context.Context, gatewayURL, jobName, instance string, payload []byte) error {
	if gatewayURL == "" {
		return fmt.Errorf("metrics: gateway URL is required")
	}
	if jobName == "" {
		return fmt.Errorf("metrics: job name is required")
	}
	u := strings.TrimRight(gatewayURL, "/") + "/metrics/job/" + jobName
	if instance != "" {
		u += "/instance/" + instance
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain; version=0.0.4")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("push gateway: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("push gateway status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
