package main

import (
	"encoding/json"
	"io"
)

// writeJSON renders v as indented JSON, the form every --json output takes so a
// reader piping it to jq sees the same shape whichever command produced it.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	return enc.Encode(v)
}
