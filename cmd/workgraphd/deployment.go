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
)

// deploymentCommand is an explicit host-operator use case and does not require application database configuration.
func deploymentCommand(parent context.Context, args []string, output io.Writer) error {
	if len(args) == 0 || args[0] == "--help" {
		_, e := fmt.Fprintln(output, "Usage: gitmorad deployment init | configure | up | down | status | admin --directory <absolute path>")
		return e
	}
	command := args[0]
	flags := flag.NewFlagSet("deployment "+command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() {
		_, _ = fmt.Fprintf(output, "Usage: gitmorad deployment %s --directory <absolute path> [flags]\n", command)
		flags.SetOutput(output)
		flags.PrintDefaults()
		flags.SetOutput(io.Discard)
	}
	root := flags.String("directory", "", "Private absolute installation directory")
	compose, image := "", ""
	var overlays, addOverlays, removeOverlays []string
	port := 0
	if command == "init" {
		flags.StringVar(&compose, "compose", "", "Absolute path to the reviewed compose.yaml")
		flags.StringVar(&image, "image", "gitmora:local", "Prebuilt local application image")
		flags.IntVar(&port, "port", 8080, "Loopback HTTP port for the HTTPS reverse proxy")
		flags.Func("overlay", "Reviewed absolute Compose overlay path; repeat to preserve ordered overlays", func(path string) error { overlays = append(overlays, path); return nil })
	}
	if command == "configure" {
		flags.StringVar(&compose, "compose", "", "New absolute Compose file; omitted preserves current")
		flags.StringVar(&image, "image", "", "New prebuilt image; omitted preserves current")
		flags.Func("add-overlay", "Append a reviewed absolute overlay path", func(p string) error { addOverlays = append(addOverlays, p); return nil })
		flags.Func("remove-overlay", "Remove one exact overlay path", func(p string) error { removeOverlays = append(removeOverlays, p); return nil })
	}
	switch command {
	case "configure", "init", "up", "down", "status", "admin":
	default:
		return deployment.ErrInvalid
	}
	e := flags.Parse(args[1:])
	if errors.Is(e, flag.ErrHelp) {
		return nil
	}
	if e != nil || command != "admin" && flags.NArg() != 0 {
		return deployment.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	if command == "configure" {
		i, e := deployment.Configure(ctx, *root, deployment.Change{Image: image, ComposeFile: compose, AddOverlays: addOverlays, RemoveOverlays: removeOverlays})
		if e != nil {
			return e
		}
		return json.NewEncoder(output).Encode(i)
	}
	if command == "init" {
		i, e := deployment.Create(ctx, *root, compose, port, image, overlays...)
		if e != nil {
			return e
		}
		return json.NewEncoder(output).Encode(i)
	}
	docker, e := deployment.NewDocker()
	if e != nil {
		return e
	}
	service := deployment.NewService(docker)
	switch command {
	case "up":
		e = service.Up(ctx, *root)
	case "down":
		e = service.Down(ctx, *root)
	case "status":
		return service.Status(ctx, *root, output)
	case "admin":
		return service.Admin(ctx, *root, flags.Args(), output)
	}
	if e != nil {
		return e
	}
	return json.NewEncoder(output).Encode(map[string]string{"status": command + "_completed"})
}
