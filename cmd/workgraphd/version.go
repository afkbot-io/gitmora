package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"workgraph.local/platform/internal/distribution/buildinfo"
)

// serverVersion reports only linked release metadata; it never loads a role, secret or database.
func serverVersion(args []string, output io.Writer) error {
	if len(args) == 1 && args[0] == "--help" {
		_, err := fmt.Fprintln(output, "Usage: gitmorad version\nReports JSON release metadata without database, network or credentials.")
		return err
	}
	if len(args) != 0 {
		return errors.New("invalid version arguments")
	}
	return json.NewEncoder(output).Encode(buildinfo.Current())
}
