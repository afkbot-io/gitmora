package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"workgraph.local/platform/internal/transport/nativeapi"
)

func apiCommand(parent context.Context, args []string, input io.Reader, out io.Writer) error {
	const usage = "Usage: gitmora api get|post|put|patch|delete /v1/<path> --server <URL> [--input <JSON file or ->]\nUses the saved account or explicitly configured WORKGRAPH_TOKEN. Mutations are sent once; keep the same request_id when recovering uncertain results."
	if len(args) == 0 || args[0] == "--help" {
		_, e := fmt.Fprintln(out, usage)
		return e
	}
	if len(args) < 2 {
		return nativeapi.ErrInvalid
	}
	method, route := strings.ToUpper(args[0]), args[1]
	flags := flag.NewFlagSet("api", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}
	server := flags.String("server", "", "Exact Gitmora server URL")
	source := flags.String("input", "", "JSON object file; - reads stdin")
	if e := flags.Parse(args[2:]); errors.Is(e, flag.ErrHelp) {
		_, err := fmt.Fprintln(out, usage)
		return err
	} else if e != nil || flags.NArg() != 0 {
		return nativeapi.ErrInvalid
	}
	if nativeapi.Validate(method, route, nil) != nil || method == "GET" && *source != "" {
		return nativeapi.ErrInvalid
	}
	client, e := nativeapi.New(*server)
	if e != nil {
		return e
	}
	var raw []byte
	if *source != "" {
		var reader io.Reader = input
		if *source != "-" {
			info, e := os.Lstat(*source)
			if e != nil || !info.Mode().IsRegular() {
				return nativeapi.ErrInvalid
			}
			file, e := os.Open(*source)
			if e != nil {
				return nativeapi.ErrInvalid
			}
			defer file.Close()
			info, e = file.Stat()
			if e != nil || !info.Mode().IsRegular() {
				return nativeapi.ErrInvalid
			}
			reader = file
		}
		if reader == nil {
			return nativeapi.ErrInvalid
		}
		raw, e = io.ReadAll(io.LimitReader(reader, 3*1024*1024+1))
		if e != nil || len(raw) == 0 {
			return nativeapi.ErrInvalid
		}
	}
	if nativeapi.Validate(method, route, raw) != nil {
		return nativeapi.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(parent, 25*time.Second)
	defer cancel()
	token, e := credentialSource(*server, "WORKGRAPH_TOKEN", os.LookupEnv, loadNativeCredential, time.Now)(ctx)
	if e != nil {
		return e
	}
	result, e := client.Do(ctx, method, route, raw, token)
	if e != nil {
		return e
	}
	return json.NewEncoder(out).Encode(result)
}
