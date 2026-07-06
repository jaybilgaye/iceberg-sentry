package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/jaybilgaye/iceberg-sentry/internal/catalog"
	"github.com/jaybilgaye/iceberg-sentry/internal/exitcode"
	"github.com/jaybilgaye/iceberg-sentry/internal/health"
	"github.com/jaybilgaye/iceberg-sentry/internal/scan"
)

// baseline is the on-disk record persisted between bench start and bench compare.
type baseline struct {
	Table      string        `json:"table"`
	Tag        string        `json:"tag"`
	CapturedAt time.Time     `json:"captured_at"`
	Stats      health.Stats  `json:"stats"`
	Report     health.Report `json:"report"`
}

func newBenchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bench",
		Short: "Capture a baseline scan and compare against a fresh scan",
		Long: `bench captures a labelled snapshot of a table's health stats, then later
re-scans and prints a side-by-side diff. Use it to quantify the effect of a
compaction or expire_snapshots run:

	iceberg-sentry bench start   --table ns.t --tag pre-compact ...
	# ...run maintenance...
	iceberg-sentry bench compare --table ns.t --tag pre-compact ...`,
	}
	cmd.AddCommand(newBenchStartCmd(), newBenchCompareCmd())
	return cmd
}

type benchFlags struct {
	auditFlags
	tag string
	dir string
}

func benchBaseFlags(f *benchFlags, cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.StringVar(&f.catalogKind, "catalog", "localfs", "catalog adapter: localfs|glue|hive|rest")
	flags.StringVar(&f.catalogRoot, "catalog-root", "", "root directory for the localfs catalog")
	flags.StringVar(&f.hiveAddr, "hive", "", "host:port for the hive metastore")
	flags.StringVar(&f.restURL, "rest", "", "Iceberg REST catalog base URL")
	flags.StringVar(&f.glueCatalog, "glue-catalog", "", "AWS account ID owning the Glue catalog")
	flags.StringVar(&f.hdfsURL, "hdfs", "", "WebHDFS root URL")
	flags.StringVar(&f.tableID, "table", "", "fully qualified table identifier")
	flags.StringVar(&f.tag, "tag", "default", "label for this baseline")
	flags.StringVar(&f.dir, "bench-dir", ".sentry/bench", "directory to read/write baselines")
	flags.BoolVar(&f.pathStyle, "s3-path-style", false, "use S3 path-style addressing (MinIO/LocalStack)")
	_ = cmd.MarkFlagRequired("table")
}

func newBenchStartCmd() *cobra.Command {
	f := &benchFlags{}
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Capture a baseline scan",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runBenchStart(cmd.Context(), cmd.OutOrStdout(), f)
		},
	}
	benchBaseFlags(f, cmd)
	return cmd
}

func newBenchCompareCmd() *cobra.Command {
	f := &benchFlags{}
	cmd := &cobra.Command{
		Use:   "compare",
		Short: "Compare current scan against a stored baseline",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runBenchCompare(cmd.Context(), cmd.OutOrStdout(), f)
		},
	}
	benchBaseFlags(f, cmd)
	return cmd
}

func runBenchStart(ctx context.Context, stdout io.Writer, f *benchFlags) error {
	res, err := runBenchScan(ctx, &f.auditFlags)
	if err != nil {
		return err
	}
	b := baseline{
		Table:      res.Entry.ID.String(),
		Tag:        f.tag,
		CapturedAt: time.Now(),
		Stats:      res.Stats,
		Report:     res.Report,
	}
	if err := writeBaseline(f.dir, b); err != nil {
		return &codedError{code: exitcode.ConfigError, err: err}
	}
	fmt.Fprintf(stdout, "  Baseline captured: %s (tag=%s, score=%d/%d)\n",
		b.Table, b.Tag, b.Report.Score, b.Report.MaxScore)
	return nil
}

func runBenchCompare(ctx context.Context, stdout io.Writer, f *benchFlags) error {
	prev, err := readBaseline(f.dir, f.tag, f.tableID)
	if err != nil {
		return &codedError{code: exitcode.ConfigError, err: err}
	}
	res, err := runBenchScan(ctx, &f.auditFlags)
	if err != nil {
		return err
	}
	return renderBenchDiff(stdout, prev, baseline{
		Table:      res.Entry.ID.String(),
		Tag:        "current",
		CapturedAt: time.Now(),
		Stats:      res.Stats,
		Report:     res.Report,
	})
}

