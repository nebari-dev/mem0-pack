package tokenproxy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeDoer struct {
	mu      sync.Mutex
	reqs    []url.Values
	methods []string
	status  int
	body    func(n int) string
	gate    chan struct{} // if set, Do blocks until closed
}

func (f *fakeDoer) Do(r *http.Request) (*http.Response, error) {
	b, _ := io.ReadAll(r.Body)
	form, _ := url.ParseQuery(string(b))
	f.mu.Lock()
	f.reqs = append(f.reqs, form)
	f.methods = append(f.methods, r.Method+" "+r.Header.Get("Content-Type"))
	n := len(f.reqs)
	f.mu.Unlock()
	if f.gate != nil {
		<-f.gate
	}
	status := f.status
	if status == 0 {
		status = 200
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(f.body(n)))}, nil
}

func (f *fakeDoer) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.reqs) }

func tokenBody(life int) func(int) string {
	return func(n int) string { return fmt.Sprintf(`{"access_token":"tok%d","expires_in":%d}`, n, life) }
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func creds(t *testing.T, scope string) Config {
	t.Helper()
	dir := t.TempDir()
	id, secret := filepath.Join(dir, "id"), filepath.Join(dir, "secret")
	if err := os.WriteFile(id, []byte("cid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secret, []byte("csecret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return Config{TokenURL: "http://kc/token", ClientIDFile: id, ClientSecretFile: secret, Scope: scope}
}

func TestClientCredentialsCaching(t *testing.T) {
	tests := []struct {
		name      string
		lifetime  int
		calls     []time.Duration
		wantFetch int
	}{
		{"300s reuse at 200s", 300, []time.Duration{0, 200 * time.Second}, 1},
		{"300s refetch at 275s", 300, []time.Duration{0, 275 * time.Second}, 2},
		{"300s refetch exactly at margin", 300, []time.Duration{0, 270 * time.Second}, 2},
		{"20s reuse at 10s", 20, []time.Duration{0, 10 * time.Second}, 1},
		{"20s refetch at 16s", 20, []time.Duration{0, 16 * time.Second}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &fakeDoer{body: tokenBody(tt.lifetime)}
			clk := &clock{t: time.Unix(1000, 0)}
			start := clk.t
			cc := NewClientCredentials(creds(t, ""), d, clk.now)
			for _, off := range tt.calls {
				clk.t = start.Add(off)
				if _, err := cc.Token(context.Background()); err != nil {
					t.Fatalf("Token at %v: %v", off, err)
				}
			}
			if got := d.count(); got != tt.wantFetch {
				t.Errorf("fetches = %d, want %d", got, tt.wantFetch)
			}
		})
	}
}

func TestClientCredentialsRequest(t *testing.T) {
	tests := []struct {
		name      string
		scope     string
		wantScope bool
	}{
		{"without scope", "", false},
		{"with scope", "openid llm", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &fakeDoer{body: tokenBody(300)}
			cfg := creds(t, tt.scope)
			cc := NewClientCredentials(cfg, d, time.Now)
			tok, err := cc.Token(context.Background())
			if err != nil || tok != "tok1" {
				t.Fatalf("Token = %q, %v", tok, err)
			}
			f := d.reqs[0]
			if d.methods[0] != "POST application/x-www-form-urlencoded" {
				t.Errorf("method/content-type = %q", d.methods[0])
			}
			if f.Get("grant_type") != "client_credentials" || f.Get("client_id") != "cid" || f.Get("client_secret") != "csecret" {
				t.Errorf("form = %v", f)
			}
			if _, has := f["scope"]; has != tt.wantScope || (has && f.Get("scope") != tt.scope) {
				t.Errorf("scope presence = %v, form = %v", has, f)
			}
		})
	}
}

func TestClientCredentialsReadsFilesEachFetch(t *testing.T) {
	d := &fakeDoer{body: tokenBody(20)}
	clk := &clock{t: time.Unix(1000, 0)}
	cfg := creds(t, "")
	cc := NewClientCredentials(cfg, d, clk.now)
	if _, err := cc.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.ClientSecretFile, []byte("rotated"), 0o600); err != nil {
		t.Fatal(err)
	}
	clk.t = clk.t.Add(19 * time.Second)
	if _, err := cc.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := d.reqs[1].Get("client_secret"); got != "rotated" {
		t.Errorf("second fetch secret = %q, want rotated", got)
	}
}

func TestClientCredentialsErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{"non-200", 401, `{"error":"invalid_client"}`},
		{"malformed json", 200, `not json`},
		{"missing access_token", 200, `{"expires_in":300}`},
		{"zero expires_in", 200, `{"access_token":"t","expires_in":0}`},
		{"negative expires_in", 200, `{"access_token":"t","expires_in":-5}`},
		{"missing expires_in", 200, `{"access_token":"t"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &fakeDoer{status: tt.status, body: func(int) string { return tt.body }}
			cc := NewClientCredentials(creds(t, ""), d, time.Now)
			if _, err := cc.Token(context.Background()); err == nil {
				t.Fatal("expected error")
			}
			if cc.Fetched() {
				t.Error("Fetched() true after failure")
			}
			// Nothing cached: the next call must fetch again.
			_, _ = cc.Token(context.Background())
			if d.count() != 2 {
				t.Errorf("fetches = %d, want 2 (failure must not be cached)", d.count())
			}
		})
	}
}

func TestClientCredentialsMissingFile(t *testing.T) {
	cfg := creds(t, "")
	cfg.ClientSecretFile = filepath.Join(t.TempDir(), "nope")
	d := &fakeDoer{body: tokenBody(300)}
	if _, err := NewClientCredentials(cfg, d, time.Now).Token(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if d.count() != 0 {
		t.Error("must not call the token endpoint without credentials")
	}
}

func TestClientCredentialsSingleFlight(t *testing.T) {
	d := &fakeDoer{body: tokenBody(300), gate: make(chan struct{})}
	cc := NewClientCredentials(creds(t, ""), d, time.Now)

	const n = 20
	var wg sync.WaitGroup
	toks := make([]string, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			toks[i], _ = cc.Token(context.Background())
		}()
	}
	// Wait for the first fetch to be in flight, give the rest time to queue.
	for d.count() == 0 {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	close(d.gate)
	wg.Wait()

	if d.count() != 1 {
		t.Errorf("fetches = %d, want 1", d.count())
	}
	for i, tok := range toks {
		if tok != "tok1" {
			t.Errorf("caller %d got %q", i, tok)
		}
	}
	if !cc.Fetched() {
		t.Error("Fetched() false after success")
	}
}
