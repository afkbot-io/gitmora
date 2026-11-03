// Command workgraphd runs the Workgraph API and privileged installation operations.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/bootstrap"
	"workgraph.local/platform/internal/capture/application"
	contextapp "workgraph.local/platform/internal/contextpack/application"
	directoryapp "workgraph.local/platform/internal/directory/application"
	forgeapp "workgraph.local/platform/internal/forge/application"
	"workgraph.local/platform/internal/forge/github"
	"workgraph.local/platform/internal/forge/gitlab"
	identityapp "workgraph.local/platform/internal/identity/application"
	identitydomain "workgraph.local/platform/internal/identity/domain"
	identityoidc "workgraph.local/platform/internal/identity/oidc"
	"workgraph.local/platform/internal/jsonwire"
	"workgraph.local/platform/internal/platform/postgres"
	"workgraph.local/platform/internal/platform/runtimeconfig"
	"workgraph.local/platform/internal/platform/secretbox"
	retrievalapp "workgraph.local/platform/internal/retrieval/application"
	"workgraph.local/platform/internal/transport/httpapi"
	"workgraph.local/platform/internal/transport/webui"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		slog.Error("workgraph command failed", "error", err.Error())
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, output io.Writer) error {
	const usage = "Usage: gitmorad serve | worker | embedding-worker | migrate | init-admin | bootstrap | issue-token | grant-operator | healthcheck | gateway | deployment | identity-server | users | recovery | egress | version"
	if len(args) == 0 {
		return errors.New(usage)
	}
	if args[0] == "--help" || args[0] == "help" {
		_, e := fmt.Fprintln(output, usage)
		return e
	}
	command := args[0]
	if command == "version" || command == "--version" {
		return serverVersion(args[1:], output)
	}
	if command == "users" {
		return usersCommand(ctx, args[1:], output)
	}
	if command == "egress" {
		return egressCommand(ctx, args[1:], output)
	}
	if command == "recovery" {
		return recoveryCommand(ctx, args[1:], os.Stdin, output)
	}
	if command == "gateway" {
		return gatewayCommand(ctx, args[1:], output)
	}
	if command == "deployment" {
		return deploymentCommand(ctx, args[1:], output)
	}
	if command == "identity-server" {
		return identityServerCommand(ctx, args[1:], output)
	}
	if command == "healthcheck" {
		return healthcheck(ctx, args[1:], output)
	}
	if command != "serve" && command != "worker" && command != "embedding-worker" && command != "migrate" && command != "init-admin" && command != "bootstrap" && command != "issue-token" && command != "grant-operator" {
		return errors.New("unknown command; run gitmorad --help")
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() {
		_, _ = fmt.Fprintf(output, "Usage: gitmorad %s [flags]\n", command)
		flags.SetOutput(output)
		flags.PrintDefaults()
		flags.SetOutput(io.Discard)
	}
	var listen string
	var once bool
	var tokenUser, tokenLabel, tokenScopes string
	var tokenTTL time.Duration
	input := bootstrap.Input{}
	if command == "init-admin" {
		flags.StringVar(&input.DisplayName, "name", "Administrator", "Initial installation operator name")
		flags.DurationVar(&tokenTTL, "ttl", 24*time.Hour, "Initial operator credential lifetime; at most 2160h")
	}
	if command == "serve" {
		flags.StringVar(&listen, "listen", "127.0.0.1:8080", "HTTP listen address")
	}
	if command == "worker" || command == "embedding-worker" {
		flags.BoolVar(&once, "once", false, "Process one due unit per configured worker and exit")
	}
	if command == "grant-operator" {
		flags.StringVar(&tokenUser, "user-id", "", "Existing enabled Workgraph user UUID")
	}
	if command == "issue-token" {
		flags.StringVar(&tokenUser, "user-id", "", "Existing Workgraph user UUID")
		flags.StringVar(&tokenLabel, "label", "Operator issued", "Credential label")
		flags.StringVar(&tokenScopes, "scopes", "capture:read,capture:write,integration:read,integration:manage", "Comma-separated capabilities")
		flags.DurationVar(&tokenTTL, "ttl", 90*24*time.Hour, "Credential lifetime, at most 365 days")
	}
	if command == "bootstrap" {
		flags.StringVar(&input.DisplayName, "name", "Administrator", "Initial administrator name")
		flags.StringVar(&input.ProjectName, "project", "", "Initial project name")
		flags.StringVar(&input.RepositoryName, "repository", "", "Repository display name")
		flags.StringVar(&input.GitLabURL, "gitlab-url", "", "GitLab base URL")
		flags.Int64Var(&input.GitLabProjectID, "gitlab-project-id", 0, "Immutable GitLab project ID")
	}
	parseError := flags.Parse(args[1:])
	if errors.Is(parseError, flag.ErrHelp) {
		return nil
	}
	if parseError != nil || flags.NArg() != 0 {
		return errors.New("invalid command flags")
	}
	deploymentDSN, err := runtimeconfig.Secret(runtimeconfig.DatabaseURL)
	if err != nil {
		return err
	}
	dsn := deploymentDSN.Reveal()
	if dsn == "" {
		return errors.New("WORKGRAPH_DATABASE_URL is required")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return errors.New("cannot open database; verify WORKGRAPH_DATABASE_URL")
	}
	defer pool.Close()
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := pool.Ping(startup); err != nil {
		return errors.New("database unavailable; verify connection and credentials")
	}
	if command == "migrate" || command == "bootstrap" || command == "init-admin" {
		if err := postgres.Migrate(startup, pool); err != nil {
			return fmt.Errorf("database migration failed: %w", err)
		}
	}
	switch command {
	case "init-admin":
		result, e := bootstrap.NewAdminService(postgres.NewBootstrapStore(pool), time.Now).Initialize(startup, input.DisplayName, tokenTTL)
		if e != nil {
			return e
		}
		return json.NewEncoder(output).Encode(struct {
			UserID    string    `json:"user_id"`
			Token     string    `json:"token"`
			ExpiresAt time.Time `json:"expires_at"`
		}{result.UserID, result.Credential.Secret, result.Credential.Record.ExpiresAt})
	case "migrate":
		_, err := fmt.Fprintln(output, "Migrations applied")
		return err
	case "bootstrap":
		result, err := bootstrap.NewService(postgres.NewBootstrapStore(pool), time.Now).Initialize(startup, input)
		if err != nil {
			return err
		}
		// The initial bearer secret is intentionally printed once to the operator, never to logs.
		return json.NewEncoder(output).Encode(struct {
			UserID       string    `json:"user_id"`
			ProjectID    string    `json:"project_id"`
			RepositoryID string    `json:"repository_id"`
			Token        string    `json:"token"`
			ExpiresAt    time.Time `json:"expires_at"`
		}{result.UserID, result.ProjectID, result.RepositoryID, result.Credential.Secret, result.Credential.Record.ExpiresAt})
	case "grant-operator":
		if err := postgres.VerifySchema(startup, pool); err != nil {
			return errors.New("database schema is not ready; run workgraphd migrate")
		}
		if err := postgres.NewDirectoryStore(pool).GrantOperatorOffline(startup, tokenUser); err != nil {
			return err
		}
		_, err := fmt.Fprintln(output, "Installation operator role granted; issue explicit management scopes separately")
		return err
	case "issue-token":
		if err := postgres.VerifySchema(startup, pool); err != nil {
			return errors.New("database schema is not ready; run workgraphd migrate")
		}
		scopes := []access.Permission{}
		for _, scope := range strings.Split(tokenScopes, ",") {
			scopes = append(scopes, access.Permission(strings.TrimSpace(scope)))
		}
		issued, err := access.IssueCredential(tokenUser, tokenLabel, scopes, time.Now(), tokenTTL)
		if err != nil {
			return errors.New("invalid credential parameters")
		}
		if err := postgres.NewCredentialStore(pool).Save(startup, issued.Record); err != nil {
			return errors.New("cannot issue credential for this user")
		}
		return json.NewEncoder(output).Encode(struct {
			Token     string    `json:"token"`
			TokenID   string    `json:"token_id"`
			ExpiresAt time.Time `json:"expires_at"`
		}{issued.Secret, issued.Record.ID, issued.Record.ExpiresAt})
	case "embedding-worker":
		if err := postgres.VerifySchema(startup, pool); err != nil {
			return err
		}
		embeddings, err := embeddingClient(startup, pool)
		if err != nil {
			return err
		}
		if embeddings == nil {
			return errors.New("local embeddings are not configured")
		}
		return runWorker(ctx, once, output, retrievalapp.NewEmbeddingIndexer(postgres.NewRetrievalStore(pool), embeddings))
	case "worker":
		if err := postgres.VerifySchema(startup, pool); err != nil {
			return errors.New("database schema is not ready; run workgraphd migrate")
		}
		forge, code, err := forgeServices(pool)
		if err != nil {
			return err
		}
		services := []reconciler{}
		if forge != nil {
			services = append(services, forge, code)
		}
		services = append(services, retrievalapp.NewIndexer(postgres.NewRetrievalStore(pool)))
		return runWorker(ctx, once, output, services...)
	case "serve":
		if err := postgres.VerifySchema(startup, pool); err != nil {
			return errors.New("database schema is not ready; run workgraphd migrate")
		}
		forge, code, err := forgeServices(pool)
		if err != nil {
			return err
		}
		var forgePort httpapi.ForgeService
		var codePort httpapi.CodeService
		if forge != nil {
			forgePort = forge
			codePort = code
		}
		origins, err := mcpAllowedOrigins()
		if err != nil {
			return err
		}
		publicOrigin, err := mcpPublicOrigin()
		if err != nil {
			return err
		}
		identities, err := identityService(startup, pool)
		if err != nil {
			return err
		}
		var identityPort httpapi.IdentityService
		var cliEnrollment httpapi.CLIEnrollmentService
		if identities != nil {
			identityPort = identities
			cliEnrollment = identityapp.NewCLIService(postgres.NewIdentityStore(pool), identities.Config())
		}
		githubClient, err := configuredGitHub()
		if err != nil {
			return err
		}
		directoryService := directoryapp.NewService(postgres.NewDirectoryStore(pool), time.Now)
		resolver := repositoryResolver{github: githubClient}
		if raw := os.Getenv("WORKGRAPH_GITLAB_ALLOWLIST"); raw != "" {
			resolver.gitlab, err = gitlab.NewWithProxy(strings.Split(raw, ","), os.Getenv("WORKGRAPH_GITLAB_PROXY_URL"))
			if err != nil {
				return err
			}
		}
		directoryService.WithRepositoryResolver(resolver)
		embeddings, err := embeddingClient(startup, pool)
		if err != nil {
			return err
		}
		retrievalService := retrievalapp.NewService(postgres.NewRetrievalStore(pool))
		if embeddings != nil {
			retrievalService.WithEmbeddings(embeddings)
		}
		handler := httpapi.NewHandler(httpapi.Config{Captures: application.NewService(postgres.NewCaptureStore(pool)), Authenticator: postgres.NewCredentialStore(pool), Ready: func(ctx context.Context) error { return postgres.VerifySchema(ctx, pool) }, Forge: forgePort, Code: codePort, Retrieval: retrievalService, ContextPacks: contextapp.NewService(postgres.NewContextStore(pool), retrievalService), ContextBrowser: contextapp.NewBrowser(postgres.NewContextStore(pool)), ContextResolver: contextapp.NewResolver(postgres.NewContextStore(pool)), Directory: directoryService, Identity: identityPort, CLIEnrollment: cliEnrollment, MCPAllowedOrigins: origins, MCPPublicOrigin: publicOrigin, WebUI: webui.Embedded()})
		return serve(ctx, listen, handler)
	default:
		return errors.New("unsupported command")
	}
}

// identityService configures a lazy local provider; bearer capture never waits for provider discovery.
func identityService(ctx context.Context, pool *pgxpool.Pool) (*identityapp.Service, error) {
	registration, err := runtimeconfig.RegistrationEnabled()
	if err != nil {
		return nil, err
	}
	store := postgres.NewIdentityStore(pool)
	clientSecret, err := runtimeconfig.Secret(runtimeconfig.OIDCClientSecret)
	if err != nil {
		return nil, err
	}
	issuer, client, secret := os.Getenv("WORKGRAPH_OIDC_ISSUER"), os.Getenv("WORKGRAPH_OIDC_CLIENT_ID"), clientSecret.Reveal()
	if issuer == "" && client == "" && secret == "" {
		if registration || os.Getenv("WORKGRAPH_OIDC_AUTH_STYLE") != "" || os.Getenv("WORKGRAPH_OIDC_SESSION_SECONDS") != "" {
			return nil, errors.New("OIDC issuer/client configuration is required")
		}
		if err := store.Activate(ctx, ""); err != nil {
			return nil, err
		}
		return nil, nil
	}
	style := os.Getenv("WORKGRAPH_OIDC_AUTH_STYLE")
	if style == "" {
		style = "basic"
	}
	seconds := 28800
	if value := os.Getenv("WORKGRAPH_OIDC_SESSION_SECONDS"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil {
			return nil, errors.New("invalid OIDC session duration")
		}
		seconds = n
	}
	config, err := identitydomain.NormalizeConfig(identitydomain.Config{RegistrationEnabled: registration, Issuer: issuer, ClientID: client, PublicURL: os.Getenv("WORKGRAPH_PUBLIC_URL"), AuthStyle: style, SessionSeconds: seconds, Policy: identitydomain.Policy})
	if err != nil {
		return nil, errors.New("invalid OIDC/public origin configuration")
	}
	provider, err := identityoidc.New(config, secret)
	if err != nil {
		return nil, errors.New("invalid local OIDC client configuration")
	}
	if err = store.ActivatePolicy(ctx, config.Fingerprint(), registration); err != nil {
		return nil, err
	}
	return identityapp.NewService(store, provider, config, time.Now), nil
}

// configuredGitHub activates one fixed read-only API route; development overrides are loopback-only.
func configuredGitHub() (*github.Router, error) {
	enabled := os.Getenv("WORKGRAPH_GITHUB_ENABLED")
	base := os.Getenv("WORKGRAPH_GITHUB_API_URL")
	proxy := os.Getenv("WORKGRAPH_GITHUB_PROXY_URL")
	if enabled != "true" {
		if (enabled != "" && enabled != "false") || base != "" || proxy != "" || os.Getenv("WORKGRAPH_GITHUB_ALLOWLIST") != "" || os.Getenv("WORKGRAPH_GITHUB_PRIVATE") != "" || os.Getenv("WORKGRAPH_GITHUB_ENTERPRISE_PROXY_URL") != "" {
			return nil, errors.New("invalid GitHub configuration")
		}
		return nil, nil
	}
	if base == "" {
		base = "https://api.github.com"
	}
	var custom []string
	if raw := os.Getenv("WORKGRAPH_GITHUB_ALLOWLIST"); raw != "" {
		custom = strings.Split(raw, ",")
	}
	private := os.Getenv("WORKGRAPH_GITHUB_PRIVATE")
	if private != "" && private != "true" && private != "false" {
		return nil, errors.New("invalid GitHub private routing configuration")
	}
	client, err := github.NewRouterWithEnterpriseProxy(base, proxy, os.Getenv("WORKGRAPH_GITHUB_ENTERPRISE_PROXY_URL"), custom, private == "true")
	if err != nil {
		return nil, errors.New("invalid GitHub API or private proxy configuration")
	}
	return client, nil
}

func forgeServices(pool *pgxpool.Pool) (*forgeapp.Service, *forgeapp.CodeService, error) {
	keys, err := runtimeconfig.Secret(runtimeconfig.EncryptionKeys)
	if err != nil {
		return nil, nil, err
	}
	keyring := keys.Reveal()
	githubClient, err := configuredGitHub()
	if err != nil {
		return nil, nil, err
	}
	allowed := os.Getenv("WORKGRAPH_GITLAB_ALLOWLIST")
	proxy := os.Getenv("WORKGRAPH_GITLAB_PROXY_URL")
	if proxy != "" && allowed == "" {
		return nil, nil, errors.New("allowed GitLab endpoints are required for the explicit proxy")
	}
	active := os.Getenv("WORKGRAPH_ENCRYPTION_ACTIVE_KEY")
	if allowed == "" && githubClient == nil {
		// A generated keyring does not enable a source. Fresh installations
		// may start without a forge, but malformed supplied keys still fail.
		if keyring != "" || active != "" {
			if _, err := configuredVault(); err != nil {
				return nil, nil, err
			}
		}
		return nil, nil, nil
	}
	box, err := configuredVault()
	if err != nil {
		return nil, nil, err
	}

	providers := forgeapp.Providers{}
	if allowed != "" {
		bases := strings.Split(allowed, ",")
		for i := range bases {
			bases[i] = strings.TrimSpace(bases[i])
		}
		provider, err := gitlab.NewWithProxy(bases, proxy)
		if err != nil {
			return nil, nil, errors.New("invalid allowed GitLab endpoint or explicit proxy")
		}
		providers.GitLab = provider
	}
	if githubClient != nil {
		providers.GitHub = githubClient
	}
	return forgeapp.NewService(postgres.NewForgeStore(pool), box, providers, time.Now), forgeapp.NewCodeService(postgres.NewForgeStore(pool), box, providers), nil
}

// reconciler is shared orchestration, while each bounded context retains its own durable leases.
type reconciler interface {
	ReconcileOnce(context.Context) (bool, error)
}

func runWorker(ctx context.Context, once bool, output io.Writer, services ...reconciler) error {
	for {
		worked, failed := false, false
		for _, service := range services {
			didWork, err := service.ReconcileOnce(ctx)
			worked = worked || didWork
			failed = failed || err != nil
			if ctx.Err() != nil {
				return nil
			}
		}
		if once {
			if failed {
				return errors.New("background reconciliation failed; inspect durable job health")
			}
			return json.NewEncoder(output).Encode(map[string]bool{"worked": worked})
		}
		if failed {
			slog.Warn("background reconciliation needs attention")
		}
		if worked && !failed {
			continue
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func configuredVault() (*secretbox.Box, error) {
	keyring, err := runtimeconfig.Secret(runtimeconfig.EncryptionKeys)
	if err != nil {
		return nil, err
	}
	var encoded map[string]string
	if jsonwire.DecodeExternalObject([]byte(keyring.Reveal()), &encoded) != nil || len(encoded) == 0 {
		return nil, errors.New("invalid provider encryption configuration")
	}
	keys := map[string][]byte{}
	for id, value := range encoded {
		key, err := base64.StdEncoding.Strict().DecodeString(value)
		if err != nil {
			return nil, errors.New("invalid provider encryption key")
		}
		keys[id] = key
	}
	box, err := secretbox.New(os.Getenv("WORKGRAPH_ENCRYPTION_ACTIVE_KEY"), keys)
	if err != nil {
		return nil, errors.New("invalid provider encryption keyring")
	}
	return box, nil
}

func serve(ctx context.Context, address string, handler http.Handler) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return errors.New("cannot bind HTTP listen address")
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	failed := make(chan error, 1)
	go func() { failed <- server.Serve(listener) }()
	slog.Info("workgraph API listening", "address", listener.Addr().String())
	select {
	case err := <-failed:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.New("HTTP server stopped unexpectedly")
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
			return errors.New("HTTP graceful shutdown timed out")
		}
		return nil
	}
}

// mcpAllowedOrigins accepts browser origins only, without paths, credentials or URL queries.
func mcpAllowedOrigins() ([]string, error) {
	raw := os.Getenv("WORKGRAPH_MCP_ALLOWED_ORIGINS")
	if raw == "" {
		return nil, nil
	}
	result := []string{}
	for _, value := range strings.Split(raw, ",") {
		value = strings.TrimSpace(value)
		u, err := url.Parse(value)
		if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || (u.Scheme != "https" && u.Scheme != "http") {
			return nil, errors.New("invalid allowed MCP browser origin")
		}
		result = append(result, value)
	}
	return result, nil
}

func mcpPublicOrigin() (string, error) {
	raw := strings.TrimSuffix(os.Getenv("WORKGRAPH_PUBLIC_URL"), "/")
	if raw == "" {
		return "", nil
	}
	u, err := identitydomain.URL(raw)
	if err != nil || u.Path != "" || identitydomain.Origin(u) != raw {
		return "", errors.New("invalid public Gitmora origin")
	}
	return raw, nil
}
