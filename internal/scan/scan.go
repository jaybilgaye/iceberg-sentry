// Package scan orchestrates a full table audit: catalog lookup, metadata
// JSON parse, manifest-list traversal, and per-manifest data-file walk. The
// output is a health.Stats record that the health package converts into a
// scored Report.
package scan

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/jaybilgaye/iceberg-sentry/internal/catalog"
	"github.com/jaybilgaye/iceberg-sentry/internal/health"
	"github.com/jaybilgaye/iceberg-sentry/internal/iceberg"
	"github.com/jaybilgaye/iceberg-sentry/internal/storage"
	"github.com/jaybilgaye/iceberg-sentry/internal/writepattern"
)

// Engine performs scans against a single (catalog, storage) pair.
type Engine struct {
	Catalog catalog.Catalog
	Storage *storage.Resolver
	// Now is injected so tests can pin "current time" for snapshot age math.
	Now func() time.Time
	// Branch optionally scans an Iceberg branch other than the table's
	// current-snapshot pointer. Empty means "scan current-snapshot-id".
	Branch string
}

// NewEngine constructs a scan engine.
func NewEngine(c catalog.Catalog, s *storage.Resolver) *Engine {
	return &Engine{Catalog: c, Storage: s, Now: time.Now}
}

// Result bundles the scored health report with the raw stats.
type Result struct {
	Report health.Report
	Stats  health.Stats
	Entry  *catalog.TableEntry
}

// Scan audits a single table.
func (e *Engine) Scan(ctx context.Context, id catalog.TableID, t health.Thresholds) (*Result, error) {
	start := time.Now()
	entry, err := e.Catalog.LoadTable(ctx, id)
	if err != nil {
		return nil, err
	}

	md, err := e.readMetadata(ctx, entry.MetadataLocation)
	if err != nil {
		return nil, err
	}

	stats, err := e.collectStats(ctx, md, entry.MetadataLocation)
	if err != nil {
		return nil, err
	}

	report := health.Score(id.String(), e.Catalog.Name(), stats, t)
	report.ScanDurationMS = time.Since(start).Milliseconds()
	if e.Branch != "" {
		report.Branch = e.Branch
	}
	return &Result{Report: report, Stats: *stats, Entry: entry}, nil
}

func (e *Engine) readMetadata(ctx context.Context, uri string) (*iceberg.TableMetadata, error) {
	rc, err := e.Storage.Open(ctx, uri)
	if err != nil {
		return nil, fmt.Errorf("open metadata %s: %w", uri, err)
	}
	defer func() { _ = rc.Close() }()
	return iceberg.ReadTableMetadata(ctx, rc)
}

func (e *Engine) collectStats(ctx context.Context, md *iceberg.TableMetadata, metadataURI string) (*health.Stats, error) {
	now := e.Now().UnixMilli()
	stats := &health.Stats{
		FormatVersion: int(md.FormatVersion),
		SnapshotCount: len(md.Snapshots),
	}
	for _, snap := range md.Snapshots {
		age := now - snap.TimestampMs
		if age < 0 {
			age = 0
		}
		if stats.OldestSnapshotAgeMs == 0 || age > stats.OldestSnapshotAgeMs {
			stats.OldestSnapshotAgeMs = age
		}
		if stats.NewestSnapshotAgeMs == 0 || age < stats.NewestSnapshotAgeMs {
			stats.NewestSnapshotAgeMs = age
		}
	}

	// Classify write pattern from the recent snapshot history.
	wp := writepattern.Classify(md.Snapshots, writepattern.Defaults())
	stats.WritePattern = wp.Pattern
	stats.AvgCommitIntervalMs = wp.AvgCommitIntervalMs
	stats.AvgFilesPerCommit = wp.AvgFilesPerCommit

	cur := e.resolveSnapshot(md)
	if cur == nil {
		// No matching snapshot — empty table, staging-only, or unknown branch.
		return stats, nil
	}
	stats.SnapshotID = cur.SnapshotID

	manifests, err := e.loadManifests(ctx, cur, metadataURI)
	if err != nil {
		return nil, err
	}
	stats.ManifestFileCount = int64(len(manifests))
	if cur.ManifestList != "" {
		stats.ManifestListFileCount = 1
	}

	partitionAgg := map[string]*health.PartitionStats{}
	for _, m := range manifests {
		uri := resolveSibling(metadataURI, m.Path)
		if err := e.scanManifest(ctx, uri, m, stats, partitionAgg); err != nil {
			return nil, err
		}
	}
	stats.Partitions = flattenPartitions(partitionAgg)

	return stats, nil
}

