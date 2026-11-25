package tokenproxy

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

type tokenKey struct{}

// Handler reverse-proxies requests to the upstream with a bearer token.
//
// Path mapping: the upstream's path prefix (for example /v1) is also the
// prefix callers use on the proxy, so the request path is forwarded unchanged
// to the upstream origin. mem0 calls http://127.0.0.1:8081/v1/chat/completions
// and, with upstream https://host/v1, the upstream sees /v1/chat/completions
// (never /v1/v1/...). A path outside the prefix gets 404 and no token is
// fetched, so the proxy cannot reach other parts of the upstream. Paths with
// percent-encoding, backslashes, or empty/dot segments are rejected the same
// way, since they could resolve outside the prefix upstream.
type Handler struct {
	prefix string
	ts     TokenSource
	log    *slog.Logger
	rp     *httputil.ReverseProxy
}

// NewHandler builds a Handler for upstream using ts for credentials.
func NewHandler(upstream *url.URL, ts TokenSource, log *slog.Logger) *Handler {
	h := &Handler{prefix: strings.TrimRight(upstream.Path, "/"), ts: ts, log: log}
	h.rp = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = upstream.Scheme
			pr.Out.URL.Host = upstream.Host
			pr.Out.Host = upstream.Host
			// Set replaces any Authorization the caller sent.
			tok, _ := pr.In.Context().Value(tokenKey{}).(string)
			pr.Out.Header.Set("Authorization", "Bearer "+tok)
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Error("proxy request", "error", err)
			writeJSON(w, http.StatusBadGateway, `{"error":"upstream unavailable"}`)
		},
	}
	return h
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !cleanPath(r.URL) || !underPrefix(r.URL.Path, h.prefix) {
		writeJSON(w, http.StatusNotFound, `{"error":"not found"}`)
		return
	}
	tok, err := h.ts.Token(r.Context())
	if err != nil {
		h.log.Error("fetch token", "error", err)
		writeJSON(w, http.StatusBadGateway, `{"error":"token unavailable"}`)
		return
	}
	h.rp.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), tokenKey{}, tok)))
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// cleanPath reports whether the request path is safe to forward verbatim:
// no percent-encoding (RawPath is set whenever the original differs from the
// default encoding), no backslash, and no empty, "." or ".." segment. Without
// this, /v1/../admin would pass the prefix check yet resolve outside it.
func cleanPath(u *url.URL) bool {
	if u.RawPath != "" || strings.Contains(u.Path, "\\") {
		return false
	}
	if u.Path == "" {
		return true
	}
	for _, seg := range strings.Split(u.Path[1:], "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

func underPrefix(path, prefix string) bool {
	if prefix == "" {
		return true
	}
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}
