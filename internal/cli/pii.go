package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jaybilgaye/iceberg-sentry/internal/catalog"
	"github.com/jaybilgaye/iceberg-sentry/internal/exitcode"
	"github.com/jaybilgaye/iceberg-sentry/internal/iceberg"
	"github.com/jaybilgaye/iceberg-sentry/internal/pii"
	"github.com/jaybilgaye/iceberg-sentry/internal/storage"
)

type piiFlags struct {
	auditFlags
	rowGroupFrac  float64
	rowsPerGroup  int
	confidence    float64
	maxFiles      int
	atlasJSONPath string
}

func newPIICmd() *cobra.Command {
	f := &piiFlags{}
	cmd := &cobra.Command{
		Use:   "pii",
		Short: "Sample Parquet row groups for PII and emit catalog-import findings",
		Long: `pii reads the current snapshot's manifest, picks data files,
samples a configurable fraction of row groups, and runs regex + entropy
detection against string-typed columns.

No PII values are ever written to disk or to logs (spec §7.3); only column
names and detected PII types are surfaced.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPII(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), f)
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
	flags.StringVar(&f.format, "format", "text", "output format: text|json|atlas")
	flags.Float64Var(&f.rowGroupFrac, "row-group-fraction", 0.05, "fraction of row groups to sample (0..1)")
	flags.IntVar(&f.rowsPerGroup, "rows-per-group", 1024, "max rows scanned per sampled row group")
	flags.Float64Var(&f.confidence, "min-confidence", 0.5, "minimum hit rate to surface a finding (0..1)")
	flags.IntVar(&f.maxFiles, "max-files", 16, "maximum number of Parquet files to sample")
	flags.StringVar(&f.atlasJSONPath, "atlas-output", "", "write Atlas/UC bulk-import payload to this path (use - for stdout)")
	flags.BoolVar(&f.pathStyle, "s3-path-style", false, "use S3 path-style addressing (MinIO/LocalStack)")
	_ = cmd.MarkFlagRequired("table")
	return cmd
}

func runPII(ctx context.Context, stdout, stderr io.Writer, f *piiFlags) error {
	cat, err := buildCatalogFromAudit(ctx, &f.auditFlags)
	if err != nil {
		return &codedError{code: exitcode.ConnectionFail, err: err}
	}
	st, err := buildStorage(ctx, &f.auditFlags)
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

	files, err := collectDataFileURIs(ctx, md, entry.MetadataLocation, st, f.maxFiles)
	if err != nil {
		return &codedError{code: exitcode.ConnectionFail, err: err}
	}
	if len(files) == 0 {
		fmt.Fprintln(stderr, "no data files to sample")
		return nil
	}

	agg := pii.NewAggregator()
	opts := pii.SampleDefaults()
	opts.RowGroupFraction = f.rowGroupFrac
	opts.RowsPerGroup = f.rowsPerGroup
	opts.Rand = rand.New(rand.NewSource(1))

	for _, uri := range files {
		body, err := pii.OpenForSampling(ctx, st, uri)
		if err != nil {
			fmt.Fprintf(stderr, "warn: skip %s: %v\n", uri, err)
			continue
		}
		if err := pii.SampleParquet(ctx, body, id.String(), agg, opts); err != nil {
			fmt.Fprintf(stderr, "warn: parquet %s: %v\n", uri, err)
		}
		_ = body.Close()
	}

	findings := agg.Findings(id.String(), f.confidence)

	if f.atlasJSONPath != "" {
		if err := writeAtlasPayload(f.atlasJSONPath, findings); err != nil {
			return &codedError{code: exitcode.ConfigError, err: err}
		}
	}

	switch strings.ToLower(f.format) {
	case "json":
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(findings); err != nil {
			return err
		}
	default:
		renderPIIText(stdout, id, len(files), findings)
	}

	if len(findings) > 0 {
		return &codedError{code: exitcode.UntaggedPII}
	}
	return nil
}

func collectDataFileURIs(
	ctx context.Context,
	md *iceberg.TableMetadata,
	metadataURI string,
	st *storage.Resolver,
	limit int,
) ([]string, error) {
	cur := md.CurrentSnapshot()
	if cur == nil {
		return nil, nil
	}
	manifests, err := loadManifestsForPII(ctx, st, cur, metadataURI)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, limit)
	for _, m := range manifests {
		if len(out) >= limit {
			break
		}
		uri := resolveSiblingForPII(metadataURI, m.Path)
		rc, err := st.Open(ctx, uri)
		if err != nil {
			return nil, err
		}
		err = iceberg.ReadManifestFile(ctx, rc, func(df iceberg.DataFile) error {
			if df.Content != iceberg.FileContentData {
				return nil
			}
			if df.Status == 2 {
				return nil
			}
			out = append(out, resolveSiblingForPII(metadataURI, df.Path))
			if len(out) >= limit {
				return io.EOF
			}
			return nil
		})
		_ = rc.Close()
		if err != nil && err != io.EOF {
			return nil, err
		}
	}
	return out, nil
}

func loadManifestsForPII(
	ctx context.Context,
	st *storage.Resolver,
	snap *iceberg.Snapshot,
	metadataURI string,
) ([]iceberg.ManifestFile, error) {
	if snap.ManifestList != "" {
		uri := resolveSiblingForPII(metadataURI, snap.ManifestList)
		rc, err := st.Open(ctx, uri)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rc.Close() }()
		return iceberg.ReadManifestList(ctx, rc)
	}
	out := make([]iceberg.ManifestFile, 0, len(snap.Manifests))
	for _, p := range snap.Manifests {
		out = append(out, iceberg.ManifestFile{Path: p, Content: iceberg.ManifestContentData})
	}
	return out, nil
}

func resolveSiblingForPII(base, ref string) string {
	if ref == "" {
		return ""
	}
	if strings.Contains(ref, "://") || strings.HasPrefix(ref, "/") {
		return ref
	}
	dir := base
	if i := strings.LastIndex(base, "/"); i >= 0 {
		dir = base[:i]
	}
	return dir + "/" + ref
}

func renderPIIText(w io.Writer, id catalog.TableID, sampledFiles int, findings []pii.Finding) {
	fmt.Fprintf(w, "\n  PII Scan Report: %s\n", id)
	fmt.Fprintln(w, "  ─────────────────────────────────────────────────────────────────")
	fmt.Fprintf(w, "  Files sampled: %d\n", sampledFiles)
	fmt.Fprintf(w, "  Findings:      %d (review required — no auto-tagging)\n\n", len(findings))
	for _, f := range findings {
		fmt.Fprintf(w, "    [%s] column=%s  confidence=%.2f  samples=%d/%d  → %s\n",
			f.PIIType, f.Column, f.Confidence, f.MatchCount, f.SampleCount, f.RecommendTag)
	}
	fmt.Fprintln(w, "")
}

// writeAtlasPayload emits a JSON document shaped for Apache Atlas bulk
// import (POST /api/atlas/v2/entity/bulk-style envelope).
func writeAtlasPayload(path string, findings []pii.Finding) error {
	payload := map[string]any{
		"review_required": true,
		"findings":        findings,
	}
	body, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	if path == "-" {
		fmt.Println(string(body))
		return nil
	}
	return writeFile(path, body)
}
