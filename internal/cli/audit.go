package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jaybilgaye/iceberg-sentry/internal/catalog"
	"github.com/jaybilgaye/iceberg-sentry/internal/catalog/hive"
	"github.com/jaybilgaye/iceberg-sentry/internal/catalog/rest"
	"github.com/jaybilgaye/iceberg-sentry/internal/exitcode"
	"github.com/jaybilgaye/iceberg-sentry/internal/health"
	"github.com/jaybilgaye/iceberg-sentry/internal/metrics"
	"github.com/jaybilgaye/iceberg-sentry/internal/output"
	"github.com/jaybilgaye/iceberg-sentry/internal/policy"
	"github.com/jaybilgaye/iceberg-sentry/internal/scan"
	"github.com/jaybilgaye/iceberg-sentry/internal/storage"
)

type auditFlags struct {
	catalogKind      string
	catalogRoot      string
	hiveAddr         string
	hivePrincipal    string
	hiveKeytab       string
	restURL          string
	restToken        string
	restClientID     string
	restClientSecret string
	restTokenURL     string
	glueCatalog      string
	hdfsURL          string
	tableID          string
	namespace        string
	branch           string
	policyPath       string
	format           string
	failOn           string
	pushGateway      string
	pushJob          string
	pushInstance     string
	atlasOutput      string
	pathStyle        bool
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
	flags.StringVar(&f.catalogKind, "catalog", "localfs", "catalog adapter: localfs|glue|hive|rest")
	flags.StringVar(&f.catalogRoot, "catalog-root", "", "root directory for the localfs catalog")
	flags.StringVar(&f.hiveAddr, "hive", "", "host:port for the hive metastore (used when --catalog=hive)")
	flags.StringVar(&f.restURL, "rest", "", "Iceberg REST catalog base URL (used when --catalog=rest)")
	flags.StringVar(&f.glueCatalog, "glue-catalog", "", "AWS account ID owning the Glue catalog (cross-account)")
	flags.StringVar(&f.hdfsURL, "hdfs", "", "WebHDFS root URL (e.g. https://namenode:14000/webhdfs/v1)")
	flags.StringVar(&f.tableID, "table", "", "fully qualified table identifier (namespace.table)")
	flags.StringVar(&f.namespace, "namespace", "", "namespace to audit (all Iceberg tables in it)")
	flags.StringVar(&f.branch, "branch", "", "Iceberg branch to scan (default: main)")
	flags.StringVar(&f.policyPath, "policy", "", "path to sentry.yaml")
	flags.StringVar(&f.format, "format", "text", "output format: text|json|sarif")
	flags.StringVar(&f.failOn, "fail-on", "critical", "minimum severity that exits non-zero: warn|critical|never")
	flags.StringVar(&f.pushGateway, "push-gateway", "", "Prometheus push-gateway URL (used with --format prometheus)")
	flags.StringVar(&f.pushJob, "push-job", "iceberg-sentry", "job label for push-gateway")
	flags.StringVar(&f.pushInstance, "push-instance", "", "instance label for push-gateway (optional)")
	flags.StringVar(&f.atlasOutput, "atlas-output", "", "write Atlas/UC bulk-import payload of health findings to this path (use - for stdout)")
	flags.StringVar(&f.restToken, "rest-token", "", "bearer token for the REST catalog (defaults to $SENTRY_REST_TOKEN)")
	flags.StringVar(&f.restClientID, "rest-oauth-client-id", "", "OAuth2 client_id for REST catalog token exchange")
	flags.StringVar(&f.restClientSecret, "rest-oauth-client-secret", "", "OAuth2 client_secret for REST catalog token exchange")
	flags.StringVar(&f.restTokenURL, "rest-oauth-token-url", "", "OAuth2 token endpoint URL (Polaris/Tabular/Unity)")
	flags.StringVar(&f.hivePrincipal, "hive-principal", "", "Kerberos service principal (e.g. hive/hms.example.com@EXAMPLE.COM)")
	flags.StringVar(&f.hiveKeytab, "hive-keytab", "", "path to Kerberos keytab")
	flags.BoolVar(&f.pathStyle, "s3-path-style", false, "use S3 path-style addressing (MinIO/LocalStack)")
	return cmd
}

