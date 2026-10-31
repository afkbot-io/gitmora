// Package keychain stores native credentials only through Darwin Keychain or Linux Secret Service.
package keychain

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"workgraph.local/platform/internal/identity/native"
)

const serviceName = "Workgraph CLI v1"

type commandRunner interface {
	Run(context.Context, string, []string, []byte) ([]byte, int, error)
}

// Store uses a per-OS-user/server namespace; filesystem entries contain only empty authentication lock files.
type Store struct {
	platform, command, locks, keychain string
	runner                             commandRunner
}

// New selects a real OS secret service with no file fallback. Optional Darwin keychain selection remains OS-protected.
func New() (*Store, error) {
	root, e := os.UserConfigDir()
	if e != nil || !filepath.IsAbs(root) {
		return nil, native.ErrVault
	}
	command := ""
	selected := os.Getenv("WORKGRAPH_KEYCHAIN_PATH")
	switch runtime.GOOS {
	case "darwin":
		command = "/usr/bin/security"
		if selected != "" {
			if !filepath.IsAbs(selected) || len(selected) > 2048 || strings.ContainsAny(selected, "\x00\r\n") {
				return nil, native.ErrVault
			}
			info, err := os.Lstat(selected)
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
				return nil, native.ErrVault
			}
		}
	case "linux":
		if selected != "" {
			return nil, native.ErrVault
		}
		command, e = exec.LookPath("secret-tool")
		if e != nil || !filepath.IsAbs(command) {
			return nil, native.ErrVault
		}
	default:
		return nil, native.ErrVault
	}
	return newStore(runtime.GOOS, command, filepath.Join(root, "workgraph", "auth"), selected, processRunner{}), nil
}

func newStore(platform, command, locks, keychain string, runner commandRunner) *Store {
	return &Store{platform: platform, command: command, locks: locks, keychain: keychain, runner: runner}
}

func serverKey(server string) (string, string, error) {
	canonical, e := native.CanonicalServer(server)
	if e != nil {
		return "", "", e
	}
	sum := sha256.Sum256([]byte(canonical))
	return canonical, hex.EncodeToString(sum[:]), nil
}

func (s *Store) run(ctx context.Context, args []string, input []byte) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.runner.Run(ctx, s.command, args, input)
}

// Load reads a single known owned namespace and rejects malformed or differently bound records.
func (s *Store) Load(ctx context.Context, server string) (native.Record, error) {
	canonical, key, e := serverKey(server)
	if e != nil {
		return native.Record{}, e
	}
	var args []string
	missing := 1
	if s.platform == "darwin" {
		args = []string{"find-generic-password", "-s", serviceName, "-a", key, "-w"}
		if s.keychain != "" {
			args = append(args, s.keychain)
		}
		missing = 44
	} else {
		args = []string{"lookup", "service", serviceName, "server", key}
	}
	raw, code, e := s.run(ctx, args, nil)
	if e != nil {
		return native.Record{}, native.ErrVault
	}
	if code == missing {
		return native.Record{}, native.ErrNotFound
	}
	if code != 0 {
		return native.Record{}, native.ErrVault
	}
	decoded, e := base64.RawURLEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if e != nil {
		return native.Record{}, native.ErrInvalid
	}
	return native.DecodeSecretStoreRecord(canonical, decoded)
}

// Save writes an opaque encoded envelope through stdin and verifies native readback before acknowledging success.
func (s *Store) Save(ctx context.Context, r native.Record) error {
	if e := r.Validate(); e != nil {
		return e
	}
	canonical, key, e := serverKey(r.Server)
	if e != nil {
		return e
	}
	if _, e = s.Load(ctx, canonical); e != nil && !errors.Is(e, native.ErrNotFound) {
		return e
	}
	raw, e := native.EncodeSecretStoreRecord(r)
	if e != nil {
		return e
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	var args []string
	var input []byte
	if s.platform == "darwin" {
		args = []string{"-i"}
		line := "add-generic-password -U -s " + strconv.Quote(serviceName) + " -a " + key + " -w " + payload
		if s.keychain != "" {
			line += " " + strconv.Quote(s.keychain)
		}
		input = []byte(line + "\n")
	} else {
		args = []string{"store", "--label=Workgraph CLI", "service", serviceName, "server", key}
		input = []byte(payload)
	}
	_, code, e := s.run(ctx, args, input)
	if e != nil || code != 0 {
		return native.ErrVault
	}
	back, e := s.Load(ctx, canonical)
	if e != nil {
		return native.ErrVault
	}
	encoded, e := native.EncodeSecretStoreRecord(back)
	if e != nil || !bytes.Equal(raw, encoded) {
		return native.ErrVault
	}
	return nil
}

// Delete forgets only this application's exact server slot. Missing entries are already forgotten.
func (s *Store) Delete(ctx context.Context, server string) error {
	_, key, e := serverKey(server)
	if e != nil {
		return e
	}
	var args []string
	missing := 1
	if s.platform == "darwin" {
		args = []string{"delete-generic-password", "-s", serviceName, "-a", key}
		if s.keychain != "" {
			args = append(args, s.keychain)
		}
		missing = 44
	} else {
		args = []string{"clear", "service", serviceName, "server", key}
	}
	_, code, e := s.run(ctx, args, nil)
	if e != nil || code != 0 && code != missing {
		return native.ErrVault
	}
	return nil
}

// Lock serializes explicit login/logout for a canonical server; hooks/status do not acquire it or start login.
func (s *Store) Lock(ctx context.Context, server string) (func(), error) {
	_, key, e := serverKey(server)
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(s.locks, 0o700); e != nil {
		return nil, native.ErrVault
	}
	info, e := os.Lstat(s.locks)
	if e != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return nil, native.ErrVault
	}
	path := filepath.Join(s.locks, key+".lock")
	if info, e = os.Lstat(path); e == nil && (!info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0) {
		return nil, native.ErrVault
	} else if e != nil && !os.IsNotExist(e) {
		return nil, native.ErrVault
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	lock := flock.New(path)
	ok, e := lock.TryLock()
	if e != nil {
		return nil, native.ErrVault
	}
	if !ok {
		return nil, native.ErrBusy
	}
	return func() { _ = lock.Unlock() }, nil
}

type processRunner struct{}
type limitedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.buffer.Len() {
		b.exceeded = true
		return 0, native.ErrVault
	}
	return b.buffer.Write(p)
}

// Run never returns backend text as an error and bounds both private output streams.
func (processRunner) Run(ctx context.Context, command string, args []string, input []byte) ([]byte, int, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Stdin = bytes.NewReader(input)
	out, diagnostic := &limitedBuffer{limit: 32768}, &limitedBuffer{limit: 32768}
	cmd.Stdout = out
	cmd.Stderr = diagnostic
	e := cmd.Run()
	if out.exceeded || diagnostic.exceeded || ctx.Err() != nil {
		return nil, -1, native.ErrVault
	}
	if e == nil {
		return out.buffer.Bytes(), 0, nil
	}
	var exit *exec.ExitError
	if errors.As(e, &exit) {
		code := exit.ExitCode()
		if code == 1 && diagnostic.buffer.Len() > 0 {
			return nil, code, native.ErrVault
		}
		return out.buffer.Bytes(), code, nil
	}
	return nil, -1, native.ErrVault
}

var _ io.Writer = (*limitedBuffer)(nil)
