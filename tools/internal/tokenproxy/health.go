package tokenproxy

import "net/http"

// NewHealth answers only GET /healthz: 200 once a token has been fetched,
// 503 before.
func NewHealth(f interface{ Fetched() bool }) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if !f.Fetched() {
			http.Error(w, "no token fetched yet", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}