func runAudit(ctx context.Context, stdout, stderr io.Writer, f *auditFlags) error {
	fmtKind, err := output.ParseFormat(f.format)
	if err != nil {
		return &codedError{code: exitcode.ConfigError, err: err}
	}

	cat, err := buildCatalogFromAudit(ctx, f)
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
	if f.branch != "" {
		engine.Branch = f.branch
	}
	worst := exitcode.OK
	reports := make([]health.Report, 0, len(ids))
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
		reports = append(reports, result.Report)
		worst = maxInt(worst, exitFromReport(result.Report, f.failOn))
	}

	if fmtKind == output.FormatPrometheus {
		var buf bytes.Buffer
		if err := metrics.Render(&buf, reports...); err != nil {
			return err
		}
		if f.pushGateway != "" {
			if err := metrics.Push(ctx, f.pushGateway, f.pushJob, f.pushInstance, buf.Bytes()); err != nil {
				return &codedError{code: exitcode.ConnectionFail, err: err}
			}
		} else {
			if _, err := stdout.Write(buf.Bytes()); err != nil {
				return err
			}
		}
	} else {
		for _, r := range reports {
			if err := output.Render(stdout, r, fmtKind); err != nil {
				return err
			}
		}
	}
	if f.atlasOutput != "" {
		if err := writeAtlasHealth(f.atlasOutput, reports); err != nil {
			return &codedError{code: exitcode.ConfigError, err: err}
		}
	}
	if worst != exitcode.OK {
		return &codedError{code: worst}
	}
	return nil
}

// writeAtlasHealth emits an Atlas/UC bulk-import-shaped payload describing
// the per-table health summary. Companion to the pii Atlas payload — both
// follow the same envelope (review_required + findings array).
func writeAtlasHealth(path string, reports []health.Report) error {
	type finding struct {
		Table         string   `json:"table"`
		Score         int      `json:"health_score"`
		MaxScore      int      `json:"max_score"`
		WorstSeverity string   `json:"worst_severity"`
		WritePattern  string   `json:"write_pattern,omitempty"`
		FailedDims    []string `json:"failed_dimensions,omitempty"`
		RecommendTag  string   `json:"recommended_tag"`
	}
	out := struct {
		ReviewRequired bool      `json:"review_required"`
		Findings       []finding `json:"findings"`
	}{ReviewRequired: true}
	for _, r := range reports {
		f := finding{
			Table:         r.TableID,
			Score:         r.Score,
			MaxScore:      r.MaxScore,
			WorstSeverity: string(r.WorstSeverity),
			WritePattern:  r.WritePattern,
			RecommendTag:  "HEALTH_" + string(r.WorstSeverity),
		}
		for _, d := range r.Dimensions {
			if d.Severity == health.SeverityCritical || d.Severity == health.SeverityWarning {
				f.FailedDims = append(f.FailedDims, d.Name)
			}
		}
		out.Findings = append(out.Findings, f)
	}
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	if path == "-" {
		fmt.Println(string(body))
		return nil
	}
	return writeFile(path, body)
}

func buildCatalogFromAudit(ctx context.Context, f *auditFlags) (catalog.Catalog, error) {
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
		opts := []hive.Option{}
		if f.hivePrincipal != "" {
			if f.hiveKeytab == "" {
				return nil, errors.New("--hive-keytab is required when --hive-principal is set")
			}
			tr, err := hive.NewKerberosTransport(host, port, f.hivePrincipal, f.hiveKeytab)
			if err != nil {
				return nil, fmt.Errorf("kerberos: %w", err)
			}
			opts = append(opts, hive.WithTransport(tr))
		}
		return hive.New(host, port, opts...), nil
	case "rest", "polaris", "unity":
		if f.restURL == "" {
			return nil, errors.New("--rest is required for the rest catalog")
		}
		restOpts := []rest.Option{}
		token := f.restToken
		if token == "" {
			token = os.Getenv("SENTRY_REST_TOKEN")
		}
		if token != "" {
			restOpts = append(restOpts, rest.WithBearerToken(token))
		}
		if f.restClientID != "" && f.restClientSecret != "" && f.restTokenURL != "" {
			restOpts = append(restOpts, rest.WithOAuth2ClientCredentials(
				f.restTokenURL, f.restClientID, f.restClientSecret,
			))
		}
		return rest.New(f.restURL, restOpts...), nil
	default:
		return nil, fmt.Errorf("unknown catalog %q", f.catalogKind)
	}
}

func buildStorage(ctx context.Context, f *auditFlags) (*storage.Resolver, error) {
	r := storage.NewResolver()
	r.Register("file", storage.NewLocalFS())

	// Wrap S3 construction in a short timeout so aws-sdk-go-v2's IMDS
	// probe can't hang the whole process when the runtime environment
	// (containers, kind, restricted-network CI) has no metadata endpoint.
	// Users running against real S3 with valid config resolve well
	// inside 3 seconds; unreachable IMDS hits our timeout and skips S3.
	s3Ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if s3, err := storage.NewS3(s3Ctx, storage.WithS3PathStyle(f.pathStyle)); err == nil {
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
