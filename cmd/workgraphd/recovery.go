package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"workgraph.local/platform/internal/jsonwire"
	"workgraph.local/platform/internal/platform/deployment"
	"workgraph.local/platform/internal/platform/postgres"
	"workgraph.local/platform/internal/platform/runtimeconfig"
	"workgraph.local/platform/internal/recovery/application"
	"workgraph.local/platform/internal/recovery/domain"
)

// recoveryCommand is a low-level offline restore boundary: it never serves an authenticated HTTP route.
// The host coordinator must own a stopped, isolated target; stdin contains only a prepared hash commitment.
func recoveryCommand(parent context.Context, args []string, input io.Reader, out io.Writer) error {
	if len(args) > 0 && args[0] == "prepare" {
		return prepareRecoveryCommand(parent, args[1:], out)
	}
	if len(args) == 0 || len(args) == 1 && (args[0] == "--help" || args[0] == "help") {
		_, e := io.WriteString(out, "Usage: gitmorad recovery prepare | metadata | quarantine\nprepare durably creates a private host candidate; quarantine reads its bounded hash-only JSON plan from stdin and requires a stopped isolated restore target.\n")
		return e
	}
	if len(args) != 1 || args[0] != "metadata" && args[0] != "quarantine" {
		return domain.ErrInvalid
	}
	var plan domain.Plan
	if args[0] == "quarantine" {
		raw, e := io.ReadAll(io.LimitReader(input, 8193))
		if e != nil || len(raw) > 8192 || jsonwire.DecodeObject(raw, &plan) != nil {
			return domain.ErrInvalid
		}
		plan, e = plan.Normalize(time.Now())
		if e != nil {
			return e
		}
	}
	dsn, e := runtimeconfig.Secret(runtimeconfig.DatabaseURL)
	if e != nil {
		return domain.ErrUnavailable
	}
	if dsn.Reveal() == "" {
		return domain.ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	pool, e := pgxpool.New(ctx, dsn.Reveal())
	if e != nil {
		return domain.ErrUnavailable
	}
	defer pool.Close()
	if args[0] == "metadata" {
		metadata, e := postgres.ReadRecoveryMetadata(ctx, pool)
		if e != nil {
			return domain.ErrSchema
		}
		return json.NewEncoder(out).Encode(metadata)
	}
	receipt, e := application.NewService(postgres.NewRecoveryStore(pool), time.Now).Quarantine(ctx, plan)
	if e != nil {
		return e
	}
	return json.NewEncoder(out).Encode(receipt)
}

func prepareRecoveryCommand(ctx context.Context, args []string, out io.Writer) error {
	f := flag.NewFlagSet("recovery prepare", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	output := f.String("output", "", "New absolute private file in an existing private directory")
	backup := f.String("backup-id", "", "Exact source backup UUID")
	schema := f.String("schema", "", "Exact application schema fingerprint")
	parent := f.String("parent-id", "", "Source backup's previous recovery UUID, if present")
	name := f.String("name", "Recovery operator", "New metadata-only operator display name")
	ttl := f.Duration("ttl", 24*time.Hour, "Recovery credential lifetime, between 1m and 24h")
	f.Usage = func() {
		_, _ = io.WriteString(out, "Usage: gitmorad recovery prepare --output <new private file> --backup-id <uuid> --schema <digest> [--parent-id <uuid>] [--name <name>] [--ttl 24h]\n")
	}
	e := f.Parse(args)
	if errors.Is(e, flag.ErrHelp) {
		return nil
	}
	if e != nil || f.NArg() != 0 {
		return domain.ErrInvalid
	}
	plan, e := deployment.PrepareRecovery(ctx, *output, domain.Provenance{BackupID: *backup, Schema: *schema, ParentID: *parent}, *name, *ttl)
	if e != nil {
		return e
	}
	return json.NewEncoder(out).Encode(plan)
}
