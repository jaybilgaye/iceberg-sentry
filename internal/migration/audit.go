// Package migration implements the on-prem HDFS → CDP Public Cloud
// Migration Readiness Audit (spec §5.5).
//
// The audit is pure metadata work — no compute or storage IO beyond what
// the regular scan already does. It looks at the table metadata + manifest
// list for patterns that will break or get expensive after migration:
//
//   - Absolute HDFS paths in metadata (hdfs://, hdfs:///, /user/hive/...)
//   - Table properties that are HDFS-specific
//   - v1 tables that should be upgraded to v2 before migration
//   - Total reachable data size (informs S3/ADLS landing cost)
//
// The output is a per-table risk score (Low/Medium/High) plus a list of
// remediation steps.
package migration

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/jaybilgaye/iceberg-sentry/internal/iceberg"
	"github.com/jaybilgaye/iceberg-sentry/internal/storage"
)

// RiskLevel is the per-table summary.
type RiskLevel string

const (
	RiskLow    RiskLevel = "LOW"
	RiskMedium RiskLevel = "MEDIUM"
	RiskHigh   RiskLevel = "HIGH"
)

// Finding is one Migration Readiness flag for a table.
type Finding struct {
	Code       string `json:"code"`
	Severity   string `json:"severity"`
	Message    string `json:"message"`
	Remediation string `json:"remediation,omitempty"`
}

// Report is the per-table migration audit result.
type Report struct {
	Table          string    `json:"table"`
	Catalog        string    `json:"catalog,omitempty"`
	FormatVersion  int       `json:"format_version"`
	Risk           RiskLevel `json:"risk"`
	Findings       []Finding `json:"findings"`
	TotalDataBytes int64     `json:"total_data_bytes"`
	ManifestCount  int64     `json:"manifest_count"`
	DataFileCount  int64     `json:"data_file_count"`
}

// HDFSpecificProperties is the set of Iceberg table properties known to
// break or behave differently on object storage.
var HDFSpecificProperties = map[string]string{
	"write.metadata.path":             "absolute HDFS path; rewrite to a location-relative path",
	"write.folder-storage.path":       "HDFS-specific; not honoured by S3FileIO",
	"write.distribution-mode":         "value 'hadoop' is HDFS-only; switch to 'hash' or 'range'",
	"hadoop.use.file.transfer.scheme": "HDFS-only",
}

