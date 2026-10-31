// Package netroute pins trusted endpoints to freshly checked numeric destinations, excluding DNS rebinding.
package netroute

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInvalid     = errors.New("invalid fixed network route")
	ErrUnavailable = errors.New("fixed network route unavailable")
)

// Visibility selects a private endpoint (proxy or provider) or public IPv4 transport.
type Visibility uint8

const (
	Private Visibility = iota + 1
	PublicIPv4
)

// Resolver is the explicit DNS port; every answer is checked before any dial.
type Resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

// Dialer receives only the checked numeric address, never an unresolved or caller-selected hostname.
type Dialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

// BoundDialer owns one immutable canonical endpoint and destination policy.
type BoundDialer struct {
	endpoint, host, port string
	visibility           Visibility
	resolver             Resolver
	dialer               Dialer
}

// NewBoundDialer validates operator input; nil ports use standard DNS and a bounded TCP dialer.
func NewBoundDialer(endpoint string, visibility Visibility, resolver Resolver, dialer Dialer) (*BoundDialer, error) {
	host, port, e := net.SplitHostPort(endpoint)
	n, parse := strconv.Atoi(port)
	if e != nil || parse != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port || len(endpoint) > 256 || net.JoinHostPort(host, port) != endpoint || visibility != Private && visibility != PublicIPv4 {
		return nil, ErrInvalid
	}
	if ip, e := netip.ParseAddr(host); e == nil {
		if !permitted(ip, visibility) || ip.String() != host {
			return nil, ErrInvalid
		}
	} else {
		if len(host) > 253 || host != strings.ToLower(host) {
			return nil, ErrInvalid
		}
		for _, label := range strings.Split(host, ".") {
			if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return nil, ErrInvalid
			}
			for _, c := range label {
				if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
					return nil, ErrInvalid
				}
			}
		}
	}
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	if dialer == nil {
		dialer = &net.Dialer{Timeout: 4 * time.Second, KeepAlive: 20 * time.Second}
	}
	return &BoundDialer{endpoint: endpoint, host: host, port: port, visibility: visibility, resolver: resolver, dialer: dialer}, nil
}

// Endpoint is the public non-secret route identity, not a mutable per-request destination.
func (d *BoundDialer) Endpoint() string { return d.endpoint }

// Public IPv4 deliberately excludes private, loopback, link-local, shared, test, benchmark and reserved space.
var excludedIPv4 = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("172.16.0.0/12"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("192.168.0.0/16"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("224.0.0.0/4"), netip.MustParsePrefix("240.0.0.0/4"),
}

func permitted(ip netip.Addr, visibility Visibility) bool {
	if !ip.IsValid() || ip.Zone() != "" {
		return false
	}
	ip = ip.Unmap()
	if visibility == Private {
		return ip.IsPrivate() || ip.IsLoopback()
	}
	if !ip.Is4() || !ip.IsGlobalUnicast() {
		return false
	}
	for _, prefix := range excludedIPv4 {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

// DialContext rejects changed endpoints/mixed DNS sets and prevents a second DNS lookup after validation.
func (d *BoundDialer) DialContext(ctx context.Context, network, endpoint string) (net.Conn, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if network != "tcp" || endpoint != d.endpoint {
		return nil, ErrInvalid
	}
	var addresses []netip.Addr
	if ip, e := netip.ParseAddr(d.host); e == nil {
		addresses = []netip.Addr{ip}
	} else {
		family := "ip"
		if d.visibility == PublicIPv4 {
			family = "ip4"
		}
		var e error
		addresses, e = d.resolver.LookupNetIP(ctx, family, d.host)
		if e != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, ErrUnavailable
		}
	}
	if len(addresses) == 0 || len(addresses) > 8 {
		return nil, ErrUnavailable
	}
	for _, ip := range addresses {
		if !permitted(ip, d.visibility) {
			return nil, ErrUnavailable
		}
	}
	for _, ip := range addresses {
		conn, e := d.dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip.Unmap().String(), d.port))
		if e == nil {
			return conn, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, ErrUnavailable
}
