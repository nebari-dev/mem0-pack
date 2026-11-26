package ensuresecret

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAPICreatorCreate(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		respBody    string
		wantErr     bool
		wantErrText []string
	}{
		{name: "201 created", status: http.StatusCreated, respBody: `{}`},
		{name: "409 already exists is success", status: http.StatusConflict, respBody: `{"reason":"AlreadyExists"}`},
		{
			name:        "403 forbidden includes status and body",
			status:      http.StatusForbidden,
			respBody:    `{"message":"secrets is forbidden"}`,
			wantErr:     true,
			wantErrText: []string{"403", "secrets is forbidden"},
		},
		{
			name:        "500 includes status and body",
			status:      http.StatusInternalServerError,
			respBody:    `{"message":"etcd down"}`,
			wantErr:     true,
			wantErrText: []string{"500", "etcd down"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tokenFile := filepath.Join(t.TempDir(), "token")
			writeFile(t, tokenFile, "tok-123\n")

			var methods []string
			var gotPath, gotAuth, gotCT string
			var gotBody map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				methods = append(methods, r.Method)
				gotPath = r.URL.Path
				gotAuth = r.Header.Get("Authorization")
				gotCT = r.Header.Get("Content-Type")
				b, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(b, &gotBody)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.respBody))
			}))
			defer srv.Close()

			c := NewAPICreator(srv.URL, srv.Client(), tokenFile)
			err := c.Create(context.Background(), "ns", "mem0-admin", "ADMIN_API_KEY", []byte("s3cret"))
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			for _, s := range tc.wantErrText {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("err %q missing %q", err, s)
				}
			}
			if len(methods) != 1 || methods[0] != http.MethodPost {
				t.Fatalf("methods = %v, want exactly one POST", methods)
			}
			if gotPath != "/api/v1/namespaces/ns/secrets" {
				t.Errorf("path = %q", gotPath)
			}
			if gotAuth != "Bearer tok-123" {
				t.Errorf("auth = %q, want trimmed token", gotAuth)
			}
			if gotCT != "application/json" {
				t.Errorf("content-type = %q", gotCT)
			}
			if gotBody["kind"] != "Secret" || gotBody["apiVersion"] != "v1" || gotBody["type"] != "Opaque" {
				t.Errorf("body = %v", gotBody)
			}
			md, _ := gotBody["metadata"].(map[string]any)
			if md["name"] != "mem0-admin" {
				t.Errorf("metadata.name = %v", md["name"])
			}
			labels, _ := md["labels"].(map[string]any)
			if labels["app.kubernetes.io/managed-by"] != "mem0-pack-tools" {
				t.Errorf("labels = %v", labels)
			}
			data, _ := gotBody["data"].(map[string]any)
			if data["ADMIN_API_KEY"] != base64.StdEncoding.EncodeToString([]byte("s3cret")) {
				t.Errorf("data = %v", data)
			}
		})
	}
}

func TestAPICreatorRereadsTokenEachCall(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "token")
	var auths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auths = append(auths, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	c := NewAPICreator(srv.URL, srv.Client(), tokenFile)
	for _, tok := range []string{"one", "two"} {
		writeFile(t, tokenFile, tok)
		if err := c.Create(context.Background(), "ns", "n", "k", []byte("v")); err != nil {
			t.Fatal(err)
		}
	}
	if len(auths) != 2 || auths[0] != "Bearer one" || auths[1] != "Bearer two" {
		t.Errorf("auths = %v", auths)
	}
}

func TestAPICreatorMissingTokenFile(t *testing.T) {
	c := NewAPICreator("http://127.0.0.1:1", http.DefaultClient, filepath.Join(t.TempDir(), "nope"))
	err := c.Create(context.Background(), "ns", "n", "k", []byte("v"))
	if err == nil || !strings.Contains(err.Error(), "read token") {
		t.Fatalf("err = %v, want read token error", err)
	}
}

func testCAPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestInCluster(t *testing.T) {
	validCA := testCAPEM(t)
	tests := []struct {
		name        string
		host, port  string
		files       map[string]string
		wantErrText string
		wantNS      string
	}{
		{
			name: "all present",
			host: "10.0.0.1", port: "443",
			files:  map[string]string{"ca.crt": validCA, "token": "t", "namespace": "mem0\n"},
			wantNS: "mem0",
		},
		{
			name: "ipv6 host is bracketed",
			host: "fd00::1", port: "443",
			files:  map[string]string{"ca.crt": validCA, "token": "t", "namespace": "mem0"},
			wantNS: "mem0",
		},
		{
			name: "missing ca", host: "10.0.0.1", port: "443",
			files:       map[string]string{"token": "t", "namespace": "mem0"},
			wantErrText: "read ca",
		},
		{
			name: "invalid ca", host: "10.0.0.1", port: "443",
			files:       map[string]string{"ca.crt": "not pem", "token": "t", "namespace": "mem0"},
			wantErrText: "no certificates",
		},
		{
			name: "missing namespace", host: "10.0.0.1", port: "443",
			files:       map[string]string{"ca.crt": validCA, "token": "t"},
			wantErrText: "read namespace",
		},
		{
			name: "missing token", host: "10.0.0.1", port: "443",
			files:       map[string]string{"ca.crt": validCA, "namespace": "mem0"},
			wantErrText: "read token",
		},
		{
			name: "empty namespace", host: "10.0.0.1", port: "443",
			files:       map[string]string{"ca.crt": validCA, "token": "t", "namespace": "\n"},
			wantErrText: "empty",
		},
		{
			name: "no service host", host: "", port: "443",
			files:       map[string]string{"ca.crt": validCA, "token": "t", "namespace": "mem0"},
			wantErrText: "KUBERNETES_SERVICE_HOST",
		},
		{
			name: "no service port", host: "10.0.0.1", port: "",
			files:       map[string]string{"ca.crt": validCA, "token": "t", "namespace": "mem0"},
			wantErrText: "KUBERNETES_SERVICE_PORT",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for n, c := range tc.files {
				writeFile(t, filepath.Join(dir, n), c)
			}
			old := serviceAccountDir
			serviceAccountDir = dir
			t.Cleanup(func() { serviceAccountDir = old })
			t.Setenv("KUBERNETES_SERVICE_HOST", tc.host)
			t.Setenv("KUBERNETES_SERVICE_PORT", tc.port)

			c, ns, err := InCluster()
			if tc.wantErrText != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrText) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErrText)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if ns != tc.wantNS {
				t.Errorf("ns = %q, want %q", ns, tc.wantNS)
			}
			if c == nil {
				t.Fatal("nil creator")
			}
		})
	}
}

func TestInClusterClientTrustsOnlyServiceAccountCA(t *testing.T) {
	// A TLS server signed by an unrelated CA must be rejected by the client.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ca.crt"), testCAPEM(t))
	writeFile(t, filepath.Join(dir, "token"), "t")
	writeFile(t, filepath.Join(dir, "namespace"), "mem0")
	old := serviceAccountDir
	serviceAccountDir = dir
	t.Cleanup(func() { serviceAccountDir = old })
	host, port, _ := strings.Cut(strings.TrimPrefix(srv.URL, "https://"), ":")
	t.Setenv("KUBERNETES_SERVICE_HOST", host)
	t.Setenv("KUBERNETES_SERVICE_PORT", port)

	c, _, err := InCluster()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Create(context.Background(), "mem0", "n", "k", []byte("v")); err == nil {
		t.Fatal("expected TLS verification failure against untrusted server")
	}
}
