// Package policy parses sentry.yaml — the policy-as-code file used to
// configure thresholds, namespaces, and CI failure rules.
package policy

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/jaybilgaye/iceberg-sentry/internal/health"
)

// Policy is one sentry.yaml entry.
type Policy struct {
	Name              string  `yaml:"name"`
	TargetNamespace   string  `yaml:"target_namespace"`
	MinFileSizeMB     int64   `yaml:"min_file_size_mb"`
	MaxManifestFiles  int64   `yaml:"max_manifest_files"`
	MaxSnapshotAge    string  `yaml:"max_snapshot_age"`
	DeleteRatioWarn   float64 `yaml:"delete_file_ratio_warn"`
	DeleteRatioFail   float64 `yaml:"delete_file_ratio_fail"`
	MinHealthScore    int     `yaml:"min_health_score"`
	WritePattern      string  `yaml:"write_pattern"`
	FailOnOrphans     bool    `yaml:"fail_on_orphans"`
	FailOnPIIUntagged bool    `yaml:"fail_on_pii_untagged"`
	PIIScan           bool    `yaml:"pii_scan"`
}

// File is the top-level sentry.yaml document.
type File struct {
	Version        string   `yaml:"version"`
	DefaultCatalog string   `yaml:"default_catalog"`
	Policies       []Policy `yaml:"policies"`
}

// Load parses a sentry.yaml file from disk.
func Load(path string) (*File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return Parse(f)
}

// Parse reads and validates a sentry.yaml document from r.
func Parse(r io.Reader) (*File, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(false)
	var out File
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("decode sentry.yaml: %w", err)
	}
	if out.Version == "" {
		return nil, fmt.Errorf("sentry.yaml missing required field: version")
	}
	for i := range out.Policies {
		p := &out.Policies[i]
		if p.Name == "" {
			return nil, fmt.Errorf("policy[%d]: name is required", i)
		}
	}
	return &out, nil
}

// MatchTable returns the first policy whose target_namespace glob matches the
// table's namespace, or nil if none match.
func (f *File) MatchTable(namespace string) *Policy {
	for i := range f.Policies {
		p := &f.Policies[i]
		if matchGlob(p.TargetNamespace, namespace) {
			return p
		}
	}
	return nil
}

// matchGlob supports "*", trailing ".*", or exact matches. Sufficient for
// Iceberg namespace globs in policy files.
func matchGlob(pattern, s string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, ".*") {
		prefix := strings.TrimSuffix(pattern, ".*")
		return s == prefix || strings.HasPrefix(s, prefix+".")
	}
	return pattern == s
}

// ApplyThresholds derives a health.Thresholds from policy values, falling
// back to the spec defaults for any unspecified field.
func (p *Policy) ApplyThresholds(base health.Thresholds) (health.Thresholds, error) {
	t := base
	if p.MinFileSizeMB > 0 {
		t.MinFileSizeBytes = p.MinFileSizeMB * 1024 * 1024
	}
	if p.MaxManifestFiles > 0 {
		t.WarnManifestCount = p.MaxManifestFiles
		t.CritManifestCount = p.MaxManifestFiles * 2
	}
	if p.MaxSnapshotAge != "" {
		d, err := parseDuration(p.MaxSnapshotAge)
		if err != nil {
			return t, fmt.Errorf("policy %q: invalid max_snapshot_age: %w", p.Name, err)
		}
		t.WarnSnapshotAge = d
		t.CritSnapshotAge = d * 3
	}
	if p.DeleteRatioWarn > 0 {
		t.WarnDeleteRatio = p.DeleteRatioWarn
	}
	if p.DeleteRatioFail > 0 {
		t.CritDeleteRatio = p.DeleteRatioFail
	}
	return t, nil
}

// parseDuration extends time.ParseDuration to accept "30d" and "12h" style
// units used in sentry.yaml.
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	if strings.HasSuffix(s, "d") {
		var days int
		if _, err := fmt.Sscanf(s, "%dd", &days); err != nil || days <= 0 {
			return 0, fmt.Errorf("invalid days duration %q", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	return d, nil
}
