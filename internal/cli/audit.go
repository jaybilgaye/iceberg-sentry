package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jaybilgaye/iceberg-sentry/internal/catalog"
	"github.com/jaybilgaye/iceberg-sentry/internal/catalog/hive"
	"github.com/jaybilgaye/iceberg-sentry/internal/exitcode"
	"github.com/jaybilgaye/iceberg-sentry/internal/health"
	"github.com/jaybilgaye/iceberg-sentry/internal/output"
	"github.com/jaybilgaye/iceberg-sentry/internal/policy"
	"github.com/jaybilgaye/iceberg-sentry/internal/scan"
	"github.com/jaybilgaye/iceberg-sentry/internal/storage"
)

type auditFlags struct {
	catalogKind string
	catalogRoot string
	hiveAddr    string
	glueCatalog string
	hdfsURL     string
	tableID     string
	namespace   string
	policyPath  string
	format      string
	failOn      string
	pathStyle   bool
}

func newAuditCmd() *cobra.Command {
	f := &auditFlags{}
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Audit one or more Iceberg tables and report a health score",
		Long: `audit walks Iceberg metadata for the requested tables and emits a
weighted health report. With --table the output is a single-table report;
with --namespace every Iceberg table under the namespace is scored.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAudit(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), f)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&f.catalogKind, "catalog", "localfs", "catalog adapter: localfs|glue|hive")
	flags.StringVar(&f.catalogRoot, "catalog-root", "", "root directory for the localfs catalog")
	flags.StringVar(&f.hiveAddr, "hive", "", "host:port for the hive metastore (used when --catalog=hive)")
	flags.StringVar(&f.glueCatalog, "glue-catalog", "", "AWS account ID owning the Glue catalog (cross-account)")
	flags.StringVar(&f.hdfsURL, "hdfs", "", "WebHDFS root URL (e.g. https://namenode:14000/webhdfs/v1)")
	flags.StringVar(&f.tableID, "table", "", "fully qualified table identifier (namespace.table)")
	flags.StringVar(&f.namespace, "namespace", "", "namespace to audit (all Iceberg tables in it)")
	flags.StringVar(&f.policyPath, "policy", "", "path to sentry.yaml")
	flags.StringVar(&f.format, "format", "text", "output format: text|json")
	flags.StringVar(&f.failOn, "fail-on", "critical", "minimum severity that exits non-zero: warn|critical|never")
	flags.BoolVar(&f.pathStyle, "s3-path-style", false, "use S3 path-style addressing (MinIO/LocalStack)")
	return cmd
}

func runAudit(ctx context.Context, stdout, stderr io.Writer, f *auditFlags) error {
	fmtKind, err := output.ParseFormat(f.format)
	if err != nil {
		return &codedError{code: exitcode.ConfigError, err: err}
	}

	cat, err := buildCatalog(ctx, f)
	if err != nil {
		return &codedError{code: exitcode.ConnectionFail, err: err}
	}
	res, err := buildStorage(ctx, f)
	if err != nil {
		return &codedError{code: exitcode.ConfigError, err: err}
	}

	thresholds := health.Defaults()
	var policyFile *policy.File
	if f.policyPath != "" {
		pf, err := policy.Load(f.policyPath)
		if err != nil {
			return &codedError{code: exitcode.ConfigError, err: fmt.Errorf("load policy: %w", err)}
		}
		policyFile = pf
	}

	var ids []catalog.TableID
	switch {
	case f.tableID != "":
		id, err := catalog.ParseTableID(f.tableID)
		if err != nil {
			return &codedError{code: exitcode.ConfigError, err: err}
		}
		ids = []catalog.TableID{id}
	case f.namespace != "":
		listed, err := cat.ListTables(ctx, f.namespace)
		if err != nil {
			return &codedError{code: exitcode.ConnectionFail, err: err}
		}
		ids = listed
	default:
		return &codedError{code: exitcode.ConfigError, err: errors.New("specify --table or --namespace")}
	}

	if len(ids) == 0 {
		fmt.Fprintln(stderr, "no tables matched")
		return nil
	}

	engine := scan.NewEngine(cat, res)
	worst := exitcode.OK
	for _, id := range ids {
		t := thresholds
		if policyFile != nil {
			if p := policyFile.MatchTable(id.Namespace); p != nil {
				if applied, err := p.ApplyThresholds(t); err != nil {
					return &codedError{code: exitcode.ConfigError, err: err}
				} else {
					t = applied
				}
			}
		}
		result, err := engine.Scan(ctx, id, t)
		if err != nil {
			fmt.Fprintf(stderr, "scan %s: %v\n", id, err)
			worst = maxInt(worst, exitcode.ConnectionFail)
			continue
		}
		if err := output.Render(stdout, result.Report, fmtKind); err != nil {
			return err
		}
		worst = maxInt(worst, exitFromReport(result.Report, f.failOn))
	}
	if worst != exitcode.OK {
		return &codedError{code: worst}
	}
	return nil
}

func buildCatalog(ctx context.Context, f *auditFlags) (catalog.Catalog, error) {
	switch strings.ToLower(f.catalogKind) {
	case "localfs", "local":
		root := f.catalogRoot
		if root == "" {
			root = os.Getenv("SENTRY_CATALOG_ROOT")
		}
		if root == "" {
			return nil, errors.New("--catalog-root is required for the localfs catalog")
		}
		return catalog.NewLocalFS(root), nil
	case "glue":
		opts := []catalog.GlueOption{}
		if f.glueCatalog != "" {
			opts = append(opts, catalog.WithGlueCatalogID(f.glueCatalog))
		}
		return catalog.NewGlue(ctx, opts...)
	case "hive":
		host, port, err := parseHostPort(f.hiveAddr)
		if err != nil {
			return nil, fmt.Errorf("--hive: %w", err)
		}
		return hive.New(host, port), nil
	default:
		return nil, fmt.Errorf("unknown catalog %q", f.catalogKind)
	}
}

func buildStorage(ctx context.Context, f *auditFlags) (*storage.Resolver, error) {
	r := storage.NewResolver()
	r.Register("file", storage.NewLocalFS())
	if s3, err := storage.NewS3(ctx, storage.WithS3PathStyle(f.pathStyle)); err == nil {
		r.Register("s3", s3)
	}
	if f.hdfsURL != "" {
		r.Register("hdfs", storage.NewHDFS(f.hdfsURL))
	}
	return r, nil
}

func exitFromReport(r health.Report, failOn string) int {
	threshold := strings.ToLower(strings.TrimSpace(failOn))
	switch r.WorstSeverity {
	case health.SeverityCritical:
		if threshold == "never" {
			return exitcode.OK
		}
		return exitcode.Critical
	case health.SeverityWarning:
		if threshold == "warn" {
			return exitcode.Warning
		}
		return exitcode.OK
	}
	return exitcode.OK
}

func parseHostPort(s string) (string, int, error) {
	if s == "" {
		return "", 0, errors.New("empty host:port")
	}
	host, portStr, found := strings.Cut(s, ":")
	if !found {
		return "", 0, fmt.Errorf("expected host:port, got %q", s)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, fmt.Errorf("invalid port %q: %w", portStr, err)
	}
	return host, port, nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