// resolveSnapshot picks the snapshot to scan. With Engine.Branch set, it
// looks up md.Refs[Branch] and finds that snapshot in md.Snapshots; falls
// back to current-snapshot if the branch is missing.
func (e *Engine) resolveSnapshot(md *iceberg.TableMetadata) *iceberg.Snapshot {
	target := md.CurrentSnapshotID
	if e.Branch != "" {
		if ref, ok := md.Refs[e.Branch]; ok {
			target = ref.SnapshotID
		} else {
			// Unknown branch: caller will see SnapshotID=0 in stats.
			return nil
		}
	}
	if target == 0 {
		return nil
	}
	for i := range md.Snapshots {
		if md.Snapshots[i].SnapshotID == target {
			return &md.Snapshots[i]
		}
	}
	return nil
}

func flattenPartitions(m map[string]*health.PartitionStats) []health.PartitionStats {
	if len(m) == 0 {
		return nil
	}
	out := make([]health.PartitionStats, 0, len(m))
	for _, v := range m {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Bytes > out[j].Bytes })
	return out
}

func (e *Engine) loadManifests(ctx context.Context, snap *iceberg.Snapshot, metadataURI string) ([]iceberg.ManifestFile, error) {
	if snap.ManifestList != "" {
		uri := resolveSibling(metadataURI, snap.ManifestList)
		rc, err := e.Storage.Open(ctx, uri)
		if err != nil {
			return nil, fmt.Errorf("open manifest list %s: %w", uri, err)
		}
		defer func() { _ = rc.Close() }()
		return iceberg.ReadManifestList(ctx, rc)
	}
	// v1 inline manifests.
	out := make([]iceberg.ManifestFile, 0, len(snap.Manifests))
	for _, p := range snap.Manifests {
		out = append(out, iceberg.ManifestFile{Path: p, Content: iceberg.ManifestContentData})
	}
	return out, nil
}

func (e *Engine) scanManifest(
	ctx context.Context,
	uri string,
	m iceberg.ManifestFile,
	stats *health.Stats,
	partitions map[string]*health.PartitionStats,
) error {
	rc, err := e.Storage.Open(ctx, uri)
	if err != nil {
		return fmt.Errorf("open manifest %s: %w", uri, err)
	}
	defer func() { _ = rc.Close() }()

	return iceberg.ReadManifestFile(ctx, rc, func(df iceberg.DataFile) error {
		if df.Status == 2 { // deleted entry
			return nil
		}
		switch df.Content {
		case iceberg.FileContentData:
			stats.DataFileCount++
			stats.DataFileTotalBytes += df.FileSizeBytes
			if df.FileSizeBytes < 64*1024*1024 {
				stats.SmallFileCountUnder64++
			}
			if df.FileSizeBytes < 128*1024*1024 {
				stats.SmallFileCountUnder128++
			}
			if len(stats.DataFileSizes) < 1024 {
				stats.DataFileSizes = append(stats.DataFileSizes, df.FileSizeBytes)
			}
			if key := partitionKey(df); key != "" {
				p := partitions[key]
				if p == nil {
					p = &health.PartitionStats{Key: key}
					partitions[key] = p
				}
				p.FileCount++
				p.Bytes += df.FileSizeBytes
				p.Rows += df.RecordCount
			}
		case iceberg.FileContentPositionDeletes:
			stats.PositionDeleteFiles++
			stats.DeleteFileTotalBytes += df.FileSizeBytes
		case iceberg.FileContentEqualityDeletes:
			stats.EqualityDeleteFiles++
			stats.DeleteFileTotalBytes += df.FileSizeBytes
		}
		return nil
	})
}

// partitionKey serialises a manifest entry's partition values into a stable
// composite key. Returns "" for unpartitioned tables.
func partitionKey(df iceberg.DataFile) string {
	if len(df.Partition) == 0 {
		return ""
	}
	keys := make([]string, 0, len(df.Partition))
	for k := range df.Partition {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('/')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(fmt.Sprint(df.Partition[k]))
	}
	return b.String()
}

// resolveSibling joins ref against base when ref is relative. Iceberg
// metadata files often store fully qualified URIs, but inline-manifest v1
// tables and some catalogs emit bare paths.
func resolveSibling(base, ref string) string {
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
	return path.Join(dir, ref)
}
