// Package output provides functions for writing structured output.
package output

import (
	"encoding/json"
	"fmt"
	"io"
)

// WriteJSON writes a value as JSON to the given writer.
func WriteJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// WriteError writes an error message as JSON to the given writer.
func WriteError(w io.Writer, msg string) {
	_ = WriteJSON(w, map[string]string{"status": "error", "message": msg})
	_, _ = fmt.Fprintln(w)
}
