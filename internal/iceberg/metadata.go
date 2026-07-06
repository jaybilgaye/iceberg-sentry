package iceberg

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// ReadTableMetadata streams a vN.metadata.json document from r and returns
// the decoded TableMetadata. The reader is consumed but not closed.
func ReadTableMetadata(_ context.Context, r io.Reader) (*TableMetadata, error) {
	dec := json.NewDecoder(r)
	dec.UseNumber()
	var raw map[string]json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode metadata json: %w", err)
	}

	// Re-marshal then decode into the typed struct. Going through a
	// RawMessage map first lets us reject a few malformed inputs cleanly
	// before Go's stricter typed unmarshaller runs.
	if _, ok := raw["format-version"]; !ok {
		return nil, fmt.Errorf("metadata is missing required field format-version")
	}

	combined, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("re-marshal metadata: %w", err)
	}

	var md TableMetadata
	if err := json.Unmarshal(combined, &md); err != nil {
		return nil, fmt.Errorf("unmarshal metadata: %w", err)
	}
	if md.FormatVersion < 1 || md.FormatVersion > 3 {
		return nil, fmt.Errorf("unsupported format-version %d", md.FormatVersion)
	}
	return &md, nil
}
