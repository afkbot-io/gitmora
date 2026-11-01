package identityhost

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"workgraph.local/platform/internal/identifier"
	identity "workgraph.local/platform/internal/identity/domain"
)

// AccountAdministrator is an operator-only provider port; it is never mounted in the application API.
type AccountAdministrator interface {
	Administrator
	Configure(context.Context, Installation, bool) error
	CreateUser(context.Context, Installation, UserInput) (identity.Identity, error)
}

type UserInput struct {
	ID, Username, DisplayName string
	Password                  []byte
}

func (UserInput) String() string               { return "[private account input]" }
func (UserInput) GoString() string             { return "[private account input]" }
func (UserInput) MarshalJSON() ([]byte, error) { return []byte(`"[private account input]"`), nil }

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,79}$`)

func (u UserInput) Validate() error {
	if (u.ID != "" && !identifier.Valid(u.ID)) || !usernamePattern.MatchString(u.Username) || strings.TrimSpace(u.DisplayName) != u.DisplayName || len(u.DisplayName) < 1 || len(u.DisplayName) > 512 || !utf8.ValidString(u.DisplayName) || strings.ContainsFunc(u.DisplayName, unicode.IsControl) || (u.ID == "" && (utf8.RuneCount(u.Password) < 12 || len(u.Password) > 1024 || !utf8.Valid(u.Password) || strings.ContainsAny(string(u.Password), "\x00\r\n"))) {
		return ErrInvalid
	}
	return nil
}
func (a *localAdministrator) accountSession(ctx context.Context, i Installation) (administration, string, error) {
	if i.Validate() != nil || i.Phase != "active" {
		return administration{}, "", ErrInvalid
	}
	secret, e := readCredentials(i.Directory)
	if e != nil {
		return administration{}, "", e
	}
	remote := administration{client: a.http, base: "http://127.0.0.1:" + strconv.Itoa(i.Profile.HTTPPort) + i.Profile.Prefix()}
	token, e := permanentToken(ctx, remote, secret)
	if e != nil {
		return remote, "", ErrProvider
	}
	var realm struct {
		ID    string `json:"id"`
		Realm string `json:"realm"`
	}
	status, e := remote.request(ctx, "GET", "/admin/realms/"+i.Profile.Realm, token, "", nil, &realm)
	if e != nil || status != 200 || realm.ID != i.RealmID || realm.Realm != i.Profile.Realm {
		return remote, "", ErrConflict
	}
	return remote, token, nil
}

// Configure changes only hosted account policy. Disabling signup leaves normal login and operator creation intact.
func (a *localAdministrator) Configure(ctx context.Context, i Installation, registration bool) error {
	remote, token, e := a.accountSession(ctx, i)
	if e != nil {
		return e
	}
	path := "/admin/realms/" + i.Profile.Realm
	var existing struct {
		PasswordPolicy       string `json:"passwordPolicy"`
		Internationalization bool   `json:"internationalizationEnabled"`
	}
	if status, e := remote.request(ctx, "GET", path, token, "", nil, &existing); e != nil || status != 200 {
		return ErrProvider
	}
	settings := map[string]any{"registrationAllowed": registration, "loginTheme": "gitmora", "displayName": "Gitmora"}
	if existing.PasswordPolicy == "" {
		settings["passwordPolicy"] = "length(12)"
	}
	if !existing.Internationalization {
		settings["internationalizationEnabled"] = true
		settings["defaultLocale"] = "ru"
		settings["supportedLocales"] = []string{"ru", "en"}
	}
	raw, _ := json.Marshal(settings)
	if status, e := remote.request(ctx, "PUT", path, token, "application/json", raw, nil); e != nil || status != 204 {
		return ErrProvider
	}
	var current struct {
		Registration bool   `json:"registrationAllowed"`
		Theme        string `json:"loginTheme"`
	}
	if status, e := remote.request(ctx, "GET", path, token, "", nil, &current); e != nil || status != 200 || current.Registration != registration || current.Theme != "gitmora" {
		return ErrConflict
	}
	return nil
}

// CreateUser creates a provider-owned subject, or explicitly recovers one by ID without touching its password.
// Keycloak assigns the subject; UserRepresentation.id is not an idempotency key for POST /users.
func (a *localAdministrator) CreateUser(ctx context.Context, i Installation, input UserInput) (identity.Identity, error) {
	input.Username = strings.ToLower(input.Username)
	if input.Validate() != nil {
		return identity.Identity{}, ErrInvalid
	}
	remote, token, e := a.accountSession(ctx, i)
	if e != nil {
		return identity.Identity{}, e
	}
	subject := input.ID
	if subject == "" {
		names := strings.Fields(input.DisplayName)
		first, last := input.DisplayName, ""
		if len(names) > 1 {
			first = names[0]
			last = strings.Join(names[1:], " ")
		}
		raw, _ := json.Marshal(map[string]any{"username": input.Username, "firstName": first, "lastName": last, "enabled": true, "requiredActions": []string{}, "credentials": []any{map[string]any{"type": "password", "value": string(input.Password), "temporary": false}}})
		defer clear(raw)
		request, e := http.NewRequestWithContext(ctx, "POST", remote.base+"/admin/realms/"+i.Profile.Realm+"/users", bytes.NewReader(raw))
		if e != nil {
			return identity.Identity{}, ErrProvider
		}
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		response, e := a.http.Do(request)
		if e != nil {
			return identity.Identity{}, ErrProvider
		}
		defer response.Body.Close()
		if _, e = io.Copy(io.Discard, io.LimitReader(response.Body, 1048577)); e != nil {
			return identity.Identity{}, ErrProvider
		}
		if response.StatusCode == 409 {
			return identity.Identity{}, ErrConflict
		}
		if response.StatusCode != 201 {
			return identity.Identity{}, ErrProvider
		}
		location, e := url.Parse(response.Header.Get("Location"))
		expected := i.Profile.Prefix() + "/admin/realms/" + i.Profile.Realm + "/users/"
		if e != nil || location.RawQuery != "" || location.Fragment != "" || location.User != nil || !strings.HasPrefix(location.Path, expected) {
			return identity.Identity{}, ErrProvider
		}
		if location.IsAbs() && identity.Origin(location) != i.Profile.Origin() && identity.Origin(location) != strings.TrimSuffix(remote.base, i.Profile.Prefix()) {
			return identity.Identity{}, ErrProvider
		}
		subject = strings.TrimPrefix(location.Path, expected)
		if !identifier.Valid(subject) {
			return identity.Identity{}, ErrProvider
		}
	}
	var user providerUser
	status, e := remote.request(ctx, "GET", "/admin/realms/"+i.Profile.Realm+"/users/"+subject, token, "", nil, &user)
	if e != nil || status != 200 || user.ID != subject || user.Username != input.Username || !user.Enabled {
		return identity.Identity{}, ErrConflict
	}
	return identity.Identity{Issuer: i.Profile.Issuer(), Subject: user.ID, DisplayName: input.DisplayName, Username: input.Username}, nil
}
