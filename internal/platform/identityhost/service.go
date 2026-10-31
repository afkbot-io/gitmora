package identityhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gofrs/flock"
	"workgraph.local/platform/internal/identifier"
	"workgraph.local/platform/internal/jsonwire"
	"workgraph.local/platform/internal/platform/deployment"
)

// Proof binds completed provider provisioning to exactly this private installation.
type Proof struct {
	RealmID, ClientUUID, UserID, MasterUserID string
	BootstrapRemoved                          bool
}

// Administrator retires the initial provider authority after verifying permanent recovery and exact realm scope.
type Administrator interface {
	Finalize(context.Context, Installation) (Proof, error)
}

// Service reuses the audited container process port and keeps identity state separate from application installations.
type Service struct {
	engine deployment.Executor
	admin  Administrator
}

func NewService(engine deployment.Executor, admin Administrator) *Service {
	return &Service{engine: engine, admin: admin}
}

func selected(i Installation) ([]string, map[string]string) {
	config, importDirectory := "keycloak-bootstrap.conf", "bootstrap"
	if i.Phase == "active" {
		config, importDirectory = "keycloak-active.conf", "runtime"
	}
	return []string{"compose", "--project-name", i.Project(), "--project-directory", filepath.Dir(i.ComposeFile), "--file", i.ComposeFile}, map[string]string{
		"GITMORA_IDENTITY_STATE": i.Directory, "GITMORA_IDENTITY_HTTP_PORT": strconv.Itoa(i.Profile.HTTPPort),
		"GITMORA_IDENTITY_REALM": i.Profile.Realm, "GITMORA_IDENTITY_IMAGE": i.Profile.IdentityImage,
		"GITMORA_IDENTITY_GATEWAY_IMAGE": i.Profile.GatewayImage, "GITMORA_IDENTITY_PUBLIC_URL": i.Profile.PublicURL,
		"GITMORA_IDENTITY_PUBLIC_ORIGIN": i.Profile.Origin(), "GITMORA_IDENTITY_PREFIX": i.Profile.Prefix(),
		"GITMORA_IDENTITY_CONFIG": config, "GITMORA_IDENTITY_IMPORT": importDirectory,
	}
}
func (s *Service) execute(ctx context.Context, i Installation, output io.Writer, args ...string) error {
	if s.engine == nil {
		return deployment.ErrEngine
	}
	prefix, env := selected(i)
	if err := s.engine.Run(ctx, append(prefix, args...), env, nil, output); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return deployment.ErrEngine
	}
	return nil
}
func owned(ctx context.Context, root string) (Installation, func(), error) {
	i, err := Load(root)
	if err != nil {
		return Installation{}, nil, err
	}
	if ctx.Err() != nil {
		return Installation{}, nil, ctx.Err()
	}
	path := filepath.Join(root, ".operations.lock")
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		f, createErr := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if createErr == nil {
			f.Close()
		} else if !errors.Is(createErr, os.ErrExist) {
			return Installation{}, nil, ErrFilesystem
		}
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return Installation{}, nil, ErrFilesystem
	}
	lock := flock.New(path)
	ok, err := lock.TryLock()
	if err != nil {
		return Installation{}, nil, ErrFilesystem
	}
	if !ok {
		return Installation{}, nil, ErrBusy
	}
	i, err = Load(root)
	if err != nil {
		_ = lock.Unlock()
		return Installation{}, nil, err
	}
	return i, func() { _ = lock.Unlock() }, nil
}

// Up uses only prebuilt images and the durable selected phase. Active state can never mount the old realm import.
func (s *Service) Up(ctx context.Context, root string) error {
	i, release, err := owned(ctx, root)
	if err != nil {
		return err
	}
	defer release()
	return s.execute(ctx, i, io.Discard, "up", "--detach", "--no-build", "--pull", "never", "--wait", "--wait-timeout", "180")
}

// Down stops only this identity installation and preserves its persistent database volume.
func (s *Service) Down(ctx context.Context, root string) error {
	i, release, err := owned(ctx, root)
	if err != nil {
		return err
	}
	defer release()
	return s.execute(ctx, i, io.Discard, "down")
}

// Status is passive public container metadata; it does not read private bootstrap/operator material.
func (s *Service) Status(ctx context.Context, root string, out io.Writer) error {
	i, err := Load(root)
	if err != nil {
		return err
	}
	return s.execute(ctx, i, out, "ps", "--all", "--format", "json")
}
func save(i Installation) error {
	f, err := os.CreateTemp(i.Directory, ".identity-*.new")
	if err != nil {
		return ErrFilesystem
	}
	name := f.Name()
	defer os.Remove(name)
	raw, _ := json.MarshalIndent(i, "", "  ")
	if _, err = f.Write(append(raw, '\n')); err != nil {
		f.Close()
		return ErrFilesystem
	}
	if f.Chmod(0600) != nil || f.Sync() != nil {
		f.Close()
		return ErrFilesystem
	}
	if f.Close() != nil {
		return ErrFilesystem
	}
	if os.Rename(name, filepath.Join(i.Directory, "identity.json")) != nil {
		return ErrFilesystem
	}
	return syncDir(i.Directory)
}

