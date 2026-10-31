// Command fixture is a synthetic local OIDC provider for built-process tests only.
package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"
)

type grant struct {
	nonce, challenge, redirect, subject, mode string
	expires                                   time.Time
}

func main() {
	callback := flag.String("callback", "", "Exact registered RP callback")
	flag.Parse()
	if *callback == "" {
		panic("callback required")
	}
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		panic(e)
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		panic(e)
	}
	base := fmt.Sprintf("http://localhost:%d", listener.Addr().(*net.TCPAddr).Port)
	var mu sync.Mutex
	mode := "good"
	subject := "subject-reader"
	calls := 0
	grants := map[string]grant{}
	encode := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		currentMode, currentSubject := mode, subject
		mu.Unlock()
		switch r.URL.Path {
		case "/control":
			if r.Method != "POST" {
				w.WriteHeader(405)
				return
			}
			var body struct{ Mode, Subject string }
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&body) != nil {
				w.WriteHeader(400)
				return
			}
			mu.Lock()
			if body.Mode != "" {
				mode = body.Mode
			}
			if body.Subject != "" {
				subject = body.Subject
			}
			mu.Unlock()
			w.Write([]byte(`{"ok":true}`))
		case "/stats":
			mu.Lock()
			n := calls
			mu.Unlock()
			json.NewEncoder(w).Encode(map[string]int{"token_calls": n})
		case "/.well-known/openid-configuration":
			if currentMode == "down" {
				w.WriteHeader(503)
				w.Write([]byte(`{"error":"synthetic provider down"}`))
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"issuer": base, "authorization_endpoint": base + "/authorize", "token_endpoint": base + "/token", "jwks_uri": base + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/keys":
			json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kty": "RSA", "kid": "fixture", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
		case "/authorize":
			q := r.URL.Query()
			if q.Get("client_id") != "workgraph" || q.Get("redirect_uri") != *callback || q.Get("code_challenge_method") != "S256" || q.Get("response_type") != "code" || q.Get("state") == "" || q.Get("nonce") == "" {
				w.WriteHeader(400)
				return
			}
			var raw [32]byte
			rand.Read(raw[:])
			code := base64.RawURLEncoding.EncodeToString(raw[:])
			mu.Lock()
			grants[code] = grant{nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), redirect: *callback, subject: currentSubject, mode: currentMode, expires: time.Now().Add(time.Minute)}
			mu.Unlock()
			target, _ := url.Parse(*callback)
			values := target.Query()
			values.Set("code", code)
			values.Set("state", q.Get("state"))
			values.Set("iss", base)
			target.RawQuery = values.Encode()
			http.Redirect(w, r, target.String(), 303)
		case "/token":
			mu.Lock()
			calls++
			mu.Unlock()
			if currentMode == "down" {
				w.WriteHeader(503)
				w.Write([]byte(`{"error":"provider temporarily unavailable"}`))
				return
			}
			client, secret, ok := r.BasicAuth()
			r.ParseForm()
			mu.Lock()
			g, exists := grants[r.Form.Get("code")]
			delete(grants, r.Form.Get("code"))
			mu.Unlock()
			h := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if !ok || client != "workgraph" || secret != "synthetic-oidc-client-secret" || !exists || time.Now().After(g.expires) || r.Form.Get("redirect_uri") != g.redirect || r.Form.Get("grant_type") != "authorization_code" || base64.RawURLEncoding.EncodeToString(h[:]) != g.challenge {
				w.WriteHeader(400)
				w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			access := "synthetic-access-token"
			accessHash := sha256.Sum256([]byte(access))
			claims := map[string]any{"iss": base, "sub": g.subject, "aud": "workgraph", "iat": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix(), "nonce": g.nonce, "at_hash": base64.RawURLEncoding.EncodeToString(accessHash[:16]), "email": "administrator@example.internal", "groups": []string{"admins"}, "roles": []string{"admin"}}
			if g.mode == "bad-nonce" {
				claims["nonce"] = "another-flow"
			}
			if g.mode == "bad-audience" {
				claims["aud"] = "another-client"
			}
			raw := encode(map[string]string{"alg": "RS256", "kid": "fixture", "typ": "JWT"}) + "." + encode(claims)
			hash := sha256.Sum256([]byte(raw))
			signature, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
			if g.mode == "bad-signature" {
				signature[0] ^= 1
			}
			jwt := raw + "." + base64.RawURLEncoding.EncodeToString(signature)
			json.NewEncoder(w).Encode(map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": 300, "id_token": jwt})
		default:
			w.WriteHeader(404)
		}
	})
	json.NewEncoder(os.Stdout).Encode(map[string]string{"issuer": base})
	server := http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	if e = server.Serve(listener); e != nil {
		panic(e)
	}
}
