package tokenproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const maxRefreshMargin = 30 * time.Second

// TokenSource returns a bearer token.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// Doer sends an HTTP request; *http.Client satisfies it.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// ClientCredentials fetches and caches a client-credentials token. A mutex is
// held across the fetch, so concurrent callers share one request.
type ClientCredentials struct {
	cfg Config
	hc  Doer
	now func() time.Time

	mu        sync.Mutex
	token     string
	refreshAt time.Time

	fetched atomic.Bool
}

// NewClientCredentials builds a token source from cfg.
func NewClientCredentials(cfg Config, hc Doer, now func() time.Time) *ClientCredentials {
	return &ClientCredentials{cfg: cfg, hc: hc, now: now}
}

// Fetched reports whether a token has been fetched successfully at least once.
func (c *ClientCredentials) Fetched() bool { return c.fetched.Load() }

// Token returns the cached token, fetching a new one when none is cached or
// less than min(30s, lifetime/4) of its life remains.
func (c *ClientCredentials) Token(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.token != "" && c.now().Before(c.refreshAt) {
		return c.token, nil
	}
	tok, lifetime, err := c.fetch(ctx)
	if err != nil {
		c.token = ""
		return "", err
	}
	margin := min(maxRefreshMargin, lifetime/4)
	c.token = tok
	c.refreshAt = c.now().Add(lifetime - margin)
	c.fetched.Store(true)
	return tok, nil
}

func (c *ClientCredentials) fetch(ctx context.Context) (string, time.Duration, error) {
	id, err := readTrimmed(c.cfg.ClientIDFile)
	if err != nil {
		return "", 0, fmt.Errorf("read client id: %w", err)
	}
	secret, err := readTrimmed(c.cfg.ClientSecretFile)
	if err != nil {
		return "", 0, fmt.Errorf("read client secret: %w", err)
	}

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {id},
		"client_secret": {secret},
	}
	if c.cfg.Scope != "" {
		form.Set("scope", c.cfg.Scope)
	}

	if c.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.cfg.Timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("send token request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", 0, fmt.Errorf("read token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// The body is not included: an error page could echo credentials.
		return "", 0, fmt.Errorf("fetch token: unexpected status %d", resp.StatusCode)
	}

	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", 0, fmt.Errorf("decode token response: %w", err)
	}
	if tr.AccessToken == "" {
		return "", 0, fmt.Errorf("decode token response: missing access_token")
	}
	if tr.ExpiresIn <= 0 {
		return "", 0, fmt.Errorf("decode token response: expires_in must be positive, got %d", tr.ExpiresIn)
	}
	return tr.AccessToken, time.Duration(tr.ExpiresIn) * time.Second, nil
}

func readTrimmed(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
