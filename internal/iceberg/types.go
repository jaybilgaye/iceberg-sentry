// Package iceberg contains data types and parsers for the Apache Iceberg
// table specification (v1, v2, and forward-compatible parsing for v3).
//
// The metadata model intentionally mirrors the Iceberg spec field names so
// JSON unmarshalling is direct. Only fields consumed by iceberg-sentry's
// diagnostics are typed; unknown fields are ignored.
//
// Spec references:
//   - https://iceberg.apache.org/spec/
//   - https://github.com/apache/iceberg/blob/main/format/spec.md
package iceberg

// FormatVersion is the Iceberg table format version.
type FormatVersion int

const (
	FormatV1 FormatVersion = 1
	FormatV2 FormatVersion = 2
	FormatV3 FormatVersion = 3
)

// TableMetadata is the parsed contents of vN.metadata.json.
type TableMetadata struct {
	FormatVersion      FormatVersion          `json:"format-version"`
	TableUUID          string                 `json:"table-uuid"`
	Location           string                 `json:"location"`
	LastUpdatedMs      int64                  `json:"last-updated-ms"`
	LastColumnID       int                    `json:"last-column-id"`
	Schemas            []Schema               `json:"schemas"`
	CurrentSchemaID    int                    `json:"current-schema-id"`
	PartitionSpecs     []PartitionSpec        `json:"partition-specs"`
	DefaultSpecID      int                    `json:"default-spec-id"`
	LastPartitionID    int                    `json:"last-partition-id"`
	Properties         map[string]string      `json:"properties"`
	CurrentSnapshotID  int64                  `json:"current-snapshot-id"`
	Snapshots          []Snapshot             `json:"snapshots"`
	SnapshotLog        []SnapshotLogEntry     `json:"snapshot-log"`
	MetadataLog        []MetadataLogEntry     `json:"metadata-log"`
	SortOrders         []SortOrder            `json:"sort-orders"`
	DefaultSortOrderID int                    `json:"default-sort-order-id"`
	Refs               map[string]SnapshotRef `json:"refs"`
	Statistics         []StatisticsFile       `json:"statistics"`
}

// Schema is a minimal schema descriptor; field-level details are not needed
// for Phase 1 health analysis.
type Schema struct {
	SchemaID int           `json:"schema-id"`
	Type     string        `json:"type"`
	Fields   []SchemaField `json:"fields"`
}

type SchemaField struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Required bool   `json:"required"`
	Type     any    `json:"type"`
	Doc      string `json:"doc,omitempty"`
}

type PartitionSpec struct {
	SpecID int              `json:"spec-id"`
	Fields []PartitionField `json:"fields"`
}

type PartitionField struct {
	SourceID  int    `json:"source-id"`
	FieldID   int    `json:"field-id"`
	Name      string `json:"name"`
	Transform string `json:"transform"`
}

// Snapshot describes one committed point in the table's history.
type Snapshot struct {
	SnapshotID       int64  `json:"snapshot-id"`
	ParentSnapshotID *int64 `json:"parent-snapshot-id,omitempty"`
	SequenceNumber   int64  `json:"sequence-number"`
	TimestampMs      int64  `json:"timestamp-ms"`
	ManifestList     string `json:"manifest-list"`
	// Older v1 metadata may inline manifests instead of using a manifest-list.
	Manifests []string          `json:"manifests,omitempty"`
	Summary   map[string]string `json:"summary,omitempty"`
	SchemaID  *int              `json:"schema-id,omitempty"`
}

type SnapshotLogEntry struct {
	TimestampMs int64 `json:"timestamp-ms"`
	SnapshotID  int64 `json:"snapshot-id"`
}

type MetadataLogEntry struct {
	TimestampMs  int64  `json:"timestamp-ms"`
	MetadataFile string `json:"metadata-file"`
}

type SortOrder struct {
	OrderID int              `json:"order-id"`
	Fields  []SortOrderField `json:"fields"`
}

type SortOrderField struct {
	Transform string `json:"transform"`
	SourceID  int    `json:"source-id"`
	Direction string `json:"direction"`
	NullOrder string `json:"null-order"`
}

// SnapshotRef is an Iceberg branch or tag reference.
type SnapshotRef struct {
	SnapshotID         int64  `json:"snapshot-id"`
	Type               string `json:"type"` // "branch" or "tag"
	MinSnapshotsToKeep *int   `json:"min-snapshots-to-keep,omitempty"`
	MaxSnapshotAgeMs   *int64 `json:"max-snapshot-age-ms,omitempty"`
	MaxRefAgeMs        *int64 `json:"max-ref-age-ms,omitempty"`
}

type StatisticsFile struct {
	SnapshotID int64  `json:"snapshot-id"`
	Path       string `json:"statistics-path"`
	FileSize   int64  `json:"file-size-in-bytes"`
}

// CurrentSnapshot returns the snapshot pointed at by current-snapshot-id, or
// nil if the table has no current snapshot.
func (m *TableMetadata) CurrentSnapshot() *Snapshot {
	if m == nil || m.CurrentSnapshotID == 0 {
		return nil
	}
	for i := range m.Snapshots {
		if m.Snapshots[i].SnapshotID == m.CurrentSnapshotID {
			return &m.Snapshots[i]
		}
	}
	return nil
}
