// Synthetic network-namespace acceptance driver. Public test addresses exist only on this container's loopback.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/forge/gitlab"
)

const host = "gitlab.verification.invalid"
const token = "synthetic-egress-provider-token"

type child struct {
	cmd  *exec.Cmd
	done chan error
	logs bytes.Buffer
}

func launch(ctx context.Context, binary, port, target string) (*child, error) {
	c := &child{done: make(chan error, 1)}
	c.cmd = exec.CommandContext(ctx, binary, "egress", "--listen", "127.0.0.1:"+port, "--target", target)
	c.cmd.Env = append(os.Environ(), "WORKGRAPH_DATABASE_URL=private-database-must-not-be-read")
	c.cmd.Stdout = &c.logs
	c.cmd.Stderr = &c.logs
	if e := c.cmd.Start(); e != nil {
		return nil, e
	}
	go func() { c.done <- c.cmd.Wait(); close(c.done) }()
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: time.Second}
	for range 50 {
		r, e := client.Get("http://127.0.0.1:" + port + "/health/ready")
		if e == nil {
			r.Body.Close()
			if r.StatusCode == 200 {
				return c, nil
			}
		}
		select {
		case <-ctx.Done():
			return c, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	return c, errors.New("relay did not become ready")
}
func (c *child) stop() error {
	_ = c.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case e := <-c.done:
		return e
	case <-time.After(5 * time.Second):
		_ = c.cmd.Process.Kill()
		<-c.done
		return errors.New("relay failed graceful socket shutdown")
	}
}
func (c *child) cleanup() { _ = c.cmd.Process.Kill(); <-c.done }

func connect(port, target, extra string) (net.Conn, *bufio.Reader, int, error) {
	c, e := net.DialTimeout("tcp", "127.0.0.1:"+port, time.Second)
	if e != nil {
		return nil, nil, 0, e
	}
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	_, e = io.WriteString(c, "CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\n"+extra+"\r\n")
	if e != nil {
		c.Close()
		return nil, nil, 0, e
	}
	b := bufio.NewReader(c)
	line, e := b.ReadString('\n')
	if e != nil {
		c.Close()
		return nil, nil, 0, e
	}
	var version string
	var status int
	_, e = fmt.Sscanf(line, "%s %d", &version, &status)
	if e != nil {
		c.Close()
		return nil, nil, 0, e
	}
	for {
		line, e = b.ReadString('\n')
		if e != nil {
			c.Close()
			return nil, nil, 0, e
		}
		if line == "\r\n" {
			break
		}
	}
	return c, b, status, nil
}

func verify() error {
	if os.Geteuid() == 0 || len(os.Args) != 2 {
		return errors.New("fixture requires an unprivileged user and exact built binary")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return e
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{host}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if e != nil {
		return e
	}
	root, e := os.MkdirTemp("", "egress-trust-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(root)
	ca := filepath.Join(root, "ca.pem")
	if e = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); e != nil {
		return e
	}
	_ = os.Setenv("SSL_CERT_FILE", ca)
	_ = os.Setenv("SSL_CERT_DIR", root)
	listener, e := net.Listen("tcp", "0.0.0.0:443")
	if e != nil {
		return e
	}
	var accepted atomic.Int64
	server := &http.Server{ReadHeaderTimeout: 3 * time.Second, ErrorLog: log.New(io.Discard, "", 0), Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || r.TLS.ServerName != host || r.Host != host || r.URL.Path != "/api/v4/projects/42" || r.Header.Get("PRIVATE-TOKEN") != token {
			w.WriteHeader(403)
			return
		}
		accepted.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":42}`)
	})}
	go server.Serve(tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}))
	defer server.Close()
	main, e := launch(ctx, os.Args[1], "18080", host+":443")
	if main != nil {
		defer main.cleanup()
	}
	if e != nil {
		return e
	}
	client, e := gitlab.NewWithProxy([]string{"https://" + host}, "http://127.0.0.1:18080")
	if e != nil {
		return e
	}
	if e = client.VerifyProject(ctx, domain.Repository{BaseURL: "https://" + host, ProjectID: 42}, token); e != nil || accepted.Load() != 1 {
		return errors.New("actual private proxy/public route/TLS provider request failed")
	}
	proxy, _ := url.Parse("http://127.0.0.1:18080")
	bad := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxy), TLSClientConfig: &tls.Config{RootCAs: x509.NewCertPool()}}}
	_, e = bad.Get("https://" + host + "/api/v4/projects/42")
	var authority x509.UnknownAuthorityError
	if e == nil || !errors.As(e, &authority) || accepted.Load() != 1 {
		return errors.New("untrusted provider certificate was not rejected")
	}
	for _, target := range []string{"example.com:443", host + ":80", "169.254.169.254:443"} {
		c, _, status, e := connect("18080", target, "")
		if e != nil {
			return e
		}
		c.Close()
		if status != 403 {
			return errors.New("relay accepted a different authority")
		}
	}
	for _, name := range []string{"Authorization", "Proxy-Authorization", "PRIVATE-TOKEN", "X-Gitlab-Token", "Cookie"} {
		c, _, status, e := connect("18080", host+":443", name+":\r\n"+name+": synthetic-private-marker\r\n")
		if e != nil {
			return e
		}
		c.Close()
		if status != 403 {
			return errors.New("relay accepted plaintext credential metadata")
		}
	}
	blocked, e := launch(ctx, os.Args[1], "18081", "blocked.verification.invalid:443")
	if blocked != nil {
		defer blocked.cleanup()
	}
	if e != nil {
		return e
	}
	c, _, status, e := connect("18081", "blocked.verification.invalid:443", "")
	if e != nil {
		return e
	}
	c.Close()
	if status != 502 {
		return errors.New("public route dialed a private DNS answer")
	}
	if e = blocked.stop(); e != nil {
		return e
	}
	c, reader, status, e := connect("18080", host+":443", "")
	if e != nil {
		return e
	}
	defer c.Close()
	if status != 200 {
		return errors.New("shutdown probe tunnel did not open")
	}
	if e = main.stop(); e != nil {
		return e
	}
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	if _, e = reader.ReadByte(); e == nil {
		return errors.New("shutdown left a hijacked connection open")
	}
	if strings.Contains(main.logs.String(), token) || strings.Contains(main.logs.String(), "synthetic-private-marker") {
		return errors.New("relay logged private material")
	}
	return nil
}

func main() {
	if e := verify(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	fmt.Println("Actual unprivileged Linux egress: isolated network namespace, production CONNECT and DNS pinning, GitLab adapter with verified TLS, wrong authority/private DNS/plaintext credential rejection and active-tunnel shutdown passed.")
}
