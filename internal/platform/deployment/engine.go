package deployment

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/gofrs/flock"
)

// Executor is the operator application's process port; adapters never invoke a shell or expose private diagnostics.
type Executor interface {
	Run(context.Context, []string, map[string]string, io.Reader, io.Writer) error
}

// Service orchestrates only an explicitly loaded installation and preserves its persistent volumes on normal stop.
type Service struct{ engine Executor }

// NewService supplies the explicit engine port; initialization remains engine-independent.
func NewService(engine Executor) *Service { return &Service{engine: engine} }

func composeArgs(i Installation, args ...string) ([]string, map[string]string) {
	prefix := []string{"compose", "--project-name", i.Project(), "--project-directory", filepath.Dir(i.ComposeFile), "--file", i.ComposeFile}
	for _, path := range i.Overlays {
		prefix = append(prefix, "--file", path)
	}
	env := map[string]string{"WORKGRAPH_STATE_DIR": i.Directory, "WORKGRAPH_HTTP_PORT": strconv.Itoa(i.HTTPPort), "WORKGRAPH_IMAGE": i.Image}
	return append(prefix, args...), env
}

func (s *Service) compose(ctx context.Context, root string, input io.Reader, output io.Writer, args ...string) error {
	i, e := Load(root)
	if e != nil {
		return e
	}
	if s.engine == nil {
		return ErrEngine
	}
	if len(args) > 0 && args[0] != "ps" {
		if e = ctx.Err(); e != nil {
			return e
		}
		path := filepath.Join(root, ".operations.lock")
		if info, err := os.Lstat(path); err == nil && (!info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0) {
			return ErrFilesystem
		} else if err != nil && !os.IsNotExist(err) {
			return ErrFilesystem
		}
		lease := flock.New(path)
		owned, err := lease.TryLock()
		if err != nil {
			return ErrFilesystem
		}
		if !owned {
			return ErrBusy
		}
		defer lease.Unlock()
	}
	command, env := composeArgs(i, args...)
	return s.engine.Run(ctx, command, env, input, output)
}

// Up uses prebuilt local images and waits for database, migration and API readiness; it never pulls at runtime.
func (s *Service) Up(ctx context.Context, root string) error {
	return s.compose(ctx, root, nil, io.Discard, "up", "--detach", "--no-build", "--pull", "never", "--wait", "--wait-timeout", "90")
}

// Down stops only this project and deliberately preserves every data volume.
func (s *Service) Down(ctx context.Context, root string) error {
	return s.compose(ctx, root, nil, io.Discard, "down")
}

// Status exposes bounded container metadata, not an application's process environment or logs.
func (s *Service) Status(ctx context.Context, root string, out io.Writer) error {
	return s.compose(ctx, root, nil, out, "ps", "--all", "--format", "json")
}

// Admin attaches to an existing API container: first-only credentials never enter a one-off container logging driver.
func (s *Service) Admin(ctx context.Context, root string, args []string, out io.Writer) error {
	if len(args) == 0 {
		return ErrInvalid
	}
	switch args[0] {
	case "init-admin", "bootstrap", "issue-token", "grant-operator":
	case "users":
		if len(args) < 2 || args[1] != "bind-subject" {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return s.compose(ctx, root, nil, out, append([]string{"exec", "-T", "api", "/gitmorad"}, args...)...)
}

// Docker implements the process port using the operator's Docker installation/context and exact argv.
type Docker struct{ binary string }

// NewDocker resolves the CLI once; no shell or automatic Docker installation is performed.
func NewDocker() (*Docker, error) {
	binary, e := exec.LookPath("docker")
	if e != nil || !filepath.IsAbs(binary) {
		return nil, ErrEngine
	}
	return &Docker{binary: binary}, nil
}

type discardedDiagnostics struct{}

func (discardedDiagnostics) Write(p []byte) (int, error) { return len(p), nil }

// Run preserves output only at its explicitly selected destination; remote/backend error text is never returned.
func (d *Docker) Run(ctx context.Context, args []string, overrides map[string]string, input io.Reader, output io.Writer) error {
	env := map[string]string{}
	for _, pair := range os.Environ() {
		key, value, ok := strings.Cut(pair, "=")
		if ok {
			env[key] = value
		}
	}
	for key, value := range overrides {
		env[key] = value
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	command := exec.CommandContext(ctx, d.binary, args...)
	for _, key := range keys {
		command.Env = append(command.Env, key+"="+env[key])
	}
	command.Stdin = input
	command.Stdout = output
	command.Stderr = discardedDiagnostics{}
	if e := command.Run(); e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrEngine
	}
	return nil
}

var _ Executor = (*Docker)(nil)