// Audit walks the table metadata and the current snapshot's manifests,
// returning a Migration Readiness Report. Manifest data is sampled to bound
// memory; only the first N manifests contribute to data-byte and file-count
// totals (N = 256 by default).
func Audit(ctx context.Context, table string, md *iceberg.TableMetadata, metadataURI string, st *storage.Resolver) (*Report, error) {
	r := &Report{
		Table:         table,
		FormatVersion: int(md.FormatVersion),
		Risk:          RiskLow,
	}

	// 1. Path scheme analysis on the metadata-side URIs.
	scanned := []string{
		md.Location,
		metadataURI,
	}
	for _, snap := range md.Snapshots {
		if snap.ManifestList != "" {
			scanned = append(scanned, snap.ManifestList)
		}
	}
	for _, uri := range scanned {
		if uri == "" {
			continue
		}
		scheme := schemeOf(uri)
		if strings.HasPrefix(scheme, "hdfs") {
			r.Findings = append(r.Findings, Finding{
				Code:        "HDFS_PATH",
				Severity:    "HIGH",
				Message:     fmt.Sprintf("absolute hdfs:// path in metadata: %s", uri),
				Remediation: "re-register the table with a location-relative metadata path before migration",
			})
		}
		// /user/hive/warehouse/...-style bare paths are also HDFS-specific.
		if scheme == "" && strings.HasPrefix(uri, "/user/hive") {
			r.Findings = append(r.Findings, Finding{
				Code:        "HDFS_BARE_PATH",
				Severity:    "MEDIUM",
				Message:     fmt.Sprintf("bare HDFS warehouse path in metadata: %s", uri),
				Remediation: "switch to an absolute s3:// or abfss:// location prefix",
			})
		}
	}

	// 2. HDFS-specific properties.
	for key, msg := range HDFSpecificProperties {
		if v, ok := md.Properties[key]; ok {
			r.Findings = append(r.Findings, Finding{
				Code:        "HDFS_PROPERTY",
				Severity:    "MEDIUM",
				Message:     fmt.Sprintf("table property %s=%s — %s", key, v, msg),
				Remediation: fmt.Sprintf("ALTER TABLE %s UNSET TBLPROPERTIES ('%s')", table, key),
			})
		}
	}

	// 3. v1 tables should be upgraded before migration — cheaper to do
	//    on-prem against a warm cache than after migration over slower S3 reads.
	if md.FormatVersion == 1 {
		r.Findings = append(r.Findings, Finding{
			Code:        "V1_UPGRADE_RECOMMENDED",
			Severity:    "MEDIUM",
			Message:     "table is Iceberg v1; v2 supports row-level deletes (merge-on-read) and is the migration target",
			Remediation: fmt.Sprintf("ALTER TABLE %s SET TBLPROPERTIES ('format-version'='2')", table),
		})
	}

	// 4. Manifest sweep for data-byte total and HDFS data-file paths.
	if cur := md.CurrentSnapshot(); cur != nil {
		manifestURIs, err := loadManifestList(ctx, cur, metadataURI, st)
		if err == nil {
			r.ManifestCount = int64(len(manifestURIs))
			limit := 256
			if len(manifestURIs) < limit {
				limit = len(manifestURIs)
			}
			hdfsDataPathSeen := false
			for i := 0; i < limit; i++ {
				rc, err := st.Open(ctx, manifestURIs[i])
				if err != nil {
					continue
				}
				err = iceberg.ReadManifestFile(ctx, rc, func(df iceberg.DataFile) error {
					if df.Status == 2 {
						return nil
					}
					r.DataFileCount++
					r.TotalDataBytes += df.FileSizeBytes
					if !hdfsDataPathSeen {
						if scheme := schemeOf(df.Path); strings.HasPrefix(scheme, "hdfs") {
							hdfsDataPathSeen = true
							r.Findings = append(r.Findings, Finding{
								Code:        "HDFS_DATA_PATH",
								Severity:    "HIGH",
								Message:     "data files referenced by absolute hdfs:// path",
								Remediation: "rewrite manifests with object-store URIs (DISTCP + ALTER TABLE ... REGISTER, or rewrite_data_files post-migration)",
							})
						}
					}
					return nil
				})
				_ = rc.Close()
				if err != nil {
					break
				}
			}
		}
	}

	r.Risk = computeRisk(r.Findings)
	return r, nil
}

func schemeOf(uri string) string {
	if i := strings.Index(uri, "://"); i > 0 {
		return strings.ToLower(uri[:i])
	}
	return ""
}

func computeRisk(findings []Finding) RiskLevel {
	var high, medium int
	for _, f := range findings {
		switch f.Severity {
		case "HIGH":
			high++
		case "MEDIUM":
			medium++
		}
	}
	switch {
	case high >= 1:
		return RiskHigh
	case medium >= 2:
		return RiskHigh
	case medium >= 1:
		return RiskMedium
	}
	return RiskLow
}

// loadManifestList walks the current snapshot's manifest-list and returns
// the absolute URIs of every manifest file.
func loadManifestList(ctx context.Context, snap *iceberg.Snapshot, metadataURI string, st *storage.Resolver) ([]string, error) {
	if snap.ManifestList == "" {
		out := make([]string, len(snap.Manifests))
		for i, p := range snap.Manifests {
			out[i] = resolveSibling(metadataURI, p)
		}
		return out, nil
	}
	listURI := resolveSibling(metadataURI, snap.ManifestList)
	rc, err := st.Open(ctx, listURI)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	mfs, err := iceberg.ReadManifestList(ctx, rc)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(mfs))
	for _, m := range mfs {
		out = append(out, resolveSibling(metadataURI, m.Path))
	}
	return out, nil
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
