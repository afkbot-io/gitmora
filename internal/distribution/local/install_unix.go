//go:build unix

// Package local installs verified-by-the-caller client bytes at a stable ordinary executable path.
package local

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"

	"workgraph.local/platform/internal/distribution/domain"
)

const maxExecutable = 128 << 20

// Install copies source into an owned existing directory, then atomically replaces only gitmora.
// expected is mandatory for changed existing bytes. No shell, network, credential store or Git is used.
func Install(ctx context.Context, directory, source, expected string) (domain.Receipt, error) {
	if err := ctx.Err(); err != nil {
		return domain.Receipt{}, err
	}
	if !filepath.IsAbs(directory) || !filepath.IsAbs(source) || strings.ContainsAny(directory+source, "\x00\r\n") || (expected != "" && !domain.Digest(expected)) {
		return domain.Receipt{}, domain.ErrInvalid
	}
	canonical, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return domain.Receipt{}, domain.ErrInvalid
	}
	root, err := os.OpenRoot(canonical)
	if err != nil {
		return domain.Receipt{}, domain.ErrInvalid
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return domain.Receipt{}, domain.ErrInvalid
	}
	defer dir.Close()
	info, err := dir.Stat()
	if err != nil || !owned(info) || !info.IsDir() || info.Mode().Perm()&0022 != 0 {
		return domain.Receipt{}, domain.ErrInvalid
	}
	lock, err := openAt(dir, ".gitmora-install.lock", os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		lock, err = openAt(dir, ".gitmora-install.lock", os.O_RDWR, 0)
	}
	if err != nil {
		return domain.Receipt{}, domain.ErrInvalid
	}
	defer lock.Close()
	if li, e := lock.Stat(); e != nil || !regularOwned(li) || li.Size() != 0 || li.Mode().Perm() != 0600 {
		return domain.Receipt{}, domain.ErrInvalid
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return domain.Receipt{}, domain.ErrBusy
		}
		return domain.Receipt{}, domain.ErrIO
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	input, err := os.OpenFile(source, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return domain.Receipt{}, domain.ErrInvalid
	}
	defer input.Close()
	si, err := input.Stat()
	if err != nil || !si.Mode().IsRegular() || si.Size() < 1 || si.Size() > maxExecutable || si.Mode().Perm()&0111 == 0 {
		return domain.Receipt{}, domain.ErrInvalid
	}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return domain.Receipt{}, domain.ErrIO
	}
	name := ".gitmora-stage-" + hex.EncodeToString(nonce[:])
	stage, err := openAt(dir, name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
	if err != nil {
		return domain.Receipt{}, domain.ErrIO
	}
	defer stage.Close()
	defer root.Remove(name)
	hash := sha256.New()
	count, err := io.Copy(io.MultiWriter(stage, hash), &checkedReader{ctx: ctx, reader: io.LimitReader(input, maxExecutable+1)})
	if err != nil {
		if ctx.Err() != nil {
			return domain.Receipt{}, ctx.Err()
		}
		return domain.Receipt{}, domain.ErrIO
	}
	if count != si.Size() || count > maxExecutable {
		return domain.Receipt{}, domain.ErrInvalid
	}
	if err = stage.Chmod(0700); err != nil {
		return domain.Receipt{}, domain.ErrIO
	}
	if err = stage.Sync(); err != nil {
		return domain.Receipt{}, domain.ErrIO
	}
	if err = stage.Close(); err != nil {
		return domain.Receipt{}, domain.ErrIO
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	current, err := currentHash(ctx, dir)
	if err != nil {
		return domain.Receipt{}, err
	}
	change, err := domain.Replacement(current, digest, expected)
	if err != nil {
		return domain.Receipt{}, err
	}
	receipt := domain.Receipt{Path: filepath.Join(canonical, "gitmora"), SHA256: digest, PreviousSHA256: current, Changed: change}
	if !change {
		return receipt, nil
	}
	if err = ctx.Err(); err != nil {
		return domain.Receipt{}, err
	}
	// Check the returned stable pathname still selects the descriptor-owned directory.
	if selected, e := os.Stat(canonical); e != nil || !os.SameFile(info, selected) {
		return domain.Receipt{}, domain.ErrConflict
	}
	if err = root.Rename(name, "gitmora"); err != nil {
		return domain.Receipt{}, domain.ErrIO
	}
	if err = dir.Sync(); err != nil {
		return receipt, domain.ErrUncertain
	}
	return receipt, nil
}

func owned(info os.FileInfo) bool {
	if info == nil {
		return false
	}
	s, ok := info.Sys().(*syscall.Stat_t)
	return ok && s.Uid == uint32(os.Geteuid())
}
func regularOwned(info os.FileInfo) bool {
	if !owned(info) || !info.Mode().IsRegular() {
		return false
	}
	return info.Sys().(*syscall.Stat_t).Nlink == 1
}

// os.Root deliberately follows in-root symlinks even when O_NOFOLLOW is supplied.
// Use the owned directory descriptor directly so a leaf symlink is always rejected.
func openAt(dir *os.File, name string, flags int, mode uint32) (*os.File, error) {
	fd, err := unix.Openat(int(dir.Fd()), name, flags|unix.O_CLOEXEC|unix.O_NONBLOCK|unix.O_NOFOLLOW, mode)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

func currentHash(ctx context.Context, dir *os.File) (string, error) {
	f, err := openAt(dir, "gitmora", os.O_RDONLY, 0)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", domain.ErrInvalid
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !regularOwned(info) || info.Size() < 1 || info.Size() > maxExecutable || info.Mode().Perm()&0022 != 0 || info.Mode().Perm()&0111 == 0 {
		return "", domain.ErrInvalid
	}
	hash := sha256.New()
	count, err := io.Copy(hash, &checkedReader{ctx: ctx, reader: io.LimitReader(f, maxExecutable+1)})
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", domain.ErrIO
	}
	if count != info.Size() {
		return "", domain.ErrInvalid
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type checkedReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *checkedReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
