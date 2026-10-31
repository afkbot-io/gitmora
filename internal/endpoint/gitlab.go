// Package endpoint defines canonical provider endpoint identities without performing network I/O.
package endpoint

import (
	"net"
	"net/url"
	"path"
	"strconv"
	"strings"
)

// GitLabBase preserves existing endpoint identity rules: HTTPS, or loopback HTTP for local fixtures.
func GitLabBase(value string) (string, bool) {
	base, err := url.Parse(value)
	if err != nil || base.Hostname() == "" || base.User != nil || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" || base.Opaque != "" || base.RawPath != "" || strings.Contains(base.Path, "//") || strings.HasSuffix(base.Host, ":") {
		return "", false
	}
	if port := base.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 || strconv.Itoa(number) != port {
			return "", false
		}
	}
	if base.Path != "" && base.Path != "/" && path.Clean(base.Path) != strings.TrimSuffix(base.Path, "/") {
		return "", false
	}
	loopback := base.Hostname() == "localhost"
	if address := net.ParseIP(base.Hostname()); address != nil && address.IsLoopback() {
		loopback = true
	}
	if base.Scheme != "https" && !(base.Scheme == "http" && loopback) {
		return "", false
	}
	return strings.TrimSuffix(base.String(), "/"), true
}