func runBenchScan(ctx context.Context, f *auditFlags) (*scan.Result, error) {
	cat, err := buildCatalogFromAudit(ctx, f)
	if err != nil {
		return nil, &codedError{code: exitcode.ConnectionFail, err: err}
	}
	st, err := buildStorage(ctx, f)
	if err != nil {
		return nil, &codedError{code: exitcode.ConfigError, err: err}
	}
	id, err := catalog.ParseTableID(f.tableID)
	if err != nil {
		return nil, &codedError{code: exitcode.ConfigError, err: err}
	}
	eng := scan.NewEngine(cat, st)
	return eng.Scan(ctx, id, health.Defaults())
}

func writeBaseline(dir string, b baseline) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, sanitizeFilename(b.Table+"_"+b.Tag)+".json")
	body, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o644)
}

func readBaseline(dir, tag, table string) (baseline, error) {
	path := filepath.Join(dir, sanitizeFilename(table+"_"+tag)+".json")
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return baseline{}, fmt.Errorf("no baseline at %s — run `bench start` first", path)
		}
		return baseline{}, err
	}
	var b baseline
	if err := json.Unmarshal(body, &b); err != nil {
		return baseline{}, err
	}
	return b, nil
}

func renderBenchDiff(w io.Writer, prev, cur baseline) error {
	fmt.Fprintf(w, "\n  Benchmark: %s  │  %s → current\n", prev.Table, prev.Tag)
	fmt.Fprintln(w, "  ────────────────────────────────────────────────────────────────────")
	fmt.Fprintf(w, "  %-20s %12d  →  %-12d  %s\n", "Health Score:", prev.Report.Score, cur.Report.Score, deltaInt(prev.Report.Score, cur.Report.Score))
	fmt.Fprintf(w, "  %-20s %12d  →  %-12d  %s\n", "Data Files:", prev.Stats.DataFileCount, cur.Stats.DataFileCount, deltaInt64(prev.Stats.DataFileCount, cur.Stats.DataFileCount))
	fmt.Fprintf(w, "  %-20s %12d  →  %-12d  %s\n", "Manifest Files:", prev.Stats.ManifestFileCount, cur.Stats.ManifestFileCount, deltaInt64(prev.Stats.ManifestFileCount, cur.Stats.ManifestFileCount))
	fmt.Fprintf(w, "  %-20s %12d  →  %-12d  %s\n", "Position Deletes:", prev.Stats.PositionDeleteFiles, cur.Stats.PositionDeleteFiles, deltaInt64(prev.Stats.PositionDeleteFiles, cur.Stats.PositionDeleteFiles))
	fmt.Fprintf(w, "  %-20s %12d  →  %-12d  %s\n", "Equality Deletes:", prev.Stats.EqualityDeleteFiles, cur.Stats.EqualityDeleteFiles, deltaInt64(prev.Stats.EqualityDeleteFiles, cur.Stats.EqualityDeleteFiles))
	avgPrev := avgBytes(prev.Stats.DataFileTotalBytes, prev.Stats.DataFileCount)
	avgCur := avgBytes(cur.Stats.DataFileTotalBytes, cur.Stats.DataFileCount)
	fmt.Fprintf(w, "  %-20s %12s  →  %-12s\n", "Avg File Size:", humanBytes(avgPrev), humanBytes(avgCur))
	fmt.Fprintln(w, "")
	return nil
}

func avgBytes(total, n int64) int64 {
	if n == 0 {
		return 0
	}
	return total / n
}

func deltaInt(a, b int) string {
	d := b - a
	if d == 0 {
		return "(no change)"
	}
	if d > 0 {
		return fmt.Sprintf("(+%d)", d)
	}
	return fmt.Sprintf("(%d)", d)
}

func deltaInt64(a, b int64) string {
	d := b - a
	if d == 0 {
		return "(no change)"
	}
	if d > 0 {
		return fmt.Sprintf("(+%d)", d)
	}
	return fmt.Sprintf("(%d)", d)
}

func sanitizeFilename(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			out = append(out, c)
		case c == '_' || c == '-' || c == '.':
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}
