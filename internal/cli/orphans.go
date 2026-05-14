package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jaybilgaye/iceberg-sentry/internal/catalog"
	"github.com/jaybilgaye/iceberg-sentry/internal/exitcode"
	"github.com/jaybilgaye/iceberg-sentry/internal/iceberg"
	"github.com/jaybilgaye/iceberg-sentry/internal/orphans"
)

type orphansFlags struct {
	catalogKind string
	catalogRoot string
	hiveAddr    string
	restURL     string
	glueCatalog string
	hdfsURL     string
	tableID     string
	dataPrefix  string
	grace       string
	format      string
	previewN    int
	pathStyle   bool
	confirm     bool
}

func newOrphansCmd() *cobra.Command {
	f := &orphansFlags{}
	cmd := &cobra.Command{
		Use:   "orphans",
		Short: "Report files in storage that no valid Iceberg snapshot references",
		Long: `orphans compares Iceberg metadata against storage to find files that no
snapshot points at. The output is a recommendation only — files are never
deleted by iceberg-sentry. Dry-run is always on; the --confirm flag is
reserved for a future delete-execution mode.

A grace period (default 24h) excludes files written during concurrent jobs.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runOrphans(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), f)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&f.catalogKind, "catalog", "localfs", "catalog adapter: localfs|glue|hive|rest")
	flags.StringVar(&f.catalogRoot, "catalog-root", "", "root directory for the localfs catalog")
	flags.StringVar(&f.hiveAddr, "hive", "", "host:port for the hive metastore")
	flags.StringVar(&f.restURL, "rest", "", "Iceberg REST catalog base URL")
	flags.StringVar(&f.glueCatalog, "glue-catalog", "", "AWS account ID owning the Glue catalog")
	flags.StringVar(&f.hdfsURL, "hdfs", "", "WebHDFS root URL")
	flags.StringVar(&f.tableID, "table", "", "fully qualified table identifier (namespace.table)")
	flags.StringVar(&f.dataPrefix, "data-prefix", "", "storage prefix to crawl (defaults to <table>/data)")
	flags.StringVar(&f.grace, "grace-period", "24h", "files newer than this are not flagged (e.g. 6h, 30m)")
	flags.StringVar(&f.format, "format", "text", "output format: text|json")
	flags.IntVar(&f.previewN, "preview", 25, "max orphans to list inline; 0 means all")
	flags.BoolVar(&f.pathStyle, "s3-path-style", false, "use S3 path-style addressing (MinIO/LocalStack)")
	flags.BoolVar(&f.confirm, "confirm", false, "reserved for future destructive mode; currently a no-op (always dry-run)")
	_ = cmd.MarkFlagRequired("table")
	return cmd
}

func runOrphans(ctx context.Context, stdout, stderr io.Writer, f *orphansFlags) error {
	if f.confirm {
		fmt.Fprintln(stderr, "warning: --confirm is reserved; orphans is always dry-run in this release")
	}
	grace, err := time.ParseDuration(f.grace)
	if err != nil {
		return &codedError{code: exitcode.ConfigError, err: fmt.Errorf("invalid --grace-period: %w", err)}
	}

	cat, err := buildCatalogFromAudit(ctx, &auditFlags{
		catalogKind: f.catalogKind,
		catalogRoot: f.catalogRoot,
		hiveAddr:    f.hiveAddr,
		restURL:     f.restURL,
		glueCatalog: f.glueCatalog,
	})
	if err != nil {
		return &codedError{code: exitcode.ConnectionFail, err: err}
	}
	st, err := buildStorage(ctx, &auditFlags{
		hdfsURL:   f.hdfsURL,
		pathStyle: f.pathStyle,
	})
	if err != nil {
		return &codedError{code: exitcode.ConfigError, err: err}
	}

	id, err := catalog.ParseTableID(f.tableID)
	if err != nil {
		return &codedError{code: exitcode.ConfigError, err: err}
	}
	entry, err := cat.LoadTable(ctx, id)
	if err != nil {
		return &codedError{code: exitcode.ConnectionFail, err: err}
	}

	rc, err := st.Open(ctx, entry.MetadataLocation)
	if err != nil {
		return &codedError{code: exitcode.ConnectionFail, err: err}
	}
	md, err := iceberg.ReadTableMetadata(ctx, rc)
	_ = rc.Close()
	if err != nil {
		return &codedError{code: exitcode.ConnectionFail, err: err}
	}

	dataPrefix := f.dataPrefix
	if dataPrefix == "" {
		if md.Location == "" {
			return &codedError{code: exitcode.ConfigError, err: errors.New("--data-prefix is required when metadata has no location")}
		}
		dataPrefix = strings.TrimRight(md.Location, "/") + "/data"
	}

	rep, err := orphans.Scan(ctx, md, entry.MetadataLocation, dataPrefix, st, orphans.Options{
		GracePeriod:    grace,
		SamplePreviewN: f.previewN,
	})
	if err != nil {
		return &codedError{code: exitcode.ConnectionFail, err: err}
	}

	switch strings.ToLower(f.format) {
	case "json":
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	default:
		return renderOrphansText(stdout, id, dataPrefix, rep)
	}
}

func renderOrphansText(w io.Writer, id catalog.TableID, dataPrefix string, r *orphans.Report) error {
	if _, err := fmt.Fprintf(w, "\n  [DRY RUN] Orphan File Report: %s\n", id); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "  ─────────────────────────────────────────────────────────────────"); err != nil {
		return err
	}
	fmt.Fprintf(w, "  Scanned prefix:  %s\n", dataPrefix)
	fmt.Fprintf(w, "  Snapshot:        %d (locked)\n", r.SnapshotID)
	fmt.Fprintf(w, "  Grace period:    %s\n", r.GracePeriod)
	fmt.Fprintf(w, "  Active files:    %d\n", r.ActiveFiles)
	fmt.Fprintf(w, "  Scanned objects: %d\n\n", r.ScannedObjects)

	fmt.Fprintf(w, "  Found %d orphan candidate(s)  │  Reclaimable: %s\n",
		len(r.Candidates), humanBytes(r.TotalBytes))

	if len(r.Candidates) > 0 {
		fmt.Fprintln(w, "\n  Preview:")
		for _, c := range r.Candidates {
			fmt.Fprintf(w, "    %s  (%s)\n", c.URI, humanBytes(c.SizeBytes))
		}
	}
	fmt.Fprintln(w, "")
	return nil
}

func humanBytes(b int64) string {
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
		tb = gb * 1024
	)
	switch {
	case b >= tb:
		return fmt.Sprintf("%.2f TB", float64(b)/float64(tb))
	case b >= gb:
		return fmt.Sprintf("%.2f GB", float64(b)/float64(gb))
	case b >= mb:
		return fmt.Sprintf("%.2f MB", float64(b)/float64(mb))
	case b >= kb:
		return fmt.Sprintf("%.2f KB", float64(b)/float64(kb))
	}
	return fmt.Sprintf("%d B", b)
}
