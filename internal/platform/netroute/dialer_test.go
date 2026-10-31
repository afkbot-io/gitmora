package netroute_test

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"

	"workgraph.local/platform/internal/platform/netroute"
)

type resolver struct {
	addresses []netip.Addr
	calls     int
}

func (r *resolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	r.calls++
	return r.addresses, nil
}

type dialer struct{ addresses []string }

func (d *dialer) DialContext(_ context.Context, network, address string) (net.Conn, error) {
	d.addresses = append(d.addresses, address)
	return nil, errors.New("synthetic private backend error")
}

func TestPublicRouteRejectsEveryNonPublicAnswerBeforeDialAndPinsNumericAddresses(t *testing.T) {
	for _, ip := range []string{"0.0.0.0", "10.0.0.1", "100.64.0.1", "127.0.0.1", "169.254.169.254", "172.16.0.1", "192.168.0.1", "192.0.0.9", "198.18.0.1", "198.51.100.1", "203.0.113.1", "224.0.0.1", "255.255.255.255", "::1", "fc00::1", "64:ff9b::a00:1"} {
		t.Run(ip, func(t *testing.T) {
			r := &resolver{addresses: []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr(ip)}}
			d := &dialer{}
			bound, e := netroute.NewBoundDialer("gitlab.com:443", netroute.PublicIPv4, r, d)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = bound.DialContext(context.Background(), "tcp", "gitlab.com:443"); e == nil || len(d.addresses) != 0 || strings.Contains(e.Error(), "private") {
				t.Fatal("unsafe DNS set dialed or leaked")
			}
		})
	}
	r := &resolver{addresses: []netip.Addr{netip.MustParseAddr("172.65.251.78")}}
	d := &dialer{}
	bound, e := netroute.NewBoundDialer("gitlab.com:443", netroute.PublicIPv4, r, d)
	if e != nil {
		t.Fatal(e)
	}
	_, e = bound.DialContext(context.Background(), "tcp", "gitlab.com:443")
	if e == nil || len(d.addresses) != 1 || d.addresses[0] != "172.65.251.78:443" || strings.Contains(e.Error(), "private") {
		t.Fatal("route did not pin the checked address")
	}
	before := r.calls
	for _, target := range []string{"example.com:443", "gitlab.com:80", "gitlab.com.:443", "GITLAB.com:443"} {
		if _, e = bound.DialContext(context.Background(), "tcp", target); e == nil || r.calls != before {
			t.Fatal("caller changed fixed route")
		}
	}
}

func TestPrivateProxyRouteRejectsPublicAndLinkLocalDestinations(t *testing.T) {
	for _, ip := range []string{"1.1.1.1", "169.254.169.254", "fe80::1", "0.0.0.0"} {
		r := &resolver{addresses: []netip.Addr{netip.MustParseAddr(ip)}}
		d := &dialer{}
		bound, e := netroute.NewBoundDialer("gitlab-egress:8080", netroute.Private, r, d)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = bound.DialContext(context.Background(), "tcp", "gitlab-egress:8080"); e == nil || len(d.addresses) != 0 {
			t.Fatal("proxy escaped private route")
		}
	}
	for _, endpoint := range []string{"host:0", "host:08080", "host:65536", "http://host:8080", "user@host:8080", "bad_host:8080"} {
		if _, e := netroute.NewBoundDialer(endpoint, netroute.Private, &resolver{}, &dialer{}); e == nil {
			t.Fatal("ambiguous route accepted")
		}
	}
}
