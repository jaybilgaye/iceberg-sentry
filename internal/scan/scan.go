// Package scan orchestrates a full table audit: catalog lookup, metadata
// JSON parse, manifest-list traversal, and per-manifest data-file walk. The
// output is a health.Stats record that the health package converts into a
// scored Report.
package scan

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/jaybilgaye/iceberg-sentry/internal/catalog"
	"github.com/jaybilgaye/iceberg-sentry/internal/health"
	"github.com/jaybilgaye/iceberg-sentry/internal/iceberg"
	"github.com/jaybilgaye/iceberg-sentry/internal/storage"
)

// Engine performs scans against a single (catalog, storage) pair.
type Engine struct {
	Catalog catalog.Catalog
	Storage *storage.Resolver
	// Now is injected so tests can pin "current time" for snapshot age math.
	Now func() time.Time
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

	cur := md.CurrentSnapshot()
	if cur == nil {
		// No current snapshot — empty table or staging-only. Stats are valid as-is.
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

	for _, m := range manifests {
		uri := resolveSibling(metadataURI, m.Path)
		if err := e.scanManifest(ctx, uri, m, stats); err != nil {
			return nil, err
		}
	}

	return stats, nil
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

func (e *Engine) scanManifest(ctx context.Context, uri string, m iceberg.ManifestFile, stats *health.Stats) error {
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
