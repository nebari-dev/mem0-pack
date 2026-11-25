package ensuresecret

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	managedByLabel = "app.kubernetes.io/managed-by"
	managedByValue = "mem0-pack-tools"
	maxErrBody     = 4096
)

// serviceAccountDir holds the projected ServiceAccount files. It is a variable
// so tests can point it at a temp dir.
var serviceAccountDir = "/var/run/secrets/kubernetes.io/serviceaccount"

// Doer sends an HTTP request; *http.Client satisfies it.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// APICreator creates Secrets through the Kubernetes API. It only ever issues
// POST, so it never reads or modifies an existing Secret.
type APICreator struct {
	apiURL    string
	hc        Doer
	tokenFile string
}

// NewAPICreator returns a creator that talks to apiURL and reads its bearer
// token from tokenFile on every call, since projected tokens rotate.
func NewAPICreator(apiURL string, hc Doer, tokenFile string) *APICreator {
	return &APICreator{apiURL: strings.TrimRight(apiURL, "/"), hc: hc, tokenFile: tokenFile}
}

// Create POSTs an Opaque Secret holding value under key. A 201 or a 409
// (already exists) returns nil; any other status is an error carrying the
// status code and response body.
func (a *APICreator) Create(ctx context.Context, ns, name, key string, value []byte) error {
	tok, err := os.ReadFile(a.tokenFile)
	if err != nil {
		return fmt.Errorf("read token: %w", err)
	}
	body, err := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"type":       "Opaque",
		"metadata": map[string]any{
			"name":   name,
			"labels": map[string]string{managedByLabel: managedByValue},
		},
		"data": map[string]string{key: base64.StdEncoding.EncodeToString(value)},
	})
	if err != nil {
		return fmt.Errorf("encode secret: %w", err)
	}
	endpoint := a.apiURL + "/api/v1/namespaces/" + url.PathEscape(ns) + "/secrets"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tok)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.hc.Do(req)
	if err != nil {
		return fmt.Errorf("post secret: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusCreated, http.StatusConflict:
		return nil
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBody))
	return fmt.Errorf("create secret: status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
}

// InCluster builds a creator from the in-cluster environment and returns it
// with the Pod's namespace. The client trusts only the ServiceAccount CA.
func InCluster() (*APICreator, string, error) {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" {
		return nil, "", errors.New("KUBERNETES_SERVICE_HOST is not set")
	}
	if port == "" {
		return nil, "", errors.New("KUBERNETES_SERVICE_PORT is not set")
	}
	caPEM, err := os.ReadFile(filepath.Join(serviceAccountDir, "ca.crt"))
	if err != nil {
		return nil, "", fmt.Errorf("read ca: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, "", errors.New("parse ca: no certificates found")
	}
	nsBytes, err := os.ReadFile(filepath.Join(serviceAccountDir, "namespace"))
	if err != nil {
		return nil, "", fmt.Errorf("read namespace: %w", err)
	}
	ns := strings.TrimSpace(string(nsBytes))
	if ns == "" {
		return nil, "", errors.New("read namespace: file is empty")
	}
	tokenFile := filepath.Join(serviceAccountDir, "token")
	if _, err := os.ReadFile(tokenFile); err != nil {
		return nil, "", fmt.Errorf("read token: %w", err)
	}
	hc := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		},
	}
	apiURL := "https://" + net.JoinHostPort(host, port)
	return NewAPICreator(apiURL, hc, tokenFile), ns, nil
}
