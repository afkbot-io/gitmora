package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"workgraph.local/platform/internal/access"
	directory "workgraph.local/platform/internal/directory/domain"
	forgeapp "workgraph.local/platform/internal/forge/application"
	"workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/platform/secretbox"
)

type credentialVerifier struct{ forgeapp.Provider }

func (credentialVerifier) VerifyProject(_ context.Context, _ domain.Repository, token string) error {
	if token != "synthetic-provider-token" {
		return domain.ErrInvalid
	}
	return nil
}

func TestSavedForgeCredentialCopiesOnlyAPITokenIntoIndependentTargetEnvelope(t *testing.T) {
	service, store, pool, _ := forgeFixture(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "UPDATE memberships SET role='admin' WHERE user_id=$1", subject); err != nil {
		t.Fatal(err)
	}
	initial, err := service.Connect(ctx, subject, repository, providerSecrets())
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.RepositoryForAdmin(ctx, subject, repository)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO repositories(id,project_id,gitlab_url,gitlab_project_id,name) VALUES($1,$2,$3,43,'second')`, otherRepository, project, source.BaseURL); err != nil {
		t.Fatal(err)
	}
	vault, err := secretbox.New("test", map[string][]byte{"test": bytes.Repeat([]byte{1}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	copying := forgeapp.NewService(store, vault, credentialVerifier{}, time.Now)
	status, err := copying.ConnectUsingCredential(ctx, subject, otherRepository, forgeapp.CredentialRef{RepositoryID: repository, Generation: initial.Generation}, domain.Secrets{WebhookMode: "disabled"})
	if err != nil || status.WebhookMode != "disabled" {
		t.Fatal(status, err)
	}
	stored, err := store.Credential(ctx, subject, project, forgeapp.CredentialRef{RepositoryID: otherRepository, Generation: status.Generation})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := vault.Open(stored.Repository.CredentialScope(), stored.Envelope)
	if err != nil {
		t.Fatal(err)
	}
	var secrets domain.Secrets
	if json.Unmarshal(raw, &secrets) != nil || secrets.APIToken != "synthetic-provider-token" || secrets.SigningToken != "" || secrets.LegacyToken != "" {
		t.Fatal("webhook credentials copied to another repository")
	}
	if _, err = vault.Open(source.CredentialScope(), stored.Envelope); err == nil {
		t.Fatal("copied ciphertext retained source scope")
	}
	unchanged, err := service.Status(ctx, subject, repository)
	if err != nil || unchanged.Generation != initial.Generation {
		t.Fatal("reusing a token rotated the source", err)
	}
}

// credentialResolverProbe records whether a request reached external I/O at all.
type credentialResolverProbe struct {
	endpoint string
	calls    int
	during   func()
}

func (p *credentialResolverProbe) CredentialEndpoint(string, string) (string, error) {
	return p.endpoint, nil
}
func (p *credentialResolverProbe) LookupRepositoryWithToken(_ context.Context, _, _, token string) (directory.ResolvedRepository, error) {
	p.calls++
	if token != "synthetic-provider-token" {
		return directory.ResolvedRepository{}, domain.ErrInvalid
	}
	if p.during != nil {
		p.during()
	}
	return directory.ResolvedRepository{URL: p.endpoint, ID: 43, Name: "team/second"}, nil
}

func TestSavedForgeCredentialRechecksProjectEndpointGenerationAndTargetAccess(t *testing.T) {
	service, store, pool, _ := forgeFixture(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "UPDATE memberships SET role='admin' WHERE user_id=$1", subject); err != nil {
		t.Fatal(err)
	}
	initial, err := service.Connect(ctx, subject, repository, providerSecrets())
	if err != nil {
		t.Fatal(err)
	}
	ref := forgeapp.CredentialRef{RepositoryID: repository, Generation: initial.Generation}
	page, err := service.Credentials(ctx, subject, project, "")
	if err != nil || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	encoded, _ := json.Marshal(page)
	if strings.Contains(string(encoded), "synthetic-provider-token") || strings.Contains(string(encoded), "envelope") || page.Items[0].Provider != "gitlab" {
		t.Fatal("credential list exposed secret material or lost provider")
	}
	if _, err = service.Credentials(ctx, reader, project, ""); !errors.Is(err, access.ErrDenied) {
		t.Fatal("reader listed credentials", err)
	}
	source, err := store.RepositoryForAdmin(ctx, subject, repository)
	if err != nil {
		t.Fatal(err)
	}
	resolver := &credentialResolverProbe{endpoint: source.BaseURL}
	service.WithCredentialResolver(resolver)
	if _, err = service.LookupWithCredential(ctx, subject, project, source.BaseURL+"/team/second", "gitlab", ref); err != nil || resolver.calls != 1 {
		t.Fatal("saved-token lookup failed", err)
	}
	resolver.endpoint = source.BaseURL + "/other-instance"
	if _, err = service.LookupWithCredential(ctx, subject, project, resolver.endpoint+"/team/second", "gitlab", ref); !errors.Is(err, domain.ErrCredentialEndpoint) || resolver.calls != 1 {
		t.Fatal("saved token crossed custom instance prefix", err)
	}
	resolver.endpoint = source.BaseURL
	if _, err = service.LookupWithCredential(ctx, subject, project, "ignored", "github", ref); !errors.Is(err, domain.ErrCredentialEndpoint) || resolver.calls != 1 {
		t.Fatal("saved token crossed provider", err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO repositories(id,project_id,gitlab_url,gitlab_project_id,name) VALUES($1,$2,$3,43,'second')`, otherRepository, project, source.BaseURL); err != nil {
		t.Fatal(err)
	}
	// The selected token has no access to project 43: never persist a failed target verification.
	if _, err = service.ConnectUsingCredential(ctx, subject, otherRepository, ref, domain.Secrets{WebhookMode: "disabled"}); err == nil {
		t.Fatal("unverified target connected")
	}
	if _, err = service.Status(ctx, subject, otherRepository); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("failed verification changed the target", err)
	}
	target, err := store.RepositoryForAdmin(ctx, subject, otherRepository)
	if err != nil {
		t.Fatal(err)
	}
	// The storage fence is exercised independently of the real provider's intentional denial.
	connection, err := store.CredentialForTarget(ctx, subject, otherRepository, ref)
	if err != nil {
		t.Fatal(err)
	}
	// A rotation after a successful read must prevent a later write or lookup result.
	if _, err = service.Connect(ctx, subject, repository, providerSecrets()); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Connect(ctx, subject, target, make([]byte, 40), "disabled", false, &connection); !errors.Is(err, domain.ErrCredentialUnavailable) {
		t.Fatal("stale source persisted", err)
	}
	if _, err = service.LookupWithCredential(ctx, subject, project, "ignored", "gitlab", ref); !errors.Is(err, domain.ErrCredentialUnavailable) || resolver.calls != 1 {
		t.Fatal("stale selection reached provider", err)
	}
	current, err := service.Status(ctx, subject, repository)
	if err != nil {
		t.Fatal(err)
	}
	ref.Generation = current.Generation
	resolver.during = func() {
		if err := service.Disconnect(ctx, subject, repository); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = service.LookupWithCredential(ctx, subject, project, "ignored", "gitlab", ref); !errors.Is(err, domain.ErrCredentialUnavailable) {
		t.Fatal("disconnect during lookup returned metadata", err)
	}
	page, err = service.Credentials(ctx, subject, project, "")
	if err != nil || len(page.Items) != 0 {
		t.Fatal("disconnected credential remained selectable", page, err)
	}
}

func TestSavedForgeCredentialCannotLeaveItsProject(t *testing.T) {
	service, store, pool, _ := forgeFixture(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "UPDATE memberships SET role='admin' WHERE user_id=$1", subject); err != nil {
		t.Fatal(err)
	}
	initial, err := service.Connect(ctx, subject, repository, providerSecrets())
	if err != nil {
		t.Fatal(err)
	}
	const foreign = "77777777-7777-4777-8777-777777777777"
	source, err := store.RepositoryForAdmin(ctx, subject, repository)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO projects(id,name) VALUES($1,'other')`, foreign); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO memberships(project_id,user_id,role) VALUES($1,$2,'admin')`, foreign, subject); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO repositories(id,project_id,gitlab_url,gitlab_project_id,name) VALUES($1,$2,$3,42,'foreign')`, otherRepository, foreign, source.BaseURL); err != nil {
		t.Fatal(err)
	}
	ref := forgeapp.CredentialRef{RepositoryID: repository, Generation: initial.Generation}
	if _, err = service.ConnectUsingCredential(ctx, subject, otherRepository, ref, domain.Secrets{WebhookMode: "disabled"}); !errors.Is(err, domain.ErrCredentialUnavailable) {
		t.Fatal("credential copied between projects", err)
	}
}
