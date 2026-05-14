package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jaybilgaye/iceberg-sentry/internal/catalog"
	"github.com/jaybilgaye/iceberg-sentry/internal/exitcode"
	"github.com/jaybilgaye/iceberg-sentry/internal/iceberg"
	"github.com/jaybilgaye/iceberg-sentry/internal/migration"
)

type migrationFlags struct {
	auditFlags
}

func newMigrationCmd() *cobra.Command {
	f := &migrationFlags{}
	cmd := &cobra.Command{
		Use:   "migration",
		Short: "On-prem HDFS → CDP Public Cloud Migration Readiness Audit",
		Long: `migration scans an Iceberg table for patterns that will break or behave
poorly after a migration from HDFS to S3/ADLS — absolute hdfs:// paths,
HDFS-specific table properties, v1 format-version tables, and dark-storage
risk. Output is a per-table risk score (LOW/MEDIUM/HIGH) plus remediation.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMigration(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), f)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&f.catalogKind, "catalog", "localfs", "catalog adapter: localfs|glue|hive|rest")
	flags.StringVar(&f.catalogRoot, "catalog-root", "", "root directory for the localfs catalog")
	flags.StringVar(&f.hiveAddr, "hive", "", "host:port for the hive metastore")
	flags.StringVar(&f.restURL, "rest", "", "Iceberg REST catalog base URL")
	flags.StringVar(&f.glueCatalog, "glue-catalog", "", "AWS account ID owning the Glue catalog")
	flags.StringVar(&f.hdfsURL, "hdfs", "", "WebHDFS root URL")
	flags.StringVar(&f.tableID, "table", "", "fully qualified table identifier (mutex with --namespace)")
	flags.StringVar(&f.namespace, "namespace", "", "namespace to audit (all Iceberg tables in it)")
	flags.StringVar(&f.format, "format", "text", "output format: text|json")
	flags.BoolVar(&f.pathStyle, "s3-path-style", false, "use S3 path-style addressing (MinIO/LocalStack)")
	return cmd
}

func runMigration(ctx context.Context, stdout, stderr io.Writer, f *migrationFlags) error {
	cat, err := buildCatalogFromAudit(ctx, &f.auditFlags)
	if err != nil {
		return &codedError{code: exitcode.ConnectionFail, err: err}
	}
	st, err := buildStorage(ctx, &f.auditFlags)
	if err != nil {
		return &codedError{code: exitcode.ConfigError, err: err}
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

	reports := make([]*migration.Report, 0, len(ids))
	worst := exitcode.OK
	for _, id := range ids {
		entry, err := cat.LoadTable(ctx, id)
		if err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", id, err)
			worst = maxInt(worst, exitcode.ConnectionFail)
			continue
		}
		rc, err := st.Open(ctx, entry.MetadataLocation)
		if err != nil {
			fmt.Fprintf(stderr, "%s: open metadata: %v\n", id, err)
			worst = maxInt(worst, exitcode.ConnectionFail)
			continue
		}
		md, err := iceberg.ReadTableMetadata(ctx, rc)
		_ = rc.Close()
		if err != nil {
			fmt.Fprintf(stderr, "%s: parse metadata: %v\n", id, err)
			worst = maxInt(worst, exitcode.ConnectionFail)
			continue
		}
		// pyiceberg writes location with no scheme — treat property HDFS hints
		// as part of the surrounding metadata.
		if md.Properties == nil {
			md.Properties = entry.Properties
		}
		r, err := migration.Audit(ctx, id.String(), md, entry.MetadataLocation, st)
		if err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", id, err)
			worst = maxInt(worst, exitcode.ConnectionFail)
			continue
		}
		r.Catalog = cat.Name()
		reports = append(reports, r)
		if r.Risk == migration.RiskHigh {
			worst = maxInt(worst, exitcode.Critical)
		} else if r.Risk == migration.RiskMedium && worst < exitcode.Warning {
			worst = exitcode.Warning
		}
	}

	switch strings.ToLower(f.format) {
	case "json":
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(reports); err != nil {
			return err
		}
	default:
		renderMigrationText(stdout, reports)
	}

	if worst != exitcode.OK {
		return &codedError{code: worst}
	}
	return nil
}

func renderMigrationText(w io.Writer, reports []*migration.Report) {
	for _, r := range reports {
		fmt.Fprintf(w, "\n  Migration Readiness: %s  │  Risk: %s\n", r.Table, r.Risk)
		fmt.Fprintln(w, "  ─────────────────────────────────────────────────────────────────")
		fmt.Fprintf(w, "  Format version:   v%d\n", r.FormatVersion)
		fmt.Fprintf(w, "  Manifest files:   %d\n", r.ManifestCount)
		fmt.Fprintf(w, "  Data files:       %d\n", r.DataFileCount)
		fmt.Fprintf(w, "  Total data:       %s\n", humanBytes(r.TotalDataBytes))
		if len(r.Findings) == 0 {
			fmt.Fprintln(w, "  ✓ No migration blockers detected.")
			continue
		}
		fmt.Fprintln(w, "  Findings:")
		for _, f := range r.Findings {
			fmt.Fprintf(w, "    [%s] %s: %s\n", f.Severity, f.Code, f.Message)
			if f.Remediation != "" {
				fmt.Fprintf(w, "          → %s\n", f.Remediation)
			}
		}
	}
	fmt.Fprintln(w, "")
}
