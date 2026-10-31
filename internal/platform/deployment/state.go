// Package deployment owns operator-only installation state and explicit container/recovery orchestration.
// It is an infrastructure boundary; business authorization remains in the platform's domain/application modules.
package deployment

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"workgraph.local/platform/internal/identifier"
	"workgraph.local/platform/internal/jsonwire"
)

var (
	ErrInvalid    = errors.New("invalid deployment configuration or recovery input")
	ErrConflict   = errors.New("deployment state already exists or conflicts")
	ErrFilesystem = errors.New("private deployment state unavailable")
	ErrEngine     = errors.New("container operation failed; inspect the selected installation")
	ErrBusy       = errors.New("another deployment operation is active for this installation")
)
var imageName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9./:@_-]{0,255}$`)

// Installation is non-secret operator metadata, pinned to its original absolute directory and unique Compose project.
type Installation struct {
	Version     int      `json:"version"`
	ID          string   `json:"id"`
	Directory   string   `json:"directory"`
	ComposeFile string   `json:"compose_file"`
	HTTPPort    int      `json:"http_port"`
	Image       string   `json:"image"`
	Overlays    []string `json:"overlays,omitempty"`
}

// Project uses the full immutable UUID so installations never share a volume/container namespace by a short prefix.
func (i Installation) Project() string { return "gitmora-" + strings.ReplaceAll(i.ID, "-", "") }

func absolute(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && len(path) <= 4096 && !strings.ContainsAny(path, "\x00\r\n")
}

// Validate rejects relocated/ambiguous installation metadata before an engine can touch another project.
func (i Installation) Validate() error {
	if i.Version != 1 || !identifier.Valid(i.ID) || !absolute(i.Directory) || i.Directory == string(filepath.Separator) || !absolute(i.ComposeFile) || i.HTTPPort < 1 || i.HTTPPort > 65535 || !imageName.MatchString(i.Image) {
		return ErrInvalid
	}
	if len(i.Overlays) > 8 {
		return ErrInvalid
	}
	for index, path := range i.Overlays {
		if !absolute(path) || path == i.ComposeFile || slices.Contains(i.Overlays[:index], path) {
			return ErrInvalid
		}
	}
	return nil
}

func entropy() (string, error) {
	var raw [32]byte
	if _, e := rand.Read(raw[:]); e != nil {
		return "", ErrFilesystem
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func writePrivate(path string, raw []byte, mode os.FileMode) error {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if e != nil {
		return ErrFilesystem
	}
	defer f.Close()
	if _, e = f.Write(raw); e != nil {
		return ErrFilesystem
	}
	if e = f.Chmod(mode); e != nil {
		return ErrFilesystem
	}
	if e = f.Sync(); e != nil {
		return ErrFilesystem
	}
	return nil
}

func syncDir(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return ErrFilesystem
	}
	defer f.Close()
	if e = f.Sync(); e != nil {
		return ErrFilesystem
	}
	return nil
}

// Create initializes only a new private directory, with independent application/admin/keyring entropy.
// Files are container-readable under a non-traversable host directory; Compose bind secrets retain source permissions.
func Create(ctx context.Context, root, compose string, port int, image string, overlays ...string) (Installation, error) {
	id, e := identifier.New()
	if e != nil {
		return Installation{}, ErrFilesystem
	}
	i := Installation{Version: 1, ID: id, Directory: root, ComposeFile: compose, HTTPPort: port, Image: image, Overlays: slices.Clone(overlays)}
	if e = i.Validate(); e != nil {
		return Installation{}, e
	}
	info, e := os.Stat(compose)
	if e != nil || !info.Mode().IsRegular() {
		return Installation{}, ErrInvalid
	}
	for _, path := range i.Overlays {
		info, e = os.Stat(path)
		if e != nil || !info.Mode().IsRegular() {
			return Installation{}, ErrInvalid
		}
	}
	if e = ctx.Err(); e != nil {
		return Installation{}, e
	}
	if e = os.Mkdir(root, 0o700); e != nil {
		if os.IsExist(e) {
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
	secrets := filepath.Join(root, "secrets")
	if e = os.Mkdir(secrets, 0o700); e != nil {
		return Installation{}, ErrFilesystem
	}
	admin, e := entropy()
	if e != nil {
		return Installation{}, e
	}
	app, e := entropy()
	if e != nil {
		return Installation{}, e
	}
	var key [32]byte
	if _, e = rand.Read(key[:]); e != nil {
		return Installation{}, ErrFilesystem
	}
	keyring, _ := json.Marshal(map[string]string{"primary": base64.StdEncoding.EncodeToString(key[:])})
	files := map[string][]byte{
		"postgres_password": []byte(admin + "\n"), "app_password": []byte(app + "\n"),
		"database_url":    []byte("postgres://workgraph:" + app + "@postgres:5432/workgraph?sslmode=disable\n"),
		"encryption_keys": append(keyring, '\n'), "oidc_client_secret": {},
	}
	for name, raw := range files {
		if e = ctx.Err(); e != nil {
			return Installation{}, e
		}
		if e = writePrivate(filepath.Join(secrets, name), raw, 0o444); e != nil {
			return Installation{}, e
		}
	}
	if e = syncDir(secrets); e != nil {
		return Installation{}, e
	}
	if e = writePrivate(filepath.Join(root, "runtime.env"), []byte("# Non-secret runtime settings only. Credentials use explicitly enabled /run/secrets/* files.\n"), 0o600); e != nil {
		return Installation{}, e
	}
	raw, _ := json.MarshalIndent(i, "", "  ")
	if e = writePrivate(filepath.Join(root, "installation.json"), append(raw, '\n'), 0o600); e != nil {
		return Installation{}, e
	}
	if e = syncDir(root); e != nil {
		return Installation{}, e
	}
	if e = syncDir(filepath.Dir(root)); e != nil {
		return Installation{}, e
	}
	complete = true
	return i, nil
}

// Load reads bounded strict metadata and refuses symlinked/insecure or relocated installation roots.
func Load(root string) (Installation, error) {
	if !absolute(root) {
		return Installation{}, ErrInvalid
	}
	info, e := os.Lstat(root)
	if e != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return Installation{}, ErrFilesystem
	}
	path := filepath.Join(root, "installation.json")
	info, e = os.Lstat(path)
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return Installation{}, ErrFilesystem
	}
	f, e := os.Open(path)
	if e != nil {
		return Installation{}, ErrFilesystem
	}
	defer f.Close()
	raw, e := io.ReadAll(io.LimitReader(f, 16385))
	if e != nil || len(raw) > 16384 {
		return Installation{}, ErrInvalid
	}
	var i Installation
	if jsonwire.DecodeObject(raw, &i) != nil || i.Validate() != nil || i.Directory != root {
		return Installation{}, ErrInvalid
	}
	return i, nil
}
