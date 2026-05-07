package iceberg

import (
	"context"
	"fmt"
	"io"

	"github.com/hamba/avro/v2/ocf"
)

// ManifestContent indicates whether a manifest tracks data files or delete files.
// Iceberg manifest content codes: 0 = data, 1 = deletes.
type ManifestContent int

const (
	ManifestContentData    ManifestContent = 0
	ManifestContentDeletes ManifestContent = 1
)

// ManifestFile is one entry from the manifest-list Avro file (the "snap-…avro").
// Field names mirror the Iceberg spec; only fields used by Phase 1 diagnostics
// are typed.
type ManifestFile struct {
	Path             string
	Length           int64
	PartitionSpecID  int32
	Content          ManifestContent
	SequenceNumber   int64
	MinSequenceNumber int64
	AddedSnapshotID  int64
	AddedFilesCount  int32
	ExistingFilesCount int32
	DeletedFilesCount  int32
	AddedRowsCount   int64
	ExistingRowsCount int64
	DeletedRowsCount  int64
}

// FileContent identifies whether a manifest entry refers to a data file or a
// delete file (and which kind of delete file).
// Iceberg data_file.content codes: 0 = data, 1 = position deletes, 2 = equality deletes.
type FileContent int

const (
	FileContentData              FileContent = 0
	FileContentPositionDeletes   FileContent = 1
	FileContentEqualityDeletes   FileContent = 2
)

// DataFile is one entry from a manifest file. Phase 1 only consumes file
// path, content, format, partition values, record count, and file size.
type DataFile struct {
	Status      int32 // 0 = existing, 1 = added, 2 = deleted (manifest_entry status)
	SnapshotID  int64
	SequenceNum int64

	Content     FileContent
	Path        string
	Format      string
	Partition   map[string]any
	RecordCount int64
	FileSizeBytes int64
}

// ReadManifestList streams entries from an Iceberg manifest-list (Avro OCF).
// The reader is consumed but not closed.
func ReadManifestList(_ context.Context, r io.Reader) ([]ManifestFile, error) {
	dec, err := ocf.NewDecoder(r)
	if err != nil {
		return nil, fmt.Errorf("open manifest-list: %w", err)
	}

	var out []ManifestFile
	for dec.HasNext() {
		var rec map[string]any
		if err := dec.Decode(&rec); err != nil {
			return nil, fmt.Errorf("decode manifest-list entry: %w", err)
		}
		out = append(out, manifestFromRecord(rec))
	}
	if err := dec.Error(); err != nil {
		return nil, fmt.Errorf("manifest-list decoder: %w", err)
	}
	return out, nil
}

// ReadManifestFile streams data-file entries from a manifest Avro file. Use
// the visit callback to process entries without buffering them all (important
// for tables with millions of files). Returning a non-nil error from visit
// stops iteration.
func ReadManifestFile(_ context.Context, r io.Reader, visit func(DataFile) error) error {
	dec, err := ocf.NewDecoder(r)
	if err != nil {
		return fmt.Errorf("open manifest file: %w", err)
	}
	for dec.HasNext() {
		var rec map[string]any
		if err := dec.Decode(&rec); err != nil {
			return fmt.Errorf("decode manifest entry: %w", err)
		}
		df, ok := dataFileFromManifestEntry(rec)
		if !ok {
			continue
		}
		if err := visit(df); err != nil {
			return err
		}
	}
	return dec.Error()
}

func manifestFromRecord(rec map[string]any) ManifestFile {
	mf := ManifestFile{
		Path:               asString(rec["manifest_path"]),
		Length:             asInt64(rec["manifest_length"]),
		PartitionSpecID:    int32(asInt64(rec["partition_spec_id"])),
		Content:            ManifestContent(asInt64(rec["content"])),
		SequenceNumber:     asInt64(rec["sequence_number"]),
		MinSequenceNumber:  asInt64(rec["min_sequence_number"]),
		AddedSnapshotID:    asInt64(rec["added_snapshot_id"]),
		AddedFilesCount:    int32(asInt64(firstNonNil(rec, "added_data_files_count", "added_files_count"))),
		ExistingFilesCount: int32(asInt64(firstNonNil(rec, "existing_data_files_count", "existing_files_count"))),
		DeletedFilesCount:  int32(asInt64(firstNonNil(rec, "deleted_data_files_count", "deleted_files_count"))),
		AddedRowsCount:     asInt64(rec["added_rows_count"]),
		ExistingRowsCount:  asInt64(rec["existing_rows_count"]),
		DeletedRowsCount:   asInt64(rec["deleted_rows_count"]),
	}
	return mf
}

func dataFileFromManifestEntry(rec map[string]any) (DataFile, bool) {
	dfRaw, ok := rec["data_file"].(map[string]any)
	if !ok {
		return DataFile{}, false
	}
	df := DataFile{
		Status:        int32(asInt64(rec["status"])),
		SnapshotID:    asInt64(rec["snapshot_id"]),
		SequenceNum:   asInt64(firstNonNil(rec, "sequence_number", "data_sequence_number")),
		Content:       FileContent(asInt64(dfRaw["content"])),
		Path:          asString(firstNonNil(dfRaw, "file_path", "path")),
		Format:        asString(dfRaw["file_format"]),
		RecordCount:   asInt64(dfRaw["record_count"]),
		FileSizeBytes: asInt64(dfRaw["file_size_in_bytes"]),
	}
	if part, ok := dfRaw["partition"].(map[string]any); ok {
		df.Partition = part
	}
	return df, true
}

// --- helpers -------------------------------------------------------------

func asInt64(v any) int64 {
	switch x := v.(type) {
	case nil:
		return 0
	case int:
		return int64(x)
	case int32:
		return int64(x)
	case int64:
		return x
	case float32:
		return int64(x)
	case float64:
		return int64(x)
	case map[string]any:
		// Avro union encoded as {"long": 12} etc.
		for _, vv := range x {
			return asInt64(vv)
		}
	}
	return 0
}

func asString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []byte:
		return string(x)
	case map[string]any:
		for _, vv := range x {
			return asString(vv)
		}
	}
	return ""
}

func firstNonNil(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			return v
		}
	}
	return nil
}
