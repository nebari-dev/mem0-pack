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
// fetched, so the proxy cannot reach other parts of the upstream.
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
	if !underPrefix(r.URL.Path, h.prefix) {
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

func underPrefix(path, prefix string) bool {
	if prefix == "" {
		return true
	}
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}
