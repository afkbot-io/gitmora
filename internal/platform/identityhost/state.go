// Package identityhost prepares an operator-owned local identity server without granting application authority.
package identityhost

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"workgraph.local/platform/internal/identifier"
	identity "workgraph.local/platform/internal/identity/domain"
	"workgraph.local/platform/internal/jsonwire"
)

var (
	ErrInvalid    = errors.New("invalid identity hosting configuration")
	ErrConflict   = errors.New("identity hosting state conflicts or already exists")
	ErrFilesystem = errors.New("private identity hosting state unavailable")
	ErrBusy       = errors.New("another identity hosting operation is active")
	ErrProvider   = errors.New("private identity administration unavailable or inconsistent")
)
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
var clientPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)
var imagePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9./:@_-]{0,255}$`)
var secretPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// Profile pins the IdP base, RP origin, exact callback and explicitly prepared local images.
type Profile struct {
	PublicURL      string `json:"public_url"`
	ApplicationURL string `json:"application_url"`
	Realm          string `json:"realm"`
	ClientID       string `json:"client_id"`
	HTTPPort       int    `json:"http_port"`
	IdentityImage  string `json:"identity_image"`
	GatewayImage   string `json:"gateway_image"`
}

// Validate permits HTTP only for loopback; sharing a cookie host requires the explicit /auth provider namespace.
func (p Profile) Validate() error {
	issuer, e := identity.URL(p.PublicURL)
	if e != nil || issuer.Path != "" && issuer.Path != "/auth" || identity.Origin(issuer)+issuer.Path != p.PublicURL {
		return ErrInvalid
	}
	app, e := identity.URL(p.ApplicationURL)
	if e != nil || app.Path != "" || identity.Origin(app) != p.ApplicationURL || strings.EqualFold(app.Hostname(), issuer.Hostname()) && issuer.Path != "/auth" {
		return ErrInvalid
	}
	if !namePattern.MatchString(p.Realm) || p.Realm == "master" || !clientPattern.MatchString(p.ClientID) || p.HTTPPort < 1 || p.HTTPPort > 65535 {
		return ErrInvalid
	}
	for _, image := range []string{p.IdentityImage, p.GatewayImage} {
		if !versionedImage(image) {
			return ErrInvalid
		}
	}
	return nil
}

func versionedImage(image string) bool {
	if !imagePattern.MatchString(image) {
		return false
	}
	name := image[strings.LastIndex(image, "/")+1:]
	if base, digest, ok := strings.Cut(name, "@sha256:"); ok {
		if base == "" || len(digest) != 64 {
			return false
		}
		for _, c := range digest {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				return false
			}
		}
		return true
	}
	base, tag, ok := strings.Cut(name, ":")
	return ok && base != "" && tag != "" && tag != "latest" && !strings.ContainsAny(tag, ":@")
}

// Issuer is the exact expected discovery/token issuer, not a caller-supplied redirect.
func (p Profile) Issuer() string { return p.PublicURL + "/realms/" + p.Realm }

// Origin excludes the provider path when fixing reverse-proxy authority.
func (p Profile) Origin() string {
	u, err := identity.URL(p.PublicURL)
	if err != nil {
		return ""
	}
	return identity.Origin(u)
}

// Prefix is empty for a distinct-host root provider and /auth for the shared-origin profile.
func (p Profile) Prefix() string {
	u, err := identity.URL(p.PublicURL)
	if err != nil {
		return ""
	}
	return u.Path
}

// Installation is non-secret pinned metadata. Finalize must retire bootstrap authority before application activation.
type Installation struct {
	Version      int     `json:"version"`
	ID           string  `json:"id"`
	Directory    string  `json:"directory"`
	ComposeFile  string  `json:"compose_file"`
	Profile      Profile `json:"profile"`
	RealmID      string  `json:"realm_id"`
	ClientUUID   string  `json:"client_uuid"`
	UserID       string  `json:"user_id"`
	MasterUserID string  `json:"master_user_id,omitempty"`
	Phase        string  `json:"phase"`
}

func (i Installation) Project() string {
	return "gitmora-identity-" + strings.ReplaceAll(i.ID, "-", "")
}
func (i Installation) Validate() error {
	if i.Version != 1 || !identifier.Valid(i.ID) || !identifier.Valid(i.RealmID) || !identifier.Valid(i.ClientUUID) || !identifier.Valid(i.UserID) || (i.MasterUserID != "" && !identifier.Valid(i.MasterUserID)) || (i.Phase == "active" && !identifier.Valid(i.MasterUserID)) || !absolute(i.Directory) || i.Directory == string(filepath.Separator) || !absolute(i.ComposeFile) || (i.Phase != "bootstrap" && i.Phase != "active") {
		return ErrInvalid
	}
	return i.Profile.Validate()
}
func absolute(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && len(path) <= 4096 && !strings.ContainsAny(path, "\x00\r\n")
}

type credentials struct {
	BootstrapSecret  string `json:"bootstrap_secret"`
	MasterPassword   string `json:"master_password"`
	UserPassword     string `json:"user_password"`
	ClientSecret     string `json:"client_secret"`
	DatabasePassword string `json:"database_password"`
}

func (credentials) String() string               { return "[redacted]" }
func (credentials) GoString() string             { return "[redacted]" }
func (credentials) MarshalJSON() ([]byte, error) { return []byte(`"[redacted]"`), nil }
func entropy() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", ErrFilesystem
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}
func write(path string, raw []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return ErrFilesystem
	}
	defer f.Close()
	if _, err = f.Write(raw); err != nil {
		return ErrFilesystem
	}
	if f.Chmod(mode) != nil || f.Sync() != nil {
		return ErrFilesystem
	}
	return nil
}
func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return ErrFilesystem
	}
	defer f.Close()
	if f.Sync() != nil {
		return ErrFilesystem
	}
	return nil
}

// Create writes one new private installation; existing state is never overwritten, reused or retargeted.
func Create(ctx context.Context, root, compose string, p Profile) (Installation, error) {
	if !absolute(root) || root == string(filepath.Separator) || !absolute(compose) || p.Validate() != nil {
		return Installation{}, ErrInvalid
	}
	info, err := os.Stat(compose)
	if err != nil || !info.Mode().IsRegular() {
		return Installation{}, ErrInvalid
	}
	i := Installation{Version: 1, Directory: root, ComposeFile: compose, Profile: p, Phase: "bootstrap"}
	for _, id := range []*string{&i.ID, &i.RealmID, &i.ClientUUID, &i.UserID} {
		*id, err = identifier.New()
		if err != nil {
			return Installation{}, ErrFilesystem
		}
	}
	var secret credentials
	for _, value := range []*string{&secret.BootstrapSecret, &secret.MasterPassword, &secret.UserPassword, &secret.ClientSecret, &secret.DatabasePassword} {
		*value, err = entropy()
		if err != nil {
			return Installation{}, err
		}
	}
	postgres, err := entropy()
	if err != nil {
		return Installation{}, err
	}
	if ctx.Err() != nil {
		return Installation{}, ctx.Err()
	}
	if err = os.Mkdir(root, 0700); err != nil {
		if os.IsExist(err) {
			return Installation{}, ErrConflict
		}
		return Installation{}, ErrFilesystem
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(root)
		}
	}()
	for _, dir := range []string{"secrets", "bootstrap", "runtime"} {
		if os.Mkdir(filepath.Join(root, dir), 0700) != nil {
			return Installation{}, ErrFilesystem
		}
	}
	ssl := "external"
	if strings.HasPrefix(p.PublicURL, "http://") {
		ssl = "none"
	}
	realm := map[string]any{
		"id": i.RealmID, "realm": p.Realm, "enabled": true, "sslRequired": ssl,
		"registrationAllowed": false, "resetPasswordAllowed": false, "passwordPolicy": "length(12)",
		"loginWithEmailAllowed": false, "rememberMe": false,
		"bruteForceProtected": true, "failureFactor": 5,
		"waitIncrementSeconds": 60, "maxFailureWaitSeconds": 300,
		"eventsEnabled": true, "adminEventsEnabled": true, "adminEventsDetailsEnabled": false,
		"clients": []any{map[string]any{
			"id": i.ClientUUID, "clientId": p.ClientID, "protocol": "openid-connect", "enabled": true,
			"publicClient": false, "clientAuthenticatorType": "client-secret", "secret": secret.ClientSecret,
			"standardFlowEnabled": true, "implicitFlowEnabled": false,
			"directAccessGrantsEnabled": false, "serviceAccountsEnabled": false,
			"redirectUris": []string{p.ApplicationURL + "/v1/auth/oidc/callback"}, "webOrigins": []string{p.ApplicationURL},
			"attributes": map[string]string{"pkce.code.challenge.method": "S256", "id.token.signed.response.alg": "RS256"},
		}},
		"users": []any{map[string]any{
			"id": i.UserID, "username": "gitmora.admin", "enabled": true,
			"firstName": "Gitmora", "lastName": "Administrator",
			// Realm import ignores credential.temporary; require replacement explicitly.
			"requiredActions": []string{"UPDATE_PASSWORD"},
			"credentials":     []any{map[string]any{"type": "password", "value": secret.UserPassword, "temporary": true}},
		}},
	}
	realmJSON, _ := json.MarshalIndent(realm, "", "  ")
	type privateCredentials credentials
	privateJSON, _ := json.MarshalIndent(privateCredentials(secret), "", "  ")
	configuration := fmt.Sprintf("db-url=jdbc:postgresql://postgres:5432/keycloak\ndb-username=keycloak\ndb-password=%s\nhostname=%s\nhostname-strict=true\nhttp-enabled=true\nproxy-headers=xforwarded\n", secret.DatabasePassword, p.PublicURL)
	providerPath := p.Prefix()
	if providerPath == "" {
		providerPath = "/"
	}
	configuration += "http-relative-path=" + providerPath + "\nhttp-management-relative-path=/\n"
	files := map[string]struct {
		raw  []byte
		mode os.FileMode
	}{
		"secrets/postgres_password": {[]byte(postgres), 0444}, "secrets/app_password": {[]byte(secret.DatabasePassword), 0444}, "secrets/oidc_client_secret": {[]byte(secret.ClientSecret), 0444},
		"secrets/keycloak-active.conf": {[]byte(configuration), 0444}, "secrets/keycloak-bootstrap.conf": {[]byte(configuration + "bootstrap-admin-client-id=gitmora-bootstrap\nbootstrap-admin-client-secret=" + secret.BootstrapSecret + "\n"), 0444},
		"bootstrap/gitmora-realm.json": {realmJSON, 0444}, "operator.json": {privateJSON, 0600},
	}
	for name, file := range files {
		if ctx.Err() != nil {
			return Installation{}, ctx.Err()
		}
		if write(filepath.Join(root, name), file.raw, file.mode) != nil {
			return Installation{}, ErrFilesystem
		}
	}
	metadata, _ := json.MarshalIndent(i, "", "  ")
	if write(filepath.Join(root, "identity.json"), append(metadata, '\n'), 0600) != nil {
		return Installation{}, ErrFilesystem
	}
	for _, dir := range []string{"bootstrap", "runtime"} {
		if os.Chmod(filepath.Join(root, dir), 0755) != nil {
			return Installation{}, ErrFilesystem
		}
	}
	for _, dir := range []string{filepath.Join(root, "secrets"), filepath.Join(root, "bootstrap"), root, filepath.Dir(root)} {
		if syncDir(dir) != nil {
			return Installation{}, ErrFilesystem
		}
	}
	complete = true
	return i, nil
}

func readPrivate(root, name string, limit int) ([]byte, error) {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, ErrFilesystem
	}
	path := filepath.Join(root, name)
	info, err = os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, ErrFilesystem
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, ErrFilesystem
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, int64(limit+1)))
	if err != nil || len(raw) > limit {
		return nil, ErrFilesystem
	}
	return raw, nil
}

// Load refuses insecure, future-version, symlinked, relocated or malformed operator metadata.
func Load(root string) (Installation, error) {
	if !absolute(root) {
		return Installation{}, ErrInvalid
	}
	raw, err := readPrivate(root, "identity.json", 16384)
	if err != nil {
		return Installation{}, err
	}
	var i Installation
	if jsonwire.DecodeObject(raw, &i) != nil || i.Directory != root || i.Validate() != nil {
		return Installation{}, ErrInvalid
	}
	return i, nil
}
func readCredentials(root string) (credentials, error) {
	raw, err := readPrivate(root, "operator.json", 8192)
	if err != nil {
		return credentials{}, err
	}
	var value credentials
	if jsonwire.DecodeObject(raw, &value) != nil {
		return credentials{}, ErrFilesystem
	}
	for _, secret := range []string{value.BootstrapSecret, value.MasterPassword, value.UserPassword, value.ClientSecret, value.DatabasePassword} {
		if !secretPattern.MatchString(secret) {
			return credentials{}, ErrFilesystem
		}
	}
	return value, nil
}
