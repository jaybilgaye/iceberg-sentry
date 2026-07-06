package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jaybilgaye/iceberg-sentry/internal/catalog"
	"github.com/jaybilgaye/iceberg-sentry/internal/cost"
	"github.com/jaybilgaye/iceberg-sentry/internal/exitcode"
	"github.com/jaybilgaye/iceberg-sentry/internal/iceberg"
)

type costFlags struct {
	auditFlags
	rate         float64
	provider     string
	coldTierDays int
}

func newCostCmd() *cobra.Command {
	f := &costFlags{}
	cmd := &cobra.Command{
		Use:   "cost",
		Short: "Snapshot cost timeline and tiered-storage recommendations",
		Long: `cost computes the cumulative-storage cost trajectory across a table's
snapshot history and flags snapshots older than the cold-tier cutoff. Cost
rates default to AWS S3 Standard list prices and are clearly labelled as
estimates.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCost(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), f)
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
	flags.Float64Var(&f.rate, "rate", 0, "override $/GB/month standard-class rate (0 = use provider default)")
	flags.StringVar(&f.provider, "provider", "s3-standard", "cost provider name (informational)")
	flags.IntVar(&f.coldTierDays, "cold-tier-days", 90, "snapshots older than this are flagged as tier candidates")
	flags.StringVar(&f.format, "format", "text", "output format: text|json")
	flags.BoolVar(&f.pathStyle, "s3-path-style", false, "use S3 path-style addressing")
	_ = cmd.MarkFlagRequired("table")
	return cmd
}

func runCost(ctx context.Context, stdout, stderr io.Writer, f *costFlags) error {
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

	var p cost.Provider
	if f.rate > 0 {
		p = cost.CustomRate(f.provider, f.rate)
	} else {
		p = cost.Default()
	}
	timeline := cost.Timeline(md, p, f.coldTierDays)

	switch strings.ToLower(f.format) {
	case "json":
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"table":          id.String(),
			"provider":       p.Name(),
			"rate_usd_gb_mo": p.Rate("standard"),
			"timeline":       timeline,
		})
	default:
		renderCostText(stdout, id, p, timeline)
	}
	return nil
}

func renderCostText(w io.Writer, id catalog.TableID, p cost.Provider, points []cost.SnapshotPoint) {
	fmt.Fprintf(w, "\n  Snapshot Cost Timeline: %s\n", id)
	fmt.Fprintln(w, "  ─────────────────────────────────────────────────────────────────")
	fmt.Fprintf(w, "  Provider: %s   Rate: $%.4f / GB-month   (list-price estimate)\n\n", p.Name(), p.Rate("standard"))
	if len(points) == 0 {
		fmt.Fprintln(w, "  No snapshot data.")
		return
	}
	fmt.Fprintf(w, "  %-20s %-12s %-12s %-12s %s\n", "snapshot", "ts", "GB", "$/mo", "tier?")
	for _, p := range points {
		marker := ""
		if p.TierCandidate {
			marker = "cold"
		}
		fmt.Fprintf(w, "  %-20d %-12s %-12.2f %-12.2f %s\n",
			p.SnapshotID, p.Timestamp.Format("2006-01-02"), p.CumulativeGB, p.MonthlyUSD, marker)
	}
	fmt.Fprintln(w, "")
}
