package deployment_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"workgraph.local/platform/internal/platform/deployment"
)

func fixture(t *testing.T) (string, string) {
	t.Helper()
	parent := t.TempDir()
	compose := filepath.Join(parent, "compose.yaml")
	if e := os.WriteFile(compose, []byte("services: {}\n"), 0o600); e != nil {
		t.Fatal(e)
	}
	return filepath.Join(parent, "installation"), compose
}

func TestInstallationAcceptsSixOrderedProductionRoleOverlaysWithABoundedLimit(t *testing.T) {
	root, compose := fixture(t)
	i := deployment.Installation{Version: 1, ID: "11111111-1111-4111-8111-111111111111", Directory: root, ComposeFile: compose, HTTPPort: 8080, Image: "gitmora:local"}
	for _, name := range []string{"tasktopia", "gitlab", "identity", "identity-auth"} {
		i.Overlays = append(i.Overlays, filepath.Join(filepath.Dir(compose), name+".yaml"))
	}
	if err := i.Validate(); err != nil {
		t.Fatal("selected production roles cannot coexist", err)
	}
	i.Overlays = append(i.Overlays, i.Overlays[0])
	if i.Validate() == nil {
		t.Fatal("duplicate overlay accepted")
	}
	i.Overlays = i.Overlays[:6]
	for _, name := range []string{"seventh", "eighth", "ninth"} {
		i.Overlays = append(i.Overlays, filepath.Join(filepath.Dir(compose), name+".yaml"))
	}
	if i.Validate() == nil {
		t.Fatal("unbounded overlay chain accepted")
	}
}

func TestInstallationCreatesIndependentProtectedSecretsAndRefusesOverwriteOrRelocation(t *testing.T) {
	root, compose := fixture(t)
	state, e := deployment.Create(context.Background(), root, compose, 8080, "gitmora:local")
	if e != nil {
		t.Fatal(e)
	}
	loaded, e := deployment.Load(root)
	if e != nil || loaded.ID != state.ID || loaded.Project() == "workgraph" {
		t.Fatal("installation identity", e)
	}
	public, _ := json.Marshal(loaded)
	values := map[string][]byte{}
	for _, name := range []string{"database_url", "postgres_password", "app_password", "encryption_keys"} {
		raw, e := os.ReadFile(filepath.Join(root, "secrets", name))
		if e != nil || len(raw) == 0 {
			t.Fatal("missing credential", e)
		}
		values[name] = raw
		if bytes.Contains(public, raw) {
			t.Fatal("public state exposed a credential")
		}
		info, e := os.Stat(filepath.Join(root, "secrets", name))
		if e != nil || info.Mode().Perm() != 0o444 {
			t.Fatal("container-readable file permission", e)
		}
	}
	if bytes.Equal(values["postgres_password"], values["app_password"]) {
		t.Fatal("administrator/application passwords shared")
	}
	for _, dir := range []string{root, filepath.Join(root, "secrets")} {
		info, _ := os.Stat(dir)
		if info.Mode().Perm() != 0o700 {
			t.Fatal("private installation directory became traversable")
		}
	}
	if _, e = deployment.Create(context.Background(), root, compose, 8081, "gitmora:local"); !errors.Is(e, deployment.ErrConflict) {
		t.Fatal("existing installation overwritten", e)
	}
	after, _ := os.ReadFile(filepath.Join(root, "secrets", "app_password"))
	if !bytes.Equal(after, values["app_password"]) {
		t.Fatal("initialization rotated live credentials")
	}
	clone := filepath.Join(filepath.Dir(root), "copied")
	if e = os.Mkdir(clone, 0o700); e != nil {
		t.Fatal(e)
	}
	manifest, _ := os.ReadFile(filepath.Join(root, "installation.json"))
	if e = os.WriteFile(filepath.Join(clone, "installation.json"), manifest, 0o600); e != nil {
		t.Fatal(e)
	}
	if _, e = deployment.Load(clone); e == nil {
		t.Fatal("copied metadata redirected operations to another installation")
	}
}

func TestConcurrentInitializationPublishesOneInstallationAndInvalidInputWritesNothing(t *testing.T) {
	root, compose := fixture(t)
	var wg sync.WaitGroup
	wg.Add(2)
	outcomes := make(chan error, 2)
	for range 2 {
		go func() {
			defer wg.Done()
			_, e := deployment.Create(context.Background(), root, compose, 8080, "gitmora:local")
			outcomes <- e
		}()
	}
	wg.Wait()
	close(outcomes)
	passed, conflicted := 0, 0
	for e := range outcomes {
		if e == nil {
			passed++
		} else if errors.Is(e, deployment.ErrConflict) {
			conflicted++
		} else {
			t.Fatal(e)
		}
	}
	if passed != 1 || conflicted != 1 {
		t.Fatal("initialization race not fenced")
	}
	for _, port := range []int{0, 65536, -1} {
		target := filepath.Join(filepath.Dir(root), "invalid")
		if _, e := deployment.Create(context.Background(), target, compose, port, "gitmora:local"); e == nil {
			t.Fatal("invalid port accepted")
		}
		if _, e := os.Stat(target); !os.IsNotExist(e) {
			t.Fatal("invalid input created state")
		}
	}
}
