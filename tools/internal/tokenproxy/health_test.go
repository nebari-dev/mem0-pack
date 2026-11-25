package tokenproxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type fetchedFlag struct{ v bool }

func (f *fetchedFlag) Fetched() bool { return f.v }

func TestHealth(t *testing.T) {
	tests := []struct {
		name    string
		fetched bool
		method  string
		path    string
		want    int
	}{
		{"before first fetch", false, http.MethodGet, "/healthz", 503},
		{"after first fetch", true, http.MethodGet, "/healthz", 200},
		{"other path", true, http.MethodGet, "/readyz", 404},
		{"root path", true, http.MethodGet, "/", 404},
		{"wrong method", true, http.MethodPost, "/healthz", 405},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewHealth(&fetchedFlag{tt.fetched})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))
			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}
