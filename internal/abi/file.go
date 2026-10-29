package abi

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

// FileSource implements Source by loading an interface JSON from a local file.
// It serves the interface from the file for any contract ID.
type FileSource struct {
	iface *Interface
}

var _ Source = (*FileSource)(nil)

// NewFileSource reads an interface from path.
// The file format can be the raw Interface object (with "functions") or
// the wrapper object containing an "interface" field (e.g. `sorovault get <id> --json`).
func NewFileSource(path string) (*FileSource, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read abi file %s: %w", path, err)
	}

	// First try decoding as the wrapper {"interface": { "functions": [...] }}
	var wrapper struct {
		Interface *Interface `json:"interface"`
	}
	if err := json.Unmarshal(raw, &wrapper); err == nil && wrapper.Interface != nil && len(wrapper.Interface.Functions) > 0 {
		return &FileSource{iface: wrapper.Interface}, nil
	}

	// Next try decoding directly as {"functions": [...]}
	var iface Interface
	if err := json.Unmarshal(raw, &iface); err == nil && len(iface.Functions) > 0 {
		return &FileSource{iface: &iface}, nil
	}

	// If neither succeeded or functions is empty, report a malformed error
	// To give a descriptive error, if unmarshaling failed, report the error.
	var testJSON any
	if err := json.Unmarshal(raw, &testJSON); err != nil {
		return nil, fmt.Errorf("parse abi file %s: %w", path, err)
	}

	return nil, fmt.Errorf("abi file %s is malformed: missing or empty interface functions", path)
}

// Interface implements Source, serving the loaded interface for any contract ID.
func (f *FileSource) Interface(_ context.Context, _, _ string) (*Interface, error) {
	return f.iface, nil
}
