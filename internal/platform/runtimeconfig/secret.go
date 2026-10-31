// Package runtimeconfig adapts deployment secret sources without mutating process-wide environment state.
package runtimeconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Name is an explicitly supported sensitive installation input; arbitrary environment keys are not accepted.
type Name string

const (
	DatabaseURL      Name = "WORKGRAPH_DATABASE_URL"
	EncryptionKeys   Name = "WORKGRAPH_ENCRYPTION_KEYS"
	OIDCClientSecret Name = "WORKGRAPH_OIDC_CLIENT_SECRET"
)

// ErrSecret deliberately carries no secret contents, local path or filesystem diagnostic.
var ErrSecret = errors.New("invalid deployment secret configuration")

// Value requires explicit Reveal at a trusted adapter boundary; generic diagnostics/JSON stay redacted.
type Value struct{ raw string }

// Reveal supplies plaintext only to its intended database/provider/cryptography adapter.
func (v Value) Reveal() string { return v.raw }

// String and GoString prevent accidental secret interpolation in diagnostics.
func (Value) String() string   { return "[redacted]" }
func (Value) GoString() string { return "[redacted]" }

// MarshalJSON prevents a secret container from becoming a generic API/log object.
func (Value) MarshalJSON() ([]byte, error) { return json.Marshal("[redacted]") }

// Secret loads exactly one configured source. A present direct variable and _FILE conflict even when empty.
// An absent source returns an empty optional value; a configured file must be absolute, regular and nonempty.
func Secret(name Name) (Value, error) {
	switch name {
	case DatabaseURL, EncryptionKeys, OIDCClientSecret:
	default:
		return Value{}, ErrSecret
	}
	fail := func() (Value, error) { return Value{}, fmt.Errorf("%w: %s", ErrSecret, name) }
	direct, hasDirect := os.LookupEnv(string(name))
	path, hasFile := os.LookupEnv(string(name) + "_FILE")
	if hasDirect && hasFile {
		return fail()
	}
	if !hasFile {
		if len(direct) > 65536 || !utf8.ValidString(direct) || strings.ContainsRune(direct, '\x00') {
			return fail()
		}
		return Value{raw: direct}, nil
	}
	if !filepath.IsAbs(path) || len(path) > 4096 || strings.ContainsAny(path, "\x00\r\n") {
		return fail()
	}
	info, e := os.Stat(path)
	if e != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 65536 {
		return fail()
	}
	f, e := openSecretFile(path)
	if e != nil {
		return fail()
	}
	defer f.Close()
	info, e = f.Stat()
	if e != nil || !info.Mode().IsRegular() {
		return fail()
	}
	raw, e := io.ReadAll(io.LimitReader(f, 65537))
	if e != nil || len(raw) > 65536 || !utf8.Valid(raw) {
		return fail()
	}
	text := strings.TrimSuffix(string(raw), "\n")
	text = strings.TrimSuffix(text, "\r")
	if text == "" || strings.ContainsRune(text, '\x00') {
		return fail()
	}
	return Value{raw: text}, nil
}
