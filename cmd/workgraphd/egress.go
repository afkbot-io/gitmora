package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"time"

	"workgraph.local/platform/internal/platform/egress"
	"workgraph.local/platform/internal/platform/netroute"
)

// egressCommand is an independently isolated role without business database/provider/keyring configuration.
func egressCommand(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("egress", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	listen := flags.String("listen", "0.0.0.0:8080", "Private CONNECT listener")
	target := flags.String("target", "gitlab.com:443", "Exact operator-selected TLS host:port")
	visibility := flags.String("visibility", "public", "Checked destination addresses: public IPv4 or private")
	flags.Usage = func() {
		_, _ = io.WriteString(out, "Usage: gitmorad egress --listen 0.0.0.0:8080 --target gitlab.com:443 [--visibility public|private]\n")
	}
	e := flags.Parse(args)
	if errors.Is(e, flag.ErrHelp) {
		return nil
	}
	if e != nil || flags.NArg() != 0 {
		return netroute.ErrInvalid
	}
	policy := netroute.PublicIPv4
	switch *visibility {
	case "public":
	case "private":
		policy = netroute.Private
	default:
		return netroute.ErrInvalid
	}
	route, e := netroute.NewBoundDialer(*target, policy, nil, nil)
	if e != nil {
		return e
	}
	handler, e := egress.New(ctx, route, egress.Options{MaxTunnels: 64, MaxLifetime: time.Minute})
	if e != nil {
		return e
	}
	defer handler.Close()
	return serve(ctx, *listen, handler)
}
