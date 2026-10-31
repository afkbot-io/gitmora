package identityhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"workgraph.local/platform/internal/identifier"
	"workgraph.local/platform/internal/jsonwire"
)

var errAuthentication = errors.New("private identity authentication rejected")
var errAdministrationDenied = errors.New("private identity administration denied")

type localAdministrator struct{ http *http.Client }
type administration struct {
	client *http.Client
	base   string
}

// NewLocalAdministrator accesses only the installation's numeric-loopback gateway, never a caller URL or ambient proxy.
func NewLocalAdministrator() AccountAdministrator {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxIdleConns = 2
	transport.IdleConnTimeout = time.Second
	transport.MaxResponseHeaderBytes = 16384
	return &localAdministrator{http: &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (a administration) request(ctx context.Context, method, path, token, kind string, body []byte, target any) (int, error) {
	request, err := http.NewRequestWithContext(ctx, method, a.base+path, bytes.NewReader(body))
	if err != nil {
		return 0, ErrProvider
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if kind != "" {
		request.Header.Set("Content-Type", kind)
	}
	response, err := a.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, ErrProvider
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024+1))
	if err != nil || len(raw) > 1024*1024 {
		return 0, ErrProvider
	}
	if response.StatusCode == 404 {
		return 404, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response.StatusCode, ErrProvider
	}
	if target != nil && jsonwire.DecodeExternalObject(raw, target) != nil {
		return response.StatusCode, ErrProvider
	}
	return response.StatusCode, nil
}
func (a administration) token(ctx context.Context, values url.Values) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", a.base+"/realms/master/protocol/openid-connect/token", strings.NewReader(values.Encode()))
	if err != nil {
		return "", ErrProvider
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r, err := a.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", ErrProvider
	}
	defer r.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(r.Body, 65537))
	if err != nil || len(raw) > 65536 {
		return "", ErrProvider
	}
	if r.StatusCode == 400 || r.StatusCode == 401 {
		return "", errAuthentication
	}
	if r.StatusCode != 200 {
		return "", ErrProvider
	}
	var result struct {
		Token string `json:"access_token"`
		Kind  string `json:"token_type"`
	}
	if jsonwire.DecodeExternalObject(raw, &result) != nil || result.Token == "" || len(result.Token) > 32768 || !strings.EqualFold(result.Kind, "Bearer") {
		return "", ErrProvider
	}
	return result.Token, nil
}
func permanentToken(ctx context.Context, a administration, secret credentials) (string, error) {
	return a.token(ctx, url.Values{"grant_type": {"password"}, "client_id": {"admin-cli"}, "username": {"gitmora.operator"}, "password": {secret.MasterPassword}})
}

type providerUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Enabled  bool   `json:"enabled"`
}
type providerClient struct {
	ID             string            `json:"id"`
	ClientID       string            `json:"clientId"`
	Protocol       string            `json:"protocol"`
	Enabled        bool              `json:"enabled"`
	Public         bool              `json:"publicClient"`
	StandardFlow   bool              `json:"standardFlowEnabled"`
	ImplicitFlow   bool              `json:"implicitFlowEnabled"`
	PasswordGrant  bool              `json:"directAccessGrantsEnabled"`
	ServiceAccount bool              `json:"serviceAccountsEnabled"`
	Redirects      []string          `json:"redirectUris"`
	Origins        []string          `json:"webOrigins"`
	Attributes     map[string]string `json:"attributes"`
}

// matchesPolicy verifies the immutable RP contract before bootstrap can be retired.
func (c providerClient) matchesPolicy(p Profile) bool {
	return c.Enabled && c.Protocol == "openid-connect" && !c.Public && c.StandardFlow &&
		!c.ImplicitFlow && !c.PasswordGrant && !c.ServiceAccount &&
		len(c.Redirects) == 1 && c.Redirects[0] == p.ApplicationURL+"/v1/auth/oidc/callback" &&
		len(c.Origins) == 1 && c.Origins[0] == p.ApplicationURL &&
		c.Attributes["pkce.code.challenge.method"] == "S256" && c.Attributes["id.token.signed.response.alg"] == "RS256"
}

func (a administration) master(ctx context.Context, token string) (providerUser, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", a.base+"/admin/realms/master/users?username=gitmora.operator&exact=true&max=2", nil)
	if err != nil {
		return providerUser{}, ErrProvider
	}
	req.Header.Set("Authorization", "Bearer "+token)
	r, err := a.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return providerUser{}, ctx.Err()
		}
		return providerUser{}, ErrProvider
	}
	defer r.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(r.Body, 65537))
	if err != nil || len(raw) > 65536 {
		return providerUser{}, ErrProvider
	}
	if r.StatusCode == 403 {
		return providerUser{}, errAdministrationDenied
	}
	if r.StatusCode != 200 {
		return providerUser{}, ErrProvider
	}
	var users []providerUser
	if jsonwire.DecodeExternalArray(raw, &users) != nil || len(users) > 1 {
		return providerUser{}, ErrProvider
	}
	if len(users) == 0 {
		return providerUser{}, nil
	}
	u := users[0]
	if !identifier.Valid(u.ID) || u.Username != "gitmora.operator" || !u.Enabled {
		return providerUser{}, ErrConflict
	}
	return u, nil
}