type boundedMetadata struct{ buffer bytes.Buffer }

func (w *boundedMetadata) Write(raw []byte) (int, error) {
	if len(raw) > 65536-w.buffer.Len() {
		return 0, ErrProvider
	}
	return w.buffer.Write(raw)
}
func (s *Service) verifyGateway(ctx context.Context, i Installation) error {
	if s.engine == nil {
		return deployment.ErrEngine
	}
	format := `{"project":{{json (index .Config.Labels "com.docker.compose.project")}},"service":{{json (index .Config.Labels "com.docker.compose.service")}},"image":{{json .Config.Image}},"running":{{json .State.Running}},"ports":{{json .NetworkSettings.Ports}}}`
	var output boundedMetadata
	if err := s.engine.Run(ctx, []string{"inspect", "--format", format, i.Project() + "-gateway-1"}, nil, nil, &output); err != nil {
		return ErrProvider
	}
	var view struct {
		Project string `json:"project"`
		Service string `json:"service"`
		Image   string `json:"image"`
		Running bool   `json:"running"`
		Ports   map[string][]struct {
			IP   string `json:"HostIp"`
			Port string `json:"HostPort"`
		} `json:"ports"`
	}
	if jsonwire.DecodeExternalObject(output.buffer.Bytes(), &view) != nil || view.Project != i.Project() || view.Service != "gateway" || view.Image != i.Profile.GatewayImage || !view.Running || len(view.Ports) != 1 {
		return ErrConflict
	}
	bindings := view.Ports["8080/tcp"]
	if len(bindings) != 1 || bindings[0].IP != "127.0.0.1" || bindings[0].Port != strconv.Itoa(i.Profile.HTTPPort) {
		return ErrConflict
	}
	return nil
}

func retireImport(i Installation) error {
	source := filepath.Join(i.Directory, "bootstrap", "gitmora-realm.json")
	target := filepath.Join(i.Directory, "secrets", "retired-realm-import.json")
	info, err := os.Lstat(source)
	if os.IsNotExist(err) {
		info, err = os.Lstat(target)
		if err != nil || !info.Mode().IsRegular() {
			return ErrFilesystem
		}
		return nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return ErrFilesystem
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		return ErrConflict
	}
	if os.Rename(source, target) != nil {
		return ErrFilesystem
	}
	if syncDir(filepath.Dir(source)) != nil || syncDir(filepath.Dir(target)) != nil {
		return ErrFilesystem
	}
	return nil
}

// Finalize verifies permanent administration, exact imported identities and bootstrap retirement before selecting active files.
// Selection is durable before recreation; interruption must resume active runtime, never reintroduce bootstrap input.
func (s *Service) Finalize(ctx context.Context, root string) (Installation, error) {
	i, release, err := owned(ctx, root)
	if err != nil {
		return Installation{}, err
	}
	defer release()
	if s.admin == nil {
		return Installation{}, ErrProvider
	}
	// Verify the owned container/loopback binding before any credential is sent to its administration endpoint.
	if err := s.verifyGateway(ctx, i); err != nil {
		return Installation{}, err
	}
	proof, err := s.admin.Finalize(ctx, i)
	if err != nil {
		return Installation{}, err
	}
	if proof.RealmID != i.RealmID || proof.ClientUUID != i.ClientUUID || proof.UserID != i.UserID || !identifier.Valid(proof.MasterUserID) || (i.MasterUserID != "" && proof.MasterUserID != i.MasterUserID) || !proof.BootstrapRemoved {
		return Installation{}, ErrConflict
	}
	if ctx.Err() != nil {
		return Installation{}, ctx.Err()
	}
	// Retire the directory input too: a still-running old container must not retain a reusable user import after an interrupted recreation.
	if err := retireImport(i); err != nil {
		return Installation{}, err
	}
	if i.Phase != "active" {
		i.Phase = "active"
		i.MasterUserID = proof.MasterUserID
		if err := save(i); err != nil {
			return Installation{}, err
		}
	}
	if err := s.execute(ctx, i, io.Discard, "up", "--detach", "--no-build", "--pull", "never", "--wait", "--wait-timeout", "180"); err != nil {
		return Installation{}, err
	}
	return i, nil
}
