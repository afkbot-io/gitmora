package identityhost

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type execution struct {
	args []string
	env  map[string]string
}
type engine struct {
	calls   []execution
	fail    bool
	foreign bool
}

func (e *engine) Run(_ context.Context, args []string, env map[string]string, _ io.Reader, out io.Writer) error {
	e.calls = append(e.calls, execution{append([]string(nil), args...), env})
	if args[0] == "inspect" {
		project := strings.TrimSuffix(args[len(args)-1], "-gateway-1")
		if e.foreign {
			project = "foreign"
		}
		_, err := io.WriteString(out, `{"project":"`+project+`","service":"gateway","image":"gitmora:verified","running":true,"ports":{"8080/tcp":[{"HostIp":"127.0.0.1","HostPort":"18081"}]}}`)
		return err
	}
	if e.fail {
		return errors.New("private backend diagnostic must not escape")
	}
	_, err := io.WriteString(out, "[]\n")
	return err
}

type administrator struct {
	fail    bool
	foreign bool
}

func (a administrator) Finalize(_ context.Context, i Installation) (Proof, error) {
	if a.fail {
		return Proof{}, ErrProvider
	}
	p := Proof{RealmID: i.RealmID, ClientUUID: i.ClientUUID, UserID: i.UserID, MasterUserID: i.MasterUserID, BootstrapRemoved: true}
	if p.MasterUserID == "" {
		p.MasterUserID = "88888888-8888-4888-8888-888888888888"
	}
	if a.foreign {
		p.RealmID = "foreign"
	}
	return p, nil
}
func prepared(t *testing.T) Installation {
	t.Helper()
	parent := t.TempDir()
	compose := filepath.Join(parent, "compose.yaml")
	os.WriteFile(compose, []byte("services: {}\n"), 0600)
	i, err := Create(context.Background(), filepath.Join(parent, "state"), compose, Profile{PublicURL: "https://identity.internal", ApplicationURL: "https://gitmora.internal", Realm: "gitmora", ClientID: "gitmora", HTTPPort: 18081, IdentityImage: "gitmora-identity:26.8.1", GatewayImage: "gitmora:verified"})
	if err != nil {
		t.Fatal(err)
	}
	return i
}
func TestBootstrapFinalizationSelectsEmptyImportAndNeverReintroducesBootstrapOnRetry(t *testing.T) {
	i := prepared(t)
	e := &engine{}
	s := NewService(e, administrator{})
	if err := s.Up(context.Background(), i.Directory); err != nil {
		t.Fatal(err)
	}
	if e.calls[0].env["GITMORA_IDENTITY_CONFIG"] != "keycloak-bootstrap.conf" || e.calls[0].env["GITMORA_IDENTITY_IMPORT"] != "bootstrap" {
		t.Fatal("missing bootstrap selection")
	}
	updated, err := s.Finalize(context.Background(), i.Directory)
	if err != nil || updated.Phase != "active" {
		t.Fatalf("finalize %v", err)
	}
	last := e.calls[len(e.calls)-1]
	if last.env["GITMORA_IDENTITY_CONFIG"] != "keycloak-active.conf" || last.env["GITMORA_IDENTITY_IMPORT"] != "runtime" {
		t.Fatal("bootstrap material still mounted")
	}
	for _, call := range e.calls {
		if !strings.Contains(strings.Join(call.args, " "), i.Project()) || strings.Contains(strings.Join(call.args, " "), "--volumes") {
			t.Fatal("wrong project/destructive ordinary operation")
		}
		for k := range call.env {
			if strings.Contains(strings.ToLower(k), "password") || strings.Contains(strings.ToLower(k), "secret") {
				t.Fatal("credential-bearing process environment")
			}
		}
	}
	if _, err := s.Finalize(context.Background(), i.Directory); err != nil {
		t.Fatal("same finalization did not replay", err)
	}
	if loaded, err := Load(i.Directory); err != nil || loaded.Phase != "active" {
		t.Fatal("active floor lost")
	}
	files, err := os.ReadDir(filepath.Join(i.Directory, "bootstrap"))
	if err != nil || len(files) != 0 {
		t.Fatal("an old running container can re-import retired user passwords")
	}
	if err := s.Down(context.Background(), i.Directory); err != nil {
		t.Fatal(err)
	}
	if len(e.calls) == 0 {
		t.Fatal("engine was not used")
	}
}

func TestFinalizationNeverSendsProviderCredentialsToAnotherInstallation(t *testing.T) {
	i := prepared(t)
	e := &engine{foreign: true}
	if _, err := NewService(e, administrator{}).Finalize(context.Background(), i.Directory); err == nil {
		t.Fatal("unowned provider gateway accepted")
	}
	loaded, _ := Load(i.Directory)
	if loaded.Phase != "bootstrap" {
		t.Fatal("foreign endpoint changed local authority")
	}
}
func TestFailedIdentityFinalizationKeepsRecoverableScopeAndActiveSelectionSurvivesEngineFailure(t *testing.T) {
	for _, a := range []administrator{{fail: true}, {foreign: true}} {
		i := prepared(t)
		e := &engine{}
		if _, err := NewService(e, a).Finalize(context.Background(), i.Directory); err == nil {
			t.Fatal("failed/foreign authority activated")
		}
		loaded, _ := Load(i.Directory)
		if loaded.Phase != "bootstrap" || len(e.calls) != 1 || e.calls[0].args[0] != "inspect" {
			t.Fatal("failed proof changed state")
		}
	}
	i := prepared(t)
	e := &engine{fail: true}
	if _, err := NewService(e, administrator{}).Finalize(context.Background(), i.Directory); err == nil || strings.Contains(err.Error(), "private backend") {
		t.Fatal("engine failure escaped")
	}
	loaded, _ := Load(i.Directory)
	if loaded.Phase != "active" {
		t.Fatal("uncertain restart can return to bootstrap import")
	}
	e.fail = false
	if err := NewService(e, administrator{}).Up(context.Background(), i.Directory); err != nil {
		t.Fatal(err)
	}
	if e.calls[len(e.calls)-1].env["GITMORA_IDENTITY_IMPORT"] != "runtime" {
		t.Fatal("restart reintroduced bootstrap source")
	}
	var out bytes.Buffer
	if err := NewService(e, nil).Status(context.Background(), i.Directory, &out); err != nil {
		t.Fatal(err)
	}
}