func (a administration) clients(ctx context.Context, realm, client, token string) ([]providerClient, error) {
	request, err := http.NewRequestWithContext(ctx, "GET", a.base+"/admin/realms/"+realm+"/clients?clientId="+url.QueryEscape(client), nil)
	if err != nil {
		return nil, ErrProvider
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := a.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrProvider
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(raw) > 65536 || response.StatusCode != 200 {
		return nil, ErrProvider
	}
	var result []providerClient
	if jsonwire.DecodeExternalArray(raw, &result) != nil || len(result) > 1 {
		return nil, ErrProvider
	}
	for _, item := range result {
		if item.ClientID != client || !identifier.Valid(item.ID) {
			return nil, ErrConflict
		}
	}
	return result, nil
}
func (a administration) ensurePermanent(ctx context.Context, i Installation, secret credentials, bootstrap string) error {
	user, err := a.master(ctx, bootstrap)
	if err != nil {
		return err
	}
	if user.ID == "" {
		input := map[string]any{"username": "gitmora.operator", "enabled": true, "firstName": "Gitmora", "lastName": "Operator", "credentials": []any{map[string]any{"type": "password", "value": secret.MasterPassword, "temporary": false}}}
		raw, _ := json.Marshal(input)
		if status, e := a.request(ctx, "POST", "/admin/realms/master/users", bootstrap, "application/json", raw, nil); e != nil || status != 201 {
			return ErrProvider
		}
		user, err = a.master(ctx, bootstrap)
		if err != nil || user.ID == "" {
			return ErrProvider
		}
	}
	if i.MasterUserID != "" && user.ID != i.MasterUserID {
		return ErrConflict
	}
	// Prove the stored private password before assigning an administrative role; never take over an existing namesake.
	if _, err := permanentToken(ctx, a, secret); err != nil {
		return ErrConflict
	}
	path := "/admin/realms/master/users/" + user.ID
	var role struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if status, e := a.request(ctx, "GET", "/admin/realms/master/roles/admin", bootstrap, "", nil, &role); e != nil || status != 200 || role.Name != "admin" || role.ID == "" {
		return ErrProvider
	}
	raw, _ := json.Marshal([]any{role})
	status, err := a.request(ctx, "POST", path+"/role-mappings/realm", bootstrap, "application/json", raw, nil)
	if err != nil || status != 204 {
		return ErrProvider
	}
	return nil
}

func (a *localAdministrator) Finalize(parent context.Context, i Installation) (Proof, error) {
	if i.Validate() != nil {
		return Proof{}, ErrInvalid
	}
	secret, err := readCredentials(i.Directory)
	if err != nil {
		return Proof{}, err
	}
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()
	remote := administration{client: a.http, base: "http://127.0.0.1:" + strconv.Itoa(i.Profile.HTTPPort) + i.Profile.Prefix()}
	token, err := permanentToken(ctx, remote, secret)
	var master providerUser
	if err == nil {
		master, err = remote.master(ctx, token)
		if err == nil && master.ID == "" {
			err = ErrConflict
		}
	}
	if (errors.Is(err, errAuthentication) || errors.Is(err, errAdministrationDenied)) && i.Phase == "bootstrap" {
		bootstrap, e := remote.token(ctx, url.Values{"grant_type": {"client_credentials"}, "client_id": {"gitmora-bootstrap"}, "client_secret": {secret.BootstrapSecret}})
		if e != nil {
			return Proof{}, ErrProvider
		}
		if e = remote.ensurePermanent(ctx, i, secret, bootstrap); e != nil {
			return Proof{}, e
		}
		token, err = permanentToken(ctx, remote, secret)
		if err == nil {
			master, err = remote.master(ctx, token)
		}
	}
	if err != nil {
		return Proof{}, ErrProvider
	}
	if master.ID == "" || (i.MasterUserID != "" && master.ID != i.MasterUserID) {
		return Proof{}, ErrConflict
	}
	var realm struct {
		ID    string `json:"id"`
		Realm string `json:"realm"`
	}
	prefix := "/admin/realms/" + i.Profile.Realm
	if status, e := remote.request(ctx, "GET", prefix, token, "", nil, &realm); e != nil || status != 200 || realm.ID != i.RealmID || realm.Realm != i.Profile.Realm {
		return Proof{}, ErrConflict
	}
	clients, err := remote.clients(ctx, i.Profile.Realm, i.Profile.ClientID, token)
	if err != nil {
		return Proof{}, err
	}
	if len(clients) != 1 || clients[0].ID != i.ClientUUID || !clients[0].matchesPolicy(i.Profile) {
		return Proof{}, ErrConflict
	}
	var clientSecret struct {
		Value string `json:"value"`
	}
	if status, e := remote.request(ctx, "GET", prefix+"/clients/"+i.ClientUUID+"/client-secret", token, "", nil, &clientSecret); e != nil || status != 200 || clientSecret.Value != secret.ClientSecret {
		return Proof{}, ErrConflict
	}
	var user providerUser
	if status, e := remote.request(ctx, "GET", prefix+"/users/"+i.UserID, token, "", nil, &user); e != nil || status != 200 || user.ID != i.UserID || user.Username != "gitmora.admin" || !user.Enabled {
		return Proof{}, ErrConflict
	}
	bootstrap, err := remote.clients(ctx, "master", "gitmora-bootstrap", token)
	if err != nil {
		return Proof{}, err
	}
	if len(bootstrap) > 0 {
		if i.Phase != "bootstrap" {
			return Proof{}, ErrConflict
		}
		if status, e := remote.request(ctx, "DELETE", "/admin/realms/master/clients/"+bootstrap[0].ID, token, "", nil, nil); e != nil || status != 204 {
			return Proof{}, ErrProvider
		}
	}
	remaining, err := remote.clients(ctx, "master", "gitmora-bootstrap", token)
	if err != nil || len(remaining) != 0 {
		return Proof{}, ErrProvider
	}
	return Proof{RealmID: i.RealmID, ClientUUID: i.ClientUUID, UserID: i.UserID, MasterUserID: master.ID, BootstrapRemoved: true}, nil
}
