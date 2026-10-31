package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	contextnative "workgraph.local/platform/internal/contextpack/native"
	identity "workgraph.local/platform/internal/identity/native"
)

type protocolWriter struct{ io.Writer }

func (protocolWriter) Close() error { return nil }
func mcpCommand(ctx context.Context, args []string, input io.Reader, out io.Writer) error {
	flags := flag.NewFlagSet("mcp", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	const usage = "Usage: gitmora mcp --server <URL>\nRuns stdio MCP using the saved account or WORKGRAPH_TOKEN. Stdout carries only protocol messages; no token belongs in agent configuration."
	flags.Usage = func() {}

	server := flags.String("server", "", "Exact Gitmora server URL")
	if e := flags.Parse(args); errors.Is(e, flag.ErrHelp) {
		_, err := fmt.Fprintln(out, usage)
		return err
	} else if e != nil || flags.NArg() != 0 {
		return identity.ErrInvalid
	}
	canonical, e := identity.CanonicalServer(*server)
	if e != nil || input == nil || out == nil {
		return identity.ErrInvalid
	}
	key, e := credentialSource(canonical, "WORKGRAPH_TOKEN", os.LookupEnv, loadNativeCredential, time.Now)(ctx)
	if e != nil {
		return e
	}
	reader, ok := input.(io.ReadCloser)
	if !ok {
		reader = io.NopCloser(input)
	}
	return contextnative.Run(ctx, canonical, key, &mcp.IOTransport{Reader: reader, Writer: protocolWriter{out}, MaxLineLength: 65536})
}
