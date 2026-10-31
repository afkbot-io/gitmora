package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/term"
	"workgraph.local/platform/internal/identifier"
	identity "workgraph.local/platform/internal/identity/domain"
	"workgraph.local/platform/internal/jsonwire"
	"workgraph.local/platform/internal/platform/deployment"
	"workgraph.local/platform/internal/platform/identityhost"
	"workgraph.local/platform/internal/platform/postgres"
	"workgraph.local/platform/internal/platform/runtimeconfig"
)

func usersCommand(parent context.Context, args []string, out io.Writer) error {
	const usage = "Usage: gitmorad users create --directory <private identity directory> --username <login> --name <display name> [--installation <application state directory>] [--subject <existing provider UUID>]\nRequires database-owner configuration; password is entered twice without echo. No project/operator rights are granted."
	if len(args) == 0 || args[0] == "--help" {
		_, e := fmt.Fprintln(out, usage)
		return e
	}
	if args[0] == "bind-subject" {
		return bindUserCommand(parent, args[1:], out)
	}
	if args[0] != "create" {
		return errors.New(usage)
	}
	flags := flag.NewFlagSet("users create", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	root := flags.String("directory", "", "Private hosted identity directory")
	applicationRoot := flags.String("installation", "", "Private application installation; binds through its existing API container")
	input := identityhost.UserInput{}
	flags.StringVar(&input.ID, "subject", "", "Recover an existing exact provider subject without resetting its password")
	flags.StringVar(&input.Username, "username", "", "Login name")
	flags.StringVar(&input.DisplayName, "name", "", "Display name")
	flags.Usage = func() {}
	if e := flags.Parse(args[1:]); errors.Is(e, flag.ErrHelp) {
		_, err := fmt.Fprintln(out, usage)
		return err
	} else if e != nil || flags.NArg() != 0 {
		return identityhost.ErrInvalid
	}
	// Validate before touching private state or prompting; the placeholder is never used as a credential.
	input.Password = []byte("validation-placeholder")
	if e := input.Validate(); e != nil {
		return e
	}
	input.Password = nil
	installation, e := identityhost.Load(*root)
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	var pool *pgxpool.Pool
	var engine deployment.Executor
	if *applicationRoot != "" {
		if _, e = deployment.Load(*applicationRoot); e != nil {
			return e
		}
		engine, e = deployment.NewDocker()
		if e != nil {
			return e
		}
	} else {
		pool, e = usersDatabase(ctx)
		if e != nil {
			return e
		}
		defer pool.Close()
	}

	if input.ID == "" {
		input.Password, e = readUserPassword()
		if e != nil {
			return e
		}
		defer clear(input.Password)
	}
	if e = input.Validate(); e != nil {
		return e
	}

	who, e := identityhost.NewLocalAdministrator().CreateUser(ctx, installation, input)
	if e != nil {
		return fmt.Errorf("provider account creation incomplete; inspect the existing username before retrying (no password was reset): %w", e)
	}
	if *applicationRoot != "" {
		var bound bytes.Buffer
		e = deployment.NewService(engine).Admin(ctx, *applicationRoot, []string{"users", "bind-subject", "--issuer", who.Issuer, "--subject", who.Subject, "--name", who.DisplayName}, &bound)
		if e != nil {
			return fmt.Errorf("account binding incomplete; retry with --subject %s: application administration unavailable", who.Subject)
		}
		receipt, e := decodeUserBinding(bound.Bytes(), who.Subject, input.Username)
		if e != nil {
			return fmt.Errorf("account binding receipt uncertain; retry with --subject %s: inspect selected application state", who.Subject)
		}
		return json.NewEncoder(out).Encode(receipt)
	}

	b, e := postgres.NewIdentityStore(pool).CreateAccountOffline(ctx, who)
	if e != nil {
		return fmt.Errorf("account binding incomplete; retry with --subject %s: account storage unavailable", who.Subject)
	}
	return json.NewEncoder(out).Encode(createdUserReceipt{"created", b.UserID, b.Subject, input.Username})
}

type createdUserReceipt struct {
	Status   string `json:"status"`
	UserID   string `json:"user_id"`
	Subject  string `json:"subject"`
	Username string `json:"username"`
}

// The inner database-owner command reports binding only; the host adds the verified provider username.
func decodeUserBinding(raw []byte, subject, username string) (createdUserReceipt, error) {
	var bound struct {
		Status  string `json:"status"`
		UserID  string `json:"user_id"`
		Subject string `json:"subject"`
	}
	if len(raw) > 4096 || jsonwire.DecodeObject(raw, &bound) != nil || bound.Status != "created" || !identifier.Valid(bound.UserID) || bound.Subject != subject {
		return createdUserReceipt{}, identity.ErrInvalid
	}
	return createdUserReceipt{bound.Status, bound.UserID, bound.Subject, username}, nil
}

func readUserPassword() ([]byte, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return nil, errors.New("a terminal is required for hidden password entry (use ssh -t)")
	}
	state, e := term.GetState(fd)
	if e != nil {
		return nil, errors.New("terminal unavailable")
	}
	defer term.Restore(fd, state)
	fmt.Fprint(os.Stderr, "Password (at least 12 characters): ")
	first, e := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if e != nil {
		return nil, errors.New("password input unavailable")
	}
	fmt.Fprint(os.Stderr, "Repeat password: ")
	second, e := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	defer clear(second)
	if e != nil || !bytes.Equal(first, second) {
		clear(first)
		return nil, errors.New("passwords do not match")
	}
	return first, nil
}

func usersDatabase(ctx context.Context) (*pgxpool.Pool, error) {
	secret, e := runtimeconfig.Secret(runtimeconfig.DatabaseURL)
	if e != nil {
		return nil, e
	}
	if secret.Reveal() == "" {
		return nil, errors.New("WORKGRAPH_DATABASE_URL or WORKGRAPH_DATABASE_URL_FILE is required")
	}
	cfg, e := pgxpool.ParseConfig(secret.Reveal())
	if e != nil {
		return nil, errors.New("invalid database configuration")
	}
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		return nil, errors.New("database unavailable")
	}
	if e = postgres.VerifySchema(ctx, pool); e != nil {
		pool.Close()
		return nil, errors.New("current database schema is required")
	}
	return pool, nil
}

// bindUserCommand is a database-owner recovery boundary for the hosted operator command, never an HTTP route.
func bindUserCommand(ctx context.Context, args []string, out io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	flags := flag.NewFlagSet("users bind-subject", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var who identity.Identity
	flags.StringVar(&who.Issuer, "issuer", "", "Expected configured provider issuer")
	flags.StringVar(&who.Subject, "subject", "", "Exact provider subject")
	flags.StringVar(&who.DisplayName, "name", "", "Display name")
	if e := flags.Parse(args); e != nil || flags.NArg() != 0 || who.Issuer == "" || who.Issuer != os.Getenv("WORKGRAPH_OIDC_ISSUER") || !identity.Subject(who.Subject) || who.DisplayName == "" {
		return identity.ErrInvalid
	}
	pool, e := usersDatabase(ctx)
	if e != nil {
		return e
	}
	defer pool.Close()
	b, e := postgres.NewIdentityStore(pool).CreateAccountOffline(ctx, who)
	if e != nil {
		return errors.New("account storage unavailable")
	}
	return json.NewEncoder(out).Encode(map[string]string{"status": "created", "user_id": b.UserID, "subject": b.Subject})
}
