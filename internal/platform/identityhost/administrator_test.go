package identityhost

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestActualAdministrationProtocolRetiresBootstrapAndReplaysWithoutPasswordReset(t *testing.T) {
	i := prepared(t)
	secret, err := readCredentials(i.Directory)
	if err != nil {
		t.Fatal(err)
	}
	master, role, bootstrapAlive := false, false, true
	created, deleted := 0, 0
	foreign := false
	unsafeClient := false
	transient := false
	ownerUsername := "gitmora.admin"
	ownerID := i.UserID
	bootstrapID := "99999999-9999-4999-8999-999999999999"
	actualMasterID := "88888888-8888-4888-8888-888888888888"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		respond := func(value any) { json.NewEncoder(w).Encode(value) }
		if r.URL.Path == "/realms/master/protocol/openid-connect/token" {
			r.ParseForm()
			if transient {
				w.WriteHeader(503)
				respond(map[string]string{"error": "private-diagnostic-" + secret.MasterPassword})
				return
			}
			if r.Form.Get("grant_type") == "password" && r.Form.Get("username") == "gitmora.operator" && r.Form.Get("password") == secret.MasterPassword && master {
				respond(map[string]string{"access_token": "permanent-private-token", "token_type": "Bearer"})
				return
			}
			if r.Form.Get("grant_type") == "client_credentials" && r.Form.Get("client_secret") == secret.BootstrapSecret && bootstrapAlive {
				respond(map[string]string{"access_token": "bootstrap-private-token", "token_type": "Bearer"})
				return
			}
			w.WriteHeader(400)
			respond(map[string]string{"error": "invalid_grant"})
			return
		}
		if r.Header.Get("Authorization") != "Bearer permanent-private-token" && r.Header.Get("Authorization") != "Bearer bootstrap-private-token" {
			t.Error("missing private administration authority")
			w.WriteHeader(401)
			return
		}
		if r.Header.Get("Authorization") == "Bearer permanent-private-token" && !role {
			w.WriteHeader(403)
			return
		}
		switch {
		case r.URL.Path == "/admin/realms/master/users" && r.Method == "GET":
			if !master {
				respond([]providerUser{})
				return
			}
			respond([]providerUser{{ID: actualMasterID, Username: "gitmora.operator", Enabled: true}})
		case r.URL.Path == "/admin/realms/master/users/"+i.MasterUserID:
			if !master {
				w.WriteHeader(404)
				return
			}
			respond(providerUser{ID: actualMasterID, Username: "gitmora.operator", Enabled: true})
		case r.URL.Path == "/admin/realms/master/users" && r.Method == "POST":
			var body struct {
				ID string `json:"id"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			master = true
			created++
			w.WriteHeader(201)
		case r.URL.Path == "/admin/realms/master/roles/admin":
			respond(map[string]string{"id": "admin-role", "name": "admin"})
		case r.URL.Path == "/admin/realms/master/users/"+actualMasterID+"/role-mappings/realm":
			role = true
			w.WriteHeader(204)
		case r.URL.Path == "/admin/realms/"+i.Profile.Realm:
			id := i.RealmID
			if foreign {
				id = "foreign-realm"
			}
			respond(map[string]string{"id": id, "realm": i.Profile.Realm})
		case r.URL.Path == "/admin/realms/"+i.Profile.Realm+"/clients":
			respond([]map[string]any{{"id": i.ClientUUID, "clientId": i.Profile.ClientID,
				"enabled": true, "protocol": "openid-connect", "publicClient": false,
				"standardFlowEnabled": true, "implicitFlowEnabled": unsafeClient,
				"directAccessGrantsEnabled": false, "serviceAccountsEnabled": false,
				"redirectUris": []string{i.Profile.ApplicationURL + "/v1/auth/oidc/callback"},
				"webOrigins":   []string{i.Profile.ApplicationURL},
				"attributes":   map[string]string{"pkce.code.challenge.method": "S256", "id.token.signed.response.alg": "RS256"}}})
		case r.URL.Path == "/admin/realms/"+i.Profile.Realm+"/clients/"+i.ClientUUID+"/client-secret":
			respond(map[string]string{"value": secret.ClientSecret})
		case r.URL.Path == "/admin/realms/"+i.Profile.Realm+"/users/"+i.UserID:
			respond(providerUser{ID: ownerID, Username: ownerUsername, Enabled: true})
		case r.URL.Path == "/admin/realms/master/clients":
			if bootstrapAlive {
				respond([]providerClient{{ID: bootstrapID, ClientID: "gitmora-bootstrap"}})
			} else {
				respond([]providerClient{})
			}
		case r.URL.Path == "/admin/realms/master/clients/"+bootstrapID && r.Method == "DELETE":
			bootstrapAlive = false
			deleted++
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected administration request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	address, _ := url.Parse(server.URL)
	i.Profile.HTTPPort, _ = strconv.Atoi(address.Port())
	admin := NewLocalAdministrator()
	proof, err := admin.Finalize(context.Background(), i)
	if err != nil || !proof.BootstrapRemoved || created != 1 || deleted != 1 {
		t.Fatalf("finalization: %+v %v", proof, err)
	}
	i.Phase = "active"
	i.MasterUserID = proof.MasterUserID
	if _, err := admin.Finalize(context.Background(), i); err != nil || created != 1 || deleted != 1 {
		t.Fatalf("active replay changed authority: %v", err)
	}
	ownerUsername = "renamed-owner"
	if _, err := admin.Finalize(context.Background(), i); err != nil || created != 1 || deleted != 1 {
		t.Fatalf("active owner rename changed authority: %v", err)
	}
	i.Phase = "bootstrap"
	if _, err := admin.Finalize(context.Background(), i); err == nil {
		t.Fatal("unexpected bootstrap owner name accepted")
	}
	i.Phase = "active"
	ownerID = "77777777-7777-4777-8777-777777777777"
	if _, err := admin.Finalize(context.Background(), i); err == nil {
		t.Fatal("renamed account with a different subject accepted")
	}
	ownerID = i.UserID
	unsafeClient = true
	if _, err := admin.Finalize(context.Background(), i); err == nil {
		t.Fatal("provider client policy drift accepted")
	}
	unsafeClient = false
	foreign = true
	if _, err := admin.Finalize(context.Background(), i); err == nil {
		t.Fatal("foreign realm accepted")
	}
	foreign = false
	bootstrapAlive = true
	if _, err := admin.Finalize(context.Background(), i); err == nil || deleted != 1 {
		t.Fatal("active state automatically repaired foreign bootstrap authority")
	}
	transient = true
	if _, err := admin.Finalize(context.Background(), i); err == nil || strings.Contains(err.Error(), secret.MasterPassword) {
		t.Fatal("private error/secret exposed")
	}
}

func TestPrivateAdministrationUsesConfiguredProviderPrefix(t *testing.T) {
	i := prepared(t)
	i.Profile.PublicURL += "/auth"
	observed := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed = r.URL.Path
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	address, _ := url.Parse(server.URL)
	i.Profile.HTTPPort, _ = strconv.Atoi(address.Port())
	if _, err := NewLocalAdministrator().Finalize(context.Background(), i); err == nil {
		t.Fatal("unavailable provider accepted")
	}
	if observed != "/auth/realms/master/protocol/openid-connect/token" {
		t.Fatal("private administration escaped configured path", observed)
	}
}
