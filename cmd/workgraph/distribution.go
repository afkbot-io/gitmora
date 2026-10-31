package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"workgraph.local/platform/internal/distribution/buildinfo"
	distribution "workgraph.local/platform/internal/distribution/domain"
	"workgraph.local/platform/internal/distribution/local"
)

func clientVersion(args []string, output io.Writer) error {
	if len(args) == 1 && args[0] == "--help" {
		_, err := fmt.Fprintln(output, "Usage: gitmora version\nReports JSON build metadata without network, repository or credentials.")
		return err
	}
	if len(args) != 0 {
		return distribution.ErrInvalid
	}
	return json.NewEncoder(output).Encode(buildinfo.Current())
}

func installClient(ctx context.Context, args []string, output io.Writer) error {
	if len(args) == 0 || args[0] != "install" {
		return distribution.ErrInvalid
	}
	flags := flag.NewFlagSet("self install", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	directory := flags.String("directory", "", "Existing current-user-owned stable installation directory")
	expected := flags.String("replace-sha256", "", "Exact SHA-256 of a different existing executable, required for replacement")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, err = fmt.Fprintln(output, "Usage: gitmora self install --directory <absolute-directory> [--replace-sha256 <current-digest>]\nInstalls this executable as gitmora without changing Git, credentials or shell configuration.")
			return err
		}
		return distribution.ErrInvalid
	}
	if flags.NArg() != 0 || *directory == "" || (*expected != "" && !distribution.Digest(*expected)) {
		return distribution.ErrInvalid
	}
	source, err := os.Executable()
	if err != nil {
		return distribution.ErrIO
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		return distribution.ErrIO
	}
	receipt, err := local.Install(ctx, *directory, source, *expected)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(receipt)
}
