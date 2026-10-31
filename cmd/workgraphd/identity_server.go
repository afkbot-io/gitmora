package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"workgraph.local/platform/internal/platform/deployment"
	"workgraph.local/platform/internal/platform/identityhost"
	"workgraph.local/platform/internal/platform/runtimeconfig"
)

func identityServerCommand(parent context.Context, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] == "--help" {
		_, err := fmt.Fprintln(out, "Usage: gitmorad identity-server init | up | down | status | finalize | configure --directory <private absolute path>\ninit writes new private provider/bootstrap files. finalize must succeed before RP activation; no credentials are printed.")
		return err
	}
	command := args[0]
	flags := flag.NewFlagSet("identity-server "+command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	root := flags.String("directory", "", "Private identity installation directory")
	var profile identityhost.Profile
	var compose string
	switch command {
	case "init":
		flags.StringVar(&compose, "compose", "", "Absolute identity Compose profile")
		flags.StringVar(&profile.PublicURL, "public-url", "", "Canonical HTTPS provider base; /auth supports the same Gitmora origin (HTTP only for loopback tests)")
		flags.StringVar(&profile.ApplicationURL, "application-url", "", "Canonical Gitmora root origin")
		flags.StringVar(&profile.Realm, "realm", "gitmora", "Application realm; master is forbidden")
		flags.StringVar(&profile.ClientID, "client-id", "gitmora", "Confidential OIDC client identifier")
		flags.IntVar(&profile.HTTPPort, "port", 18081, "Loopback-only identity gateway port")
		flags.StringVar(&profile.IdentityImage, "identity-image", "", "Prebuilt optimized identity image; default selects 26.8.1-auth or 26.8.1-root from the provider path")
		flags.StringVar(&profile.GatewayImage, "gateway-image", "gitmora:local", "Explicit prebuilt Gitmora gateway image")
	case "up", "down", "status", "finalize", "configure":
	default:
		return identityhost.ErrInvalid
	}
	flags.Usage = func() {
		_, _ = fmt.Fprintf(out, "Usage: gitmorad identity-server %s [flags]\n", command)
		flags.SetOutput(out)
		flags.PrintDefaults()
		flags.SetOutput(io.Discard)
	}
	if err := flags.Parse(args[1:]); errors.Is(err, flag.ErrHelp) {
		return nil
	} else if err != nil || flags.NArg() != 0 {
		return identityhost.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(parent, 4*time.Minute)
	defer cancel()
	if command == "init" {
		if profile.IdentityImage == "" {
			profile.IdentityImage = "gitmora-identity:26.8.1-root"
			if profile.Prefix() == "/auth" {
				profile.IdentityImage = "gitmora-identity:26.8.1-auth"
			}
		}
		i, err := identityhost.Create(ctx, *root, compose, profile)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(i)
	}
	if command == "configure" {
		enabled, e := runtimeconfig.RegistrationEnabled()
		if e != nil {
			return e
		}
		i, e := identityhost.Load(*root)
		if e != nil {
			return e
		}
		if e = identityhost.NewLocalAdministrator().Configure(ctx, i, enabled); e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(map[string]any{"status": "configured", "registration_enabled": enabled})
	}

	engine, err := deployment.NewDocker()
	if err != nil {
		return err
	}
	service := identityhost.NewService(engine, identityhost.NewLocalAdministrator())
	switch command {
	case "up":
		err = service.Up(ctx, *root)
		if err == nil {
			i, e := identityhost.Load(*root)
			if e != nil {
				return e
			}
			if i.Phase == "active" {
				enabled, e := runtimeconfig.RegistrationEnabled()
				if e != nil {
					return e
				}
				err = identityhost.NewLocalAdministrator().Configure(ctx, i, enabled)
			}
		}
	case "down":
		err = service.Down(ctx, *root)
	case "status":
		return service.Status(ctx, *root, out)
	case "finalize":
		i, e := service.Finalize(ctx, *root)
		if e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(i)
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(map[string]string{"status": command + "_completed"})
}
