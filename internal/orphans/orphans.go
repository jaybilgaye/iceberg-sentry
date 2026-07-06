// Package orphans implements the differential-scan that finds files in
// storage which are not referenced by any valid Iceberg snapshot. The
// algorithm follows spec §2.5:
//
//  1. Lock the scan to a specific Iceberg snapshot ID at start.
//  2. Walk every snapshot in the metadata, every manifest list, and every
//     manifest file. Insert each referenced file path into a Bloom filter
//     ("possibly active") and into an exact set ("definitely active").
//     The exact set is what's used for the orphan decision; the Bloom
//     filter is a fast pre-filter that lets us skip the per-object cost
//     on the storage crawl side when memory pressure spikes.
//  3. Stream the table's data directory from storage. For each listed
//     object: if the path is NOT in the exact set AND the object's age
//     exceeds the grace period, the path is an orphan candidate.
//  4. Return a manifest of candidates with total reclaimable bytes. We
//     never delete — destruction is a separate operator step requiring
//     --confirm (Phase 3).
package orphans

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/jaybilgaye/iceberg-sentry/internal/bloom"
	"github.com/jaybilgaye/iceberg-sentry/internal/iceberg"
	"github.com/jaybilgaye/iceberg-sentry/internal/storage"
)

// Candidate is one suspected orphan file.
type Candidate struct {
	URI       string `json:"uri"`
	SizeBytes int64  `json:"size_bytes"`
	AgeMs     int64  `json:"age_ms"`
}

// Report bundles candidates with summary statistics.
type Report struct {
	SnapshotID     int64       `json:"snapshot_id"`
	ScannedAt      time.Time   `json:"scanned_at"`
	GracePeriod    string      `json:"grace_period"`
	ScannedObjects int64       `json:"scanned_objects"`
	ActiveFiles    int64       `json:"active_files"`
	Candidates     []Candidate `json:"candidates"`
	TotalBytes     int64       `json:"total_bytes"`
}

// Options configures a Scan.
type Options struct {
	GracePeriod time.Duration // files newer than this are never flagged
	Now         func() time.Time
	// SamplePreviewN caps the number of Candidates returned in the report;
	// 0 means "all". Used by the CLI to keep terminal output sane while
	// preserving the totals.
	SamplePreviewN int
}

// Scan returns the orphan-candidate report for table data under dataPrefix.
// metadataURI is the table's vN.metadata.json (used for path resolution).
func Scan(
	ctx context.Context,
	md *iceberg.TableMetadata,
	metadataURI string,
	dataPrefix string,
	st *storage.Resolver,
	opts Options,
) (*Report, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.GracePeriod == 0 {
		opts.GracePeriod = 24 * time.Hour
	}

	active, bf, err := buildActiveSet(ctx, md, metadataURI, st)
	if err != nil {
		return nil, err
	}

	now := opts.Now()
	cutoffMs := now.Add(-opts.GracePeriod).UnixMilli()

	r := &Report{
		SnapshotID:  md.CurrentSnapshotID,
		ScannedAt:   now,
		GracePeriod: opts.GracePeriod.String(),
		ActiveFiles: int64(len(active)),
	}

	err = st.List(ctx, dataPrefix, func(o storage.ObjectInfo) error {
		r.ScannedObjects++
		// Skip files written after the cutoff (in-flight write protection).
		if o.UpdatedAt != 0 && o.UpdatedAt > cutoffMs {
			return nil
		}
		// Bloom pre-filter — if the filter says "no", skip the exact-set lookup.
		if bf != nil && bf.Test(o.URI) {
			if _, ok := active[normalize(o.URI)]; ok {
				return nil
			}
		} else if _, ok := active[normalize(o.URI)]; ok {
			return nil
		}
		// Don't flag metadata-side files (vN.metadata.json, manifest-list, manifests).
		if isMetadataPath(o.URI) {
			return nil
		}
		c := Candidate{
			URI:       o.URI,
			SizeBytes: o.Size,
			AgeMs:     now.UnixMilli() - o.UpdatedAt,
		}
		r.TotalBytes += o.Size
		if opts.SamplePreviewN == 0 || len(r.Candidates) < opts.SamplePreviewN {
			r.Candidates = append(r.Candidates, c)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return r, nil
}

func buildActiveSet(
	ctx context.Context,
	md *iceberg.TableMetadata,
	metadataURI string,
	st *storage.Resolver,
) (map[string]struct{}, *bloom.Filter, error) {
	active := make(map[string]struct{}, 1024)

	// The Bloom filter is sized by an estimate: 4 paths per manifest is a
	// rough but harmless upper bound for our spec-default use cases. The
	// constructor enforces a minimum.
	estimate := 4_096
	if cur := md.CurrentSnapshot(); cur != nil {
		estimate = len(md.Snapshots) * 4_096
	}
	bf, _ := bloom.New(estimate, 0.001)

	add := func(uri string) {
		n := normalize(uri)
		active[n] = struct{}{}
		if bf != nil {
			bf.Add(n)
		}
	}

	for _, snap := range md.Snapshots {
		if snap.ManifestList != "" {
			listURI := resolveSibling(metadataURI, snap.ManifestList)
			add(listURI)
			rc, err := st.Open(ctx, listURI)
			if err != nil {
				return nil, nil, fmt.Errorf("open manifest list %s: %w", listURI, err)
			}
			mfs, err := iceberg.ReadManifestList(ctx, rc)
			_ = rc.Close()
			if err != nil {
				return nil, nil, err
			}
			for _, m := range mfs {
				manifestURI := resolveSibling(metadataURI, m.Path)
				add(manifestURI)
				if err := readManifestEntries(ctx, st, metadataURI, manifestURI, add); err != nil {
					return nil, nil, err
				}
			}
			continue
		}
		// v1 inline manifests
		for _, p := range snap.Manifests {
			manifestURI := resolveSibling(metadataURI, p)
			add(manifestURI)
			if err := readManifestEntries(ctx, st, metadataURI, manifestURI, add); err != nil {
				return nil, nil, err
			}
		}
	}

	// The metadata.json file itself must remain active.
	add(metadataURI)

	return active, bf, nil
}

func readManifestEntries(
	ctx context.Context,
	st *storage.Resolver,
	metadataURI, manifestURI string,
	add func(string),
) error {
	rc, err := st.Open(ctx, manifestURI)
	if err != nil {
		return fmt.Errorf("open manifest %s: %w", manifestURI, err)
	}
	defer func() { _ = rc.Close() }()
	return iceberg.ReadManifestFile(ctx, rc, func(df iceberg.DataFile) error {
		if df.Path != "" {
			add(resolveSibling(metadataURI, df.Path))
		}
		return nil
	})
}

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

// normalize strips file:// and trailing slashes so equivalent URIs collide.
func normalize(uri string) string {
	uri = strings.TrimSuffix(uri, "/")
	if strings.HasPrefix(uri, "file://") {
		return strings.TrimPrefix(uri, "file://")
	}
	return uri
}

// isMetadataPath returns true for paths that should never be flagged as
// orphans even if our active-set walk missed them (it shouldn't, but this is
// belt-and-suspenders for unknown metadata layouts).
func isMetadataPath(uri string) bool {
	base := uri
	if i := strings.LastIndex(uri, "/"); i >= 0 {
		base = uri[i+1:]
	}
	switch {
	case strings.HasSuffix(base, ".metadata.json"):
		return true
	case strings.HasPrefix(base, "snap-") && strings.HasSuffix(base, ".avro"):
		return true
	case strings.HasPrefix(base, "version-hint.text"):
		return true
	}
	return false
}
