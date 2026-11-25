package tokenproxy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

type staticSource struct {
	tok string
	err error
}

func (s staticSource) Token(context.Context) (string, error) { return s.tok, s.err }

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestHandlerReplacesAuthorization(t *testing.T) {
	type seen struct{ auth, path, query, body, host string }
	tests := []struct {
		name       string
		prefix     string
		reqPath    string
		wantPath   string
		wantStatus int
	}{
		{"prefix is not doubled", "/v1", "/v1/chat/completions", "/v1/chat/completions", 200},
		{"embeddings", "/v1", "/v1/embeddings", "/v1/embeddings", 200},
		{"trailing slash on upstream", "/v1/", "/v1/chat/completions", "/v1/chat/completions", 200},
		{"no prefix upstream", "", "/chat/completions", "/chat/completions", 200},
		{"outside prefix", "/v1", "/admin", "", 404},
		{"sibling prefix not matched", "/v1", "/v10/models", "", 404},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			var got seen
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				b, _ := io.ReadAll(r.Body)
				got = seen{r.Header.Get("Authorization"), r.URL.Path, r.URL.RawQuery, string(b), r.Host}
				if v := r.Header.Values("Authorization"); len(v) != 1 {
					t.Errorf("Authorization values = %v", v)
				}
				_, _ = w.Write([]byte("up-ok"))
			}))
			defer up.Close()
			u, _ := url.Parse(up.URL + tt.prefix)

			var tokCalls atomic.Int32
			ts := tokenFunc(func() (string, error) { tokCalls.Add(1); return "tok", nil })
			h := NewHandler(u, ts, quiet)

			req := httptest.NewRequest(http.MethodPost, tt.reqPath+"?a=1&b=x%20y", strings.NewReader(`{"m":1}`))
			req.Header.Set("Authorization", "Bearer caller-secret")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantStatus != 200 {
				if calls.Load() != 0 || tokCalls.Load() != 0 {
					t.Errorf("upstream calls = %d, token calls = %d, want 0", calls.Load(), tokCalls.Load())
				}
				return
			}
			if got.auth != "Bearer tok" {
				t.Errorf("Authorization = %q", got.auth)
			}
			if got.path != tt.wantPath || got.query != "a=1&b=x%20y" || got.body != `{"m":1}` {
				t.Errorf("upstream saw %+v", got)
			}
			if got.host != u.Host {
				t.Errorf("Host = %q, want %q", got.host, u.Host)
			}
			if rec.Body.String() != "up-ok" {
				t.Errorf("body = %q", rec.Body.String())
			}
		})
	}
}

type tokenFunc func() (string, error)

func (f tokenFunc) Token(context.Context) (string, error) { return f() }

func TestHandlerTokenFailure(t *testing.T) {
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer up.Close()
	u, _ := url.Parse(up.URL + "/v1")

	h := NewHandler(u, staticSource{err: errors.New("boom")}, quiet)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer caller")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"error":"token unavailable"}` {
		t.Errorf("body = %q", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}
	if calls.Load() != 0 {
		t.Error("upstream was called without a token")
	}
}

func TestHandlerUpstreamDown(t *testing.T) {
	up := httptest.NewServer(http.NotFoundHandler())
	u, _ := url.Parse(up.URL + "/v1")
	up.Close()

	h := NewHandler(u, staticSource{tok: "t"}, quiet)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rec.Code)
	}
}
