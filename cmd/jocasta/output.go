package main

import (
	"encoding/json"
	"io"
)

// writeJSON renders v as indented JSON. Every --json output uses it, so jq sees
// the same shape from every command.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	return enc.Encode(v)
}
