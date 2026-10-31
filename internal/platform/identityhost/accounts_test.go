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

func TestAccountAdministrationPinsRealmAndNeverResetsExistingPassword(t *testing.T) {
	i := prepared(t)
	i.Phase = "active"
	i.MasterUserID = "88888888-8888-4888-8888-888888888888"
	id := "77777777-7777-4777-8777-777777777777"
	created := 0
	enabled := false
	var user providerUser
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/protocol/openid-connect/token"):
			json.NewEncoder(w).Encode(map[string]string{"access_token": "private-token", "token_type": "Bearer"})
		case r.URL.Path == "/admin/realms/"+i.Profile.Realm && r.Method == "GET":
			json.NewEncoder(w).Encode(map[string]any{"id": i.RealmID, "realm": i.Profile.Realm, "registrationAllowed": enabled, "loginTheme": "gitmora", "passwordPolicy": "length(20) and upperCase(1)", "resetPasswordAllowed": true})
		case r.URL.Path == "/admin/realms/"+i.Profile.Realm && r.Method == "PUT":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			enabled = body["registrationAllowed"].(bool)
			if policy, ok := body["passwordPolicy"]; ok && policy != "length(20) and upperCase(1)" {
				t.Error("existing password policy weakened")
			}
			if _, ok := body["resetPasswordAllowed"]; ok {
				t.Error("existing account recovery policy changed")
			}
			w.WriteHeader(204)

		case strings.HasSuffix(r.URL.Path, "/users/"+id):
			if user.ID == "" {
				w.WriteHeader(404)
			} else {
				json.NewEncoder(w).Encode(user)
			}
		case strings.HasSuffix(r.URL.Path, "/users") && r.Method == "POST":
			created++
			w.Header().Set("Location", "/admin/realms/"+i.Profile.Realm+"/users/"+id)
			user = providerUser{ID: id, Username: "engineer", Enabled: true}
			w.WriteHeader(201)
		default:
			t.Error("unexpected authority", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	i.Profile.HTTPPort, _ = strconv.Atoi(u.Port())
	admin := NewLocalAdministrator()
	if e := admin.Configure(context.Background(), i, true); e != nil || !enabled {
		t.Fatal(e)
	}
	input := UserInput{Username: "engineer", DisplayName: "Engineer", Password: []byte("private-password-123")}
	who, e := admin.CreateUser(context.Background(), i, input)
	if e != nil || who.Subject != id {
		t.Fatal(who, e)
	}
	input.ID = who.Subject
	input.Password = nil
	if _, e = admin.CreateUser(context.Background(), i, input); e != nil || created != 1 {
		t.Fatal("retry reset password", created, e)
	}
	user.Username = "foreign"
	if _, e = admin.CreateUser(context.Background(), i, input); e == nil {
		t.Fatal("foreign user adopted")
	}
	if e = admin.Configure(context.Background(), i, false); e != nil || enabled {
		t.Fatal("signup not closed", e)
	}
}
