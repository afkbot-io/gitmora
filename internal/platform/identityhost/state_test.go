package identityhost_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"workgraph.local/platform/internal/platform/identityhost"
)

func profile() identityhost.Profile {
	return identityhost.Profile{PublicURL: "https://identity.internal", ApplicationURL: "https://gitmora.internal", Realm: "gitmora", ClientID: "gitmora", HTTPPort: 18081, IdentityImage: "gitmora-identity:26.8.1", GatewayImage: "gitmora:source-verified"}
}
func fixture(t *testing.T) (string, string) {
	t.Helper()
	parent := t.TempDir()
	compose := filepath.Join(parent, "compose.yaml")
	if err := os.WriteFile(compose, []byte("services: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(parent, "identity"), compose
}

func TestPrivateIdentityPreparationPinsScopeAndNeverExposesSecrets(t *testing.T) {
	root, compose := fixture(t)
	i, err := identityhost.Create(context.Background(), root, compose, profile())
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := identityhost.Load(root)
	if err != nil || loaded != i {
		t.Fatalf("load: %v", err)
	}
	if i.Phase != "bootstrap" || !strings.HasPrefix(i.Project(), "gitmora-identity-") || i.RealmID == "" || i.UserID == "" || i.ClientUUID == "" {
		t.Fatalf("missing immutable identity scope: %+v", i)
	}
	public, _ := json.Marshal(i)
	operator, err := os.ReadFile(filepath.Join(root, "operator.json"))
	if err != nil {
		t.Fatal(err)
	}
	var secrets map[string]string
	if err := json.Unmarshal(operator, &secrets); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, key := range []string{"bootstrap_secret", "master_password", "user_password", "client_secret"} {
		value := secrets[key]
		if len(value) != 43 || seen[value] || bytes.Contains(public, []byte(value)) {
			t.Fatal("missing/shared/exposed private material", key)
		}
		seen[value] = true
	}
	for _, path := range []string{root, filepath.Join(root, "secrets")} {
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0700 {
			t.Fatal("private root became traversable")
		}
	}
	conf, err := os.ReadFile(filepath.Join(root, "secrets", "keycloak-bootstrap.conf"))
	if err != nil {
		t.Fatal(err)
	}
	active, err := os.ReadFile(filepath.Join(root, "secrets", "keycloak-active.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(conf, []byte(secrets["bootstrap_secret"])) || bytes.Contains(active, []byte("bootstrap-admin")) {
		t.Fatal("bootstrap material retained in active runtime")
	}
	realm, err := os.ReadFile(filepath.Join(root, "bootstrap", "gitmora-realm.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		ID                  string `json:"id"`
		Realm               string `json:"realm"`
		RegistrationAllowed bool   `json:"registrationAllowed"`
		Clients             []struct {
			ID         string            `json:"id"`
			ClientID   string            `json:"clientId"`
			Secret     string            `json:"secret"`
			Direct     bool              `json:"directAccessGrantsEnabled"`
			Redirects  []string          `json:"redirectUris"`
			Attributes map[string]string `json:"attributes"`
		} `json:"clients"`
		Users []struct {
			ID              string   `json:"id"`
			RequiredActions []string `json:"requiredActions"`
		} `json:"users"`
	}
	if json.Unmarshal(realm, &raw) != nil || raw.ID != i.RealmID || raw.RegistrationAllowed || len(raw.Clients) != 1 || raw.Clients[0].ID != i.ClientUUID || raw.Clients[0].Secret != secrets["client_secret"] || raw.Clients[0].Direct || raw.Clients[0].Attributes["pkce.code.challenge.method"] != "S256" || len(raw.Users) != 1 || raw.Users[0].ID != i.UserID {
		t.Fatal("unsafe or unpinned realm import")
	}
	if len(raw.Clients[0].Redirects) != 1 || raw.Clients[0].Redirects[0] != "https://gitmora.internal/v1/auth/oidc/callback" {
		t.Fatal("callback widened")
	}
	if len(raw.Users[0].RequiredActions) != 1 || raw.Users[0].RequiredActions[0] != "UPDATE_PASSWORD" {
		t.Fatal("realm import does not require first-login password replacement")
	}
	if _, err := identityhost.Create(context.Background(), root, compose, profile()); !errors.Is(err, identityhost.ErrConflict) {
		t.Fatalf("existing secret state overwritten: %v", err)
	}
	if after, _ := os.ReadFile(filepath.Join(root, "operator.json")); !bytes.Equal(operator, after) {
		t.Fatal("retry rotated existing credentials")
	}
}

func TestIdentityValidationAndConcurrentCreation(t *testing.T) {
	root, compose := fixture(t)
	for _, change := range []func(*identityhost.Profile){func(p *identityhost.Profile) { p.PublicURL = p.ApplicationURL }, func(p *identityhost.Profile) { p.PublicURL = "http://identity.internal" }, func(p *identityhost.Profile) { p.PublicURL = "https://identity.internal/path" }, func(p *identityhost.Profile) { p.Realm = "../master" }, func(p *identityhost.Profile) { p.Realm = "master" }, func(p *identityhost.Profile) { p.IdentityImage = "keycloak:latest" }, func(p *identityhost.Profile) { p.HTTPPort = 0 }} {
		p := profile()
		change(&p)
		if _, err := identityhost.Create(context.Background(), root, compose, p); !errors.Is(err, identityhost.ErrInvalid) {
			t.Fatalf("unsafe configuration: %v", err)
		}
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatal("invalid input wrote state")
		}
	}
	var group sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := identityhost.Create(context.Background(), root, compose, profile())
			results <- err
		}()
	}
	group.Wait()
	close(results)
	passed, conflicts := 0, 0
	for err := range results {
		if err == nil {
			passed++
		} else if errors.Is(err, identityhost.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if passed != 1 || conflicts != 1 {
		t.Fatal("concurrent installation did not select one owner")
	}
	metadata, _ := os.ReadFile(filepath.Join(root, "identity.json"))
	copyRoot := filepath.Join(filepath.Dir(root), "copy")
	os.Mkdir(copyRoot, 0700)
	os.WriteFile(filepath.Join(copyRoot, "identity.json"), metadata, 0600)
	if _, err := identityhost.Load(copyRoot); err == nil {
		t.Fatal("relocated state redirected an operation")
	}
}

func TestRegistryPortDoesNotSelectAnImplicitLatestIdentityImage(t *testing.T) {
	for _, image := range []string{"registry.internal:5000/keycloak", "registry.internal:5000/keycloak:", "registry.internal:5000/keycloak:latest", "registry.internal:5000/keycloak@sha256:abc"} {
		p := profile()
		p.IdentityImage = image
		if p.Validate() == nil {
			t.Fatal("unversioned provider image accepted", image)
		}
	}
	for _, image := range []string{"registry.internal:5000/keycloak:26.8.1", "registry.internal:5000/keycloak@sha256:" + strings.Repeat("a", 64)} {
		p := profile()
		p.IdentityImage = image
		if err := p.Validate(); err != nil {
			t.Fatal("explicit registry image rejected", err)
		}
	}
}

func TestSameOriginIdentityUsesExplicitAuthPrefix(t *testing.T) {
	root, compose := fixture(t)
	p := profile()
	p.PublicURL = p.ApplicationURL + "/auth"
	i, err := identityhost.Create(context.Background(), root, compose, p)
	if err != nil {
		t.Fatal("explicit shared-origin provider rejected", err)
	}
	configuration, err := os.ReadFile(filepath.Join(root, "secrets", "keycloak-active.conf"))
	if err != nil || !bytes.Contains(configuration, []byte("http-relative-path=/auth\n")) || !bytes.Contains(configuration, []byte("http-management-relative-path=/\n")) || i.Profile.Issuer() != p.ApplicationURL+"/auth/realms/gitmora" {
		t.Fatal("provider prefix or private management path lost")
	}
}
