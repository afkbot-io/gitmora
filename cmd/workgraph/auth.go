package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/collector/domain"
	"workgraph.local/platform/internal/collector/httpclient"
	"workgraph.local/platform/internal/identity/keychain"
	"workgraph.local/platform/internal/identity/native"
	"workgraph.local/platform/internal/identity/nativehttp"
)

func configureAuth(parent context.Context, args []string, output, diagnostic io.Writer) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "help" {
		_, e := fmt.Fprintln(output, "Usage: gitmora auth login | status | logout --server <URL>")
		return e
	}
	command := args[0]
	flags := flag.NewFlagSet("auth "+command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() {
		_, _ = fmt.Fprintf(output, "Usage: gitmora auth %s --server <URL> [flags]\n", command)
		flags.SetOutput(output)
		flags.PrintDefaults()
		flags.SetOutput(io.Discard)
	}
	server := flags.String("server", "", "Exact Gitmora server URL; credentials are scoped to this destination")
	var options native.LoginOptions
	var scopes string
	var noBrowser, localOnly bool
	switch command {
	case "login":
		flags.StringVar(&options.Label, "label", "", "Device label shown during browser consent")
		flags.StringVar(&scopes, "scopes", "", "Comma-separated engineering capabilities; credentials:manage is required")
		flags.DurationVar(&options.TTL, "ttl", 0, "New key lifetime (default 720h, maximum 2160h); zero resumes saved intent")
		flags.BoolVar(&options.Replace, "replace", false, "Revoke/cancel the saved credential before creating a new enrollment")
		flags.BoolVar(&noBrowser, "no-browser", false, "Print the verification URL without opening the browser")
	case "logout":
		flags.BoolVar(&localOnly, "local-only", false, "Forget locally without claiming remote revocation")
	case "status":
	default:
		return native.ErrInvalid
	}
	e := flags.Parse(args[1:])
	if errors.Is(e, flag.ErrHelp) {
		return nil
	}
	if e != nil || flags.NArg() != 0 {
		return native.ErrInvalid
	}
	canonical, e := native.CanonicalServer(*server)
	if e != nil {
		return native.ErrInvalid
	}
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "scopes" {
			options.Scopes = []access.Permission{}
			if scopes != "" {
				for _, p := range strings.Split(scopes, ",") {
					options.Scopes = append(options.Scopes, access.Permission(strings.TrimSpace(p)))
				}
			}
		}
	})
	if command == "login" {
		if e = native.ValidateLoginOptions(options); e != nil {
			return e
		}
	}
	vault, e := keychain.New()
	if e != nil {
		return e
	}
	remote, e := nativehttp.New(canonical)
	if e != nil {
		return e
	}
	service, e := native.NewService(canonical, vault, remote, time.Now, nil)
	if e != nil {
		return e
	}
	limit := 30 * time.Second
	if command == "login" {
		limit = 12 * time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, limit)
	defer cancel()
	switch command {
	case "login":
		r, e := service.Login(ctx, options, func(p native.Prompt) error {
			if e := json.NewEncoder(diagnostic).Encode(p); e != nil {
				return e
			}
			if !noBrowser {
				openVerification(ctx, p.VerificationURI)
			}
			return nil
		})
		if e != nil {
			return e
		}
		return json.NewEncoder(output).Encode(r)
	case "status":
		v, e := service.Inspect(ctx)
		if e != nil {
			return e
		}
		return json.NewEncoder(output).Encode(v)
	default:
		if e := service.Logout(ctx, localOnly); e != nil {
			return e
		}
		state := "revoked_or_already_inactive"
		if localOnly {
			state = "forgotten_locally_remote_revocation_not_verified"
		}
		return json.NewEncoder(output).Encode(map[string]string{"server": canonical, "state": state})
	}
}

// openVerification passes only the public fixed-origin code URL to the platform browser opener.
func openVerification(parent context.Context, url string) {
	program := ""
	switch runtime.GOOS {
	case "darwin":
		program = "/usr/bin/open"
	case "linux":
		var e error
		program, e = exec.LookPath("xdg-open")
		if e != nil {
			return
		}
	default:
		return
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, url)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	_ = cmd.Run()
}

func loadNativeCredential(ctx context.Context, server string) (native.Record, error) {
	v, e := keychain.New()
	if e != nil {
		return native.Record{}, e
	}
	return v.Load(ctx, server)
}

// collectorTokenSource reads at most one native slot per delivery batch and never starts interactive login.
// An explicitly present environment variable owns precedence even when empty or invalid.
func collectorTokenSource(config domain.Config, lookup func(string) (string, bool), load func(context.Context, string) (native.Record, error), now func() time.Time) httpclient.TokenSource {
	return credentialSource(config.ServerURL, config.TokenEnv, lookup, load, now)
}

// credentialSource resolves one explicit environment/system-store choice per native operation.
func credentialSource(serverURL, tokenEnv string, lookup func(string) (string, bool), load func(context.Context, string) (native.Record, error), now func() time.Time) func(context.Context) (string, error) {
	var once sync.Once
	var secret string
	var savedError error
	return func(ctx context.Context) (string, error) {
		once.Do(func() {
			if value, present := lookup(tokenEnv); present {
				if access.CredentialHash(value) == "" {
					savedError = native.ErrUnauthorized
				} else {
					secret = value
				}
				return
			}
			server, e := native.CanonicalServer(serverURL)
			if e != nil {
				savedError = e
				return
			}
			r, e := load(ctx, server)
			if e != nil {
				savedError = e
				return
			}
			if r.Server != server || r.Validate() != nil {
				savedError = native.ErrInvalid
				return
			}
			if r.Pending != nil || r.Account == nil || !r.Account.ExpiresAt.After(now()) {
				savedError = native.ErrUnauthorized
				return
			}
			secret = r.Secret
		})
		return secret, savedError
	}
}
