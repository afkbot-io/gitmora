package domain

import (
	"net"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"

	"workgraph.local/platform/internal/identifier"
)

// Config is credential-free repository delivery configuration stored in the Git common directory.
type Config struct {
	Disabled       bool     `json:"disabled,omitempty"`
	DisabledAgents []string `json:"disabled_agents,omitempty"`
	Version        int      `json:"version"`
	RepositoryID   string   `json:"repository_id"`
	ServerURL      string   `json:"server_url"`
	Remote         string   `json:"remote"`
	RemoteURL      string   `json:"remote_url"`
	TokenEnv       string   `json:"token_env"`
}

var envName = regexp.MustCompile(`^WORKGRAPH_[A-Z0-9_]*TOKEN$`)
var remoteName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/-]{0,99}$`)

// NormalizeConfig rejects credential-bearing URLs and insecure non-loopback API endpoints.
func NormalizeConfig(c Config) (Config, error) {
	if c.Version != 1 || !identifier.Valid(c.RepositoryID) || !remoteName.MatchString(c.Remote) || !envName.MatchString(c.TokenEnv) || !boundedID(c.RemoteURL, 2048) {
		return Config{}, ErrInvalid
	}
	if len(c.DisabledAgents) > 16 {
		return Config{}, ErrInvalid
	}
	c.DisabledAgents = slices.Clone(c.DisabledAgents)
	for _, agent := range c.DisabledAgents {
		if !ValidAgentName(agent) {
			return Config{}, ErrInvalid
		}
	}
	slices.Sort(c.DisabledAgents)
	c.DisabledAgents = slices.Compact(c.DisabledAgents)
	if len(c.DisabledAgents) == 0 {
		c.DisabledAgents = nil
	}

	base, err := url.Parse(c.ServerURL)
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" || base.Opaque != "" || base.RawPath != "" {
		return Config{}, ErrInvalid
	}
	if base.Path != "" && base.Path != "/" && path.Clean(base.Path) != strings.TrimSuffix(base.Path, "/") {
		return Config{}, ErrInvalid
	}
	loopback := base.Hostname() == "localhost"
	if address := net.ParseIP(base.Hostname()); address != nil && address.IsLoopback() {
		loopback = true
	}
	if base.Scheme != "https" && !(base.Scheme == "http" && loopback) {
		return Config{}, ErrInvalid
	}
	remote, err := url.Parse(c.RemoteURL)
	// SCP-style SSH remotes are not URLs. A malformed explicit URL must never bypass credential checks.
	if err != nil && strings.Contains(c.RemoteURL, "://") {
		return Config{}, ErrInvalid
	}
	if err == nil && remote.Scheme != "" {
		_, password := remote.User.Password()
		if remote.RawQuery != "" || remote.ForceQuery || remote.Fragment != "" || password || remote.User != nil && remote.Scheme != "ssh" {
			return Config{}, ErrInvalid
		}
	}
	c.ServerURL = strings.TrimSuffix(base.String(), "/")
	return c, nil
}

// AllowsAgent is local collection/delivery consent, independent of server authorization.
func (c Config) AllowsAgent(agent string) bool {
	return !c.Disabled && !slices.Contains(c.DisabledAgents, agent)
}

// SameConnection protects immutable destinations while allowing independent local consent changes.
func (c Config) SameConnection(other Config) bool {
	return c.Version == other.Version && c.RepositoryID == other.RepositoryID && c.ServerURL == other.ServerURL && c.Remote == other.Remote && c.RemoteURL == other.RemoteURL && c.TokenEnv == other.TokenEnv
}
