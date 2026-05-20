package cli

import (
	"encoding/json"
	"fmt"
	"io"
)

func writeOutput(w io.Writer, format outputFormat, text string, value any) error {
	switch format {
	case outputJSON:
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(value)
	case outputText:
		_, err := fmt.Fprintln(w, text)
		return err
	default:
		return fmt.Errorf("unsupported output format %q", format)
	}
}
