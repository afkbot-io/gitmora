package keychain_test

import (
	"context"
	"errors"
	"os"
	"runtime"
	"testing"
	"time"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/identifier"
	"workgraph.local/platform/internal/identity/keychain"
	"workgraph.local/platform/internal/identity/native"
)

// This opt-in test touches only a fresh random Workgraph server slot and always deletes that exact slot.
func TestActualNativeSecretStoreRoundTrip(t *testing.T) {
	if os.Getenv("WORKGRAPH_TEST_NATIVE_KEYCHAIN") != "1" {
		t.Skip("explicit native OS-store verification required")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("unsupported OS")
	}
	id, e := identifier.New()
	if e != nil {
		t.Fatal(e)
	}
	server := "http://127.0.0.1/native-verification/" + id
	v, e := keychain.New()
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, e = v.Load(ctx, server); !errors.Is(e, native.ErrNotFound) {
		t.Fatal("verification slot already present or native backend unavailable", e)
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if e := v.Delete(cleanup, server); e != nil {
			t.Error("native verification cleanup", e)
		}
	}()
	release, e := v.Lock(ctx, server)
	if e != nil {
		t.Fatal(e)
	}
	defer release()
	r, e := native.NewPendingRecord(server, "Native store verification", []access.Permission{access.WriteCapture, access.ManageCredentials}, 86400)
	if e != nil {
		t.Fatal(e)
	}
	if e = v.Save(ctx, r); e != nil {
		t.Fatal("actual native save", e)
	}
	back, e := v.Load(ctx, server)
	if e != nil || back.Secret != r.Secret || back.Pending.Proof != r.Pending.Proof {
		t.Fatal("actual native pending readback", e)
	}
	r.Pending = nil
	r.Account = &native.Account{UserID: id, TokenID: id, DisplayName: "Verification", Scopes: []access.Permission{access.ManageCredentials, access.WriteCapture}, ExpiresAt: time.Now().UTC().Truncate(time.Second).Add(time.Hour)}
	if e = v.Save(ctx, r); e != nil {
		t.Fatal("actual native activation update", e)
	}
	back, e = v.Load(ctx, server)
	if e != nil || back.Pending != nil || back.Account.TokenID != id || back.Secret != r.Secret {
		t.Fatal("actual native active readback", e)
	}
	if e = v.Delete(ctx, server); e != nil {
		t.Fatal(e)
	}
	if _, e = v.Load(ctx, server); !errors.Is(e, native.ErrNotFound) {
		t.Fatal("actual native deletion", e)
	}
}
