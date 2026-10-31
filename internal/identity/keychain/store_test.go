package keychain

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/identity/native"
)

type call struct {
	args  []string
	input string
}
type fakeRunner struct {
	mu       sync.Mutex
	calls    []call
	password string
	fail     bool
}

func (f *fakeRunner) Run(_ context.Context, _ string, args []string, input []byte) ([]byte, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call{append([]string(nil), args...), string(input)})
	if f.fail {
		return nil, 1, errors.New("private backend diagnostic")
	}
	switch args[0] {
	case "store":
		f.password = strings.TrimSpace(string(input))
		return nil, 0, nil
	case "lookup":
		if f.password == "" {
			return nil, 1, nil
		}
		return []byte(f.password + "\n"), 0, nil
	case "clear":
		f.password = ""
		return nil, 0, nil
	case "-i":
		parts := strings.Fields(string(input))
		for i, p := range parts {
			if p == "-w" {
				f.password = parts[i+1]
			}
		}
		return nil, 0, nil
	case "find-generic-password":
		if f.password == "" {
			return nil, 44, nil
		}
		return []byte(f.password + "\n"), 0, nil
	case "delete-generic-password":
		f.password = ""
		return nil, 0, nil
	}
	return nil, 1, nil
}

func TestNativeVaultUsesSystemProtocolsWithoutCredentialArgumentsOrPlaintextFallback(t *testing.T) {
	for _, platform := range []string{"darwin", "linux"} {
		t.Run(platform, func(t *testing.T) {
			f := &fakeRunner{}
			root := t.TempDir()
			v := newStore(platform, "/system/secret-tool", root, "", f)
			r, e := native.NewPendingRecord("https://workgraph.internal", "Fixture", []access.Permission{access.WriteCapture, access.ManageCredentials}, 86400)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = v.Load(context.Background(), r.Server); !errors.Is(e, native.ErrNotFound) {
				t.Fatal(e)
			}
			if e = v.Save(context.Background(), r); e != nil {
				t.Fatal(e)
			}
			back, e := v.Load(context.Background(), r.Server)
			if e != nil || back.Secret != r.Secret || back.Pending.Proof != r.Pending.Proof {
				t.Fatal("system protocol round trip", e)
			}
			encoded, _ := native.EncodeSecretStoreRecord(r)
			payload := base64.RawURLEncoding.EncodeToString(encoded)
			for _, c := range f.calls {
				args := strings.Join(c.args, " ")
				for _, private := range []string{r.Secret, r.Pending.Device, r.Pending.Proof, payload} {
					if strings.Contains(args, private) {
						t.Fatal("private material exposed in process arguments")
					}
				}
			}
			entries, e := os.ReadDir(root)
			if e != nil || len(entries) != 0 {
				t.Fatal("secret file fallback appeared", e)
			}
			f.fail = true
			if _, e = v.Load(context.Background(), r.Server); !errors.Is(e, native.ErrVault) || strings.Contains(e.Error(), "private") {
				t.Fatal("backend diagnostic exposed", e)
			}
			f.fail = false
			if e = v.Delete(context.Background(), r.Server); e != nil {
				t.Fatal(e)
			}
			if _, e = v.Load(context.Background(), r.Server); !errors.Is(e, native.ErrNotFound) {
				t.Fatal(e)
			}
		})
	}
}

func TestNativeAuthenticationLeaseIsPerServerAndContainsNoSecretMaterial(t *testing.T) {
	root := filepath.Join(t.TempDir(), "auth")
	v := newStore("linux", "/system/secret-tool", root, "", &fakeRunner{})
	release, e := v.Lock(context.Background(), "https://workgraph.internal")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = v.Lock(context.Background(), "https://WORKGRAPH.internal:443/"); !errors.Is(e, native.ErrBusy) {
		t.Fatal("same-server operation was not fenced", e)
	}
	other, e := v.Lock(context.Background(), "https://other.internal")
	if e != nil {
		t.Fatal(e)
	}
	other()
	release()
	again, e := v.Lock(context.Background(), "https://workgraph.internal")
	if e != nil {
		t.Fatal(e)
	}
	again()
	e = filepath.WalkDir(root, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if !d.IsDir() {
			raw, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			if len(raw) != 0 {
				t.Fatal("auth lock contained private state")
			}
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}

func TestNativeSecretProcessHelper(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-1] != "private-output-helper" {
		return
	}
	_, _ = os.Stdout.Write([]byte(strings.Repeat("synthetic-private", 8192)))
	os.Exit(0)
}

func TestNativeSecretProcessOutputIsActuallyBounded(t *testing.T) {
	raw, _, e := (processRunner{}).Run(context.Background(), os.Args[0], []string{"-test.run=TestNativeSecretProcessHelper", "--", "private-output-helper"}, nil)
	if e == nil || len(raw) > 32768 {
		t.Fatal("private child output bypassed the byte budget")
	}
}
