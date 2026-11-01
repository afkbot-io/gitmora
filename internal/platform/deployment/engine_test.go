package deployment_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"workgraph.local/platform/internal/platform/deployment"
)

type engine struct {
	args  []string
	env   map[string]string
	calls int
}

type heldEngine struct{ entered, release chan struct{} }

func (e *heldEngine) Run(context.Context, []string, map[string]string, io.Reader, io.Writer) error {
	close(e.entered)
	<-e.release
	return nil
}

func TestConcurrentMutatingOperationsCannotStopAnInstallationBeingStarted(t *testing.T) {
	root, compose := fixture(t)
	if _, e := deployment.Create(context.Background(), root, compose, 8080, "gitmora:local"); e != nil {
		t.Fatal(e)
	}
	port := &heldEngine{entered: make(chan struct{}), release: make(chan struct{})}
	s := deployment.NewService(port)
	finished := make(chan error, 1)
	go func() { finished <- s.Up(context.Background(), root) }()
	<-port.entered
	if e := s.Down(context.Background(), root); !errors.Is(e, deployment.ErrBusy) {
		t.Fatal("concurrent stop was not fenced", e)
	}
	close(port.release)
	if e := <-finished; e != nil {
		t.Fatal(e)
	}
	if e := deployment.NewService(&engine{}).Down(context.Background(), root); e != nil {
		t.Fatal("operation lease not released", e)
	}
}

func (e *engine) Run(_ context.Context, args []string, env map[string]string, _ io.Reader, _ io.Writer) error {
	e.calls++
	e.args = append([]string(nil), args...)
	e.env = env
	return nil
}

func TestDeploymentUsesExactProjectAndNeverDeletesDataOrPassesSecretsToTheEngine(t *testing.T) {
	root, compose := fixture(t)
	state, e := deployment.Create(context.Background(), root, compose, 8080, "gitmora:local")
	if e != nil {
		t.Fatal(e)
	}
	port := &engine{}
	s := deployment.NewService(port)
	if e = s.Up(context.Background(), root); e != nil {
		t.Fatal(e)
	}
	joined := strings.Join(port.args, " ")
	if !strings.Contains(joined, state.Project()) || !strings.Contains(joined, "--no-build") || !strings.Contains(joined, "--pull never") || port.env["WORKGRAPH_STATE_DIR"] != root {
		t.Fatal("unbounded/ambiguous deployment")
	}
	for key, value := range port.env {
		if strings.Contains(key, "PASSWORD") || strings.Contains(key, "TOKEN") || strings.Contains(value, "postgres://") {
			t.Fatal("engine environment exposed secrets")
		}
	}
	if e = s.Down(context.Background(), root); e != nil {
		t.Fatal(e)
	}
	for _, arg := range port.args {
		if arg == "--volumes" || arg == "-v" {
			t.Fatal("ordinary stop destroys data")
		}
	}
	before := port.calls
	if e = s.Admin(context.Background(), root, []string{"serve"}, io.Discard); e == nil || port.calls != before {
		t.Fatal("unrestricted admin command")
	}
	if e = s.Admin(context.Background(), root, []string{"issue-token", "--user-id", "11111111-1111-4111-8111-111111111111"}, io.Discard); e != nil {
		t.Fatal(e)
	}
	before = port.calls
	if e = s.Admin(context.Background(), root, []string{"users", "create"}, io.Discard); e == nil || port.calls != before {
		t.Fatal("interactive provider authority entered API container")
	}
	if e = s.Admin(context.Background(), root, []string{"users", "bind-subject", "--issuer", "https://idp.internal", "--subject", "exact-subject", "--name", "Engineer"}, io.Discard); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(strings.Join(port.args, " "), "exec -T api /gitmorad users bind-subject") {
		t.Fatal("account binding escaped selected API")
	}
	if e = s.Admin(context.Background(), root, []string{"issue-token", "--user-id", "11111111-1111-4111-8111-111111111111"}, io.Discard); e != nil {
		t.Fatal(e)
	}

	if !strings.Contains(strings.Join(port.args, " "), "exec -T api /gitmorad issue-token") {
		t.Fatal("bootstrap secrets would be stored in one-off container logs")
	}
}

func TestExplicitReviewedOverlayStaysPinnedInTheInstallationCommand(t *testing.T) {
	root, compose := fixture(t)
	overlay := filepath.Join(filepath.Dir(compose), "connectivity.yaml")
	if e := os.WriteFile(overlay, []byte("services: {}\n"), 0o600); e != nil {
		t.Fatal(e)
	}
	if _, e := deployment.Create(context.Background(), root, compose, 8080, "gitmora:local", overlay); e != nil {
		t.Fatal(e)
	}
	port := &engine{}
	if e := deployment.NewService(port).Up(context.Background(), root); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(strings.Join(port.args, " "), "--file "+overlay) {
		t.Fatal("reviewed connectivity overlay was silently discarded")
	}
}

func TestPrivateComposeEnvironmentIsSelectedOnEveryOperation(t *testing.T) {
	root, compose := fixture(t)
	if _, err := deployment.Create(context.Background(), root, compose, 8080, "gitmora:local"); err != nil {
		t.Fatal(err)
	}
	policy := filepath.Join(root, "compose.env")
	if err := os.WriteFile(policy, []byte("GITMORA_PUBLIC_HOST=gitmora.example.test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	port := &engine{}
	s := deployment.NewService(port)
	for _, operation := range []func() error{
		func() error { return s.Up(context.Background(), root) },
		func() error { return s.Status(context.Background(), root, io.Discard) },
		func() error {
			return s.Admin(context.Background(), root, []string{"grant-operator", "--user-id", "11111111-1111-4111-8111-111111111111"}, io.Discard)
		},
		func() error { return s.Down(context.Background(), root) },
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
		if len(port.args) < 3 || port.args[0] != "compose" || port.args[1] != "--env-file" || port.args[2] != policy {
			t.Fatal("installation policy was lost", port.args)
		}
	}
	before := port.calls
	if err := os.Chmod(policy, 0644); err != nil {
		t.Fatal(err)
	}
	if err := s.Up(context.Background(), root); !errors.Is(err, deployment.ErrFilesystem) || port.calls != before {
		t.Fatal("unsafe policy reached Docker", err)
	}
	if err := os.Remove(policy); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(compose, policy); err != nil {
		t.Fatal(err)
	}
	if err := s.Up(context.Background(), root); !errors.Is(err, deployment.ErrFilesystem) || port.calls != before {
		t.Fatal("symlink policy reached Docker", err)
	}
}
