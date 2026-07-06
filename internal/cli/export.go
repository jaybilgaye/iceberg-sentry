package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/jaybilgaye/iceberg-sentry/internal/catalog"
	"github.com/jaybilgaye/iceberg-sentry/internal/exitcode"
	"github.com/jaybilgaye/iceberg-sentry/internal/health"
	"github.com/jaybilgaye/iceberg-sentry/internal/metrics"
	"github.com/jaybilgaye/iceberg-sentry/internal/scan"
)

type exportFlags struct {
	auditFlags
	listen   string
	interval string
}

func newExportCmd() *cobra.Command {
	f := &exportFlags{}
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Long-lived Prometheus exporter (serves /metrics)",
		Long: `export runs a background scan loop and exposes the most recent
results as Prometheus exposition format on the configured listen address.

Use this when Cloudera Manager / Grafana / a Kubernetes ServiceMonitor scrapes
metrics. For one-shot push to a pushgateway, use 'audit --format prometheus
--push-gateway URL' instead.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runExport(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), f)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&f.catalogKind, "catalog", "localfs", "catalog adapter: localfs|glue|hive|rest")
	flags.StringVar(&f.catalogRoot, "catalog-root", "", "root directory for the localfs catalog")
	flags.StringVar(&f.hiveAddr, "hive", "", "host:port for the hive metastore")
	flags.StringVar(&f.restURL, "rest", "", "Iceberg REST catalog base URL")
	flags.StringVar(&f.restToken, "rest-token", "", "bearer token for the REST catalog")
	flags.StringVar(&f.glueCatalog, "glue-catalog", "", "AWS account ID owning the Glue catalog")
	flags.StringVar(&f.hdfsURL, "hdfs", "", "WebHDFS root URL")
	flags.StringVar(&f.namespace, "namespace", "", "namespace to audit (all Iceberg tables in it)")
	flags.StringVar(&f.tableID, "table", "", "fully qualified table identifier (mutex with --namespace)")
	flags.StringVar(&f.listen, "listen", ":9400", "listen address for /metrics")
	flags.StringVar(&f.interval, "interval", "5m", "interval between background re-scans")
	flags.BoolVar(&f.pathStyle, "s3-path-style", false, "use S3 path-style addressing")
	return cmd
}

func runExport(ctx context.Context, stdout, stderr io.Writer, f *exportFlags) error {
	interval, err := time.ParseDuration(f.interval)
	if err != nil {
		return &codedError{code: exitcode.ConfigError, err: fmt.Errorf("invalid --interval: %w", err)}
	}

	state := &exporterState{}
	scanOnce := func() {
		body, err := runOneScan(ctx, &f.auditFlags)
		if err != nil {
			fmt.Fprintf(stderr, "scan error: %v\n", err)
			state.set(nil, time.Now(), err)
			return
		}
		state.set(body, time.Now(), nil)
	}
	scanOnce()
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				scanOnce()
			}
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		body, ts, err := state.get()
		if err != nil {
			http.Error(w, fmt.Sprintf("# scan error: %v\n", err), http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprintf(w, "# scanned_at %s\n", ts.UTC().Format(time.RFC3339))
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _, err := state.get()
		if err != nil {
			http.Error(w, "scan-error", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})

	fmt.Fprintf(stdout, "iceberg-sentry export: listening on %s (scan interval=%s)\n", f.listen, interval)
	srv := &http.Server{
		Addr:              f.listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return &codedError{code: exitcode.ConnectionFail, err: err}
	}
}

type exporterState struct {
	mu   sync.RWMutex
	body []byte
	ts   time.Time
	err  error
}

func (s *exporterState) set(body []byte, ts time.Time, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.body, s.ts, s.err = body, ts, err
}

func (s *exporterState) get() ([]byte, time.Time, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.body, s.ts, s.err
}

// runOneScan re-uses the audit catalog/storage builders and the scan engine
// to produce the Prometheus exposition payload for a single pass.
func runOneScan(ctx context.Context, f *auditFlags) ([]byte, error) {
	cat, err := buildCatalogFromAudit(ctx, f)
	if err != nil {
		return nil, err
	}
	st, err := buildStorage(ctx, f)
	if err != nil {
		return nil, err
	}

	var ids []catalog.TableID
	switch {
	case f.tableID != "":
		id, err := catalog.ParseTableID(f.tableID)
		if err != nil {
			return nil, err
		}
		ids = []catalog.TableID{id}
	case f.namespace != "":
		listed, err := cat.ListTables(ctx, f.namespace)
		if err != nil {
			return nil, err
		}
		ids = listed
	default:
		return nil, fmt.Errorf("export: specify --table or --namespace")
	}

	eng := scan.NewEngine(cat, st)
	if f.branch != "" {
		eng.Branch = f.branch
	}
	reports := make([]health.Report, 0, len(ids))
	for _, id := range ids {
		r, err := eng.Scan(ctx, id, health.Defaults())
		if err != nil {
			return nil, fmt.Errorf("scan %s: %w", id, err)
		}
		reports = append(reports, r.Report)
	}
	var buf strings.Builder
	if err := metrics.Render(&buf, reports...); err != nil {
		return nil, err
	}
	return []byte(buf.String()), nil
}
