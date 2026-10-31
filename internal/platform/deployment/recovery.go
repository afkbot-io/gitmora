package deployment

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"workgraph.local/platform/internal/recovery/application"
	"workgraph.local/platform/internal/recovery/domain"
)

// privateRecovery is an explicit private-file codec, never a diagnostic or API serialization type.
type privateRecovery struct {
	Version int         `json:"version"`
	Plan    domain.Plan `json:"plan"`
	Token   string      `json:"token"`
}

func privateRecoveryParent(path string) bool {
	if !absolute(path) {
		return false
	}
	parent, e := os.Lstat(filepath.Dir(path))
	return e == nil && parent.IsDir() && parent.Mode().Perm()&0o077 == 0
}

// PrepareRecovery durably creates one private candidate before quarantine; it never replaces a pending intent.
func PrepareRecovery(ctx context.Context, path string, source domain.Provenance, name string, ttl time.Duration) (domain.Plan, error) {
	if !privateRecoveryParent(path) {
		return domain.Plan{}, ErrFilesystem
	}
	if e := ctx.Err(); e != nil {
		return domain.Plan{}, e
	}
	p, e := application.NewService(nil, time.Now).Prepare(source, name, ttl)
	if e != nil {
		return domain.Plan{}, e
	}
	raw, e := json.Marshal(privateRecovery{Version: 1, Plan: p.Plan, Token: p.Secret()})
	if e != nil {
		return domain.Plan{}, ErrInvalid
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if e != nil {
		if os.IsExist(e) {
			return domain.Plan{}, ErrConflict
		}
		return domain.Plan{}, ErrFilesystem
	}
	complete := false
	defer func() {
		_ = f.Close()
		if !complete {
			_ = os.Remove(path)
		}
	}()
	if _, e = f.Write(append(raw, '\n')); e != nil {
		return domain.Plan{}, ErrFilesystem
	}
	if e = f.Sync(); e != nil {
		return domain.Plan{}, ErrFilesystem
	}
	if e = f.Close(); e != nil {
		return domain.Plan{}, ErrFilesystem
	}
	if e = syncDir(filepath.Dir(path)); e != nil {
		return domain.Plan{}, e
	}
	complete = true
	return p.Plan, nil
}
