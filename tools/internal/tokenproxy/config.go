// Package tokenproxy implements a localhost reverse proxy that injects a
// Keycloak client-credentials bearer token into requests to the model endpoint.
package tokenproxy

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"time"
)

// Config holds the token proxy settings.
type Config struct {
	Listen           string
	HealthListen     string
	Upstream         *url.URL
	TokenURL         string
	ClientIDFile     string
	ClientSecretFile string
	Scope            string
	Timeout          time.Duration
}

// LoadConfig parses flags from args; environment values (UPSTREAM_URL,
// TOKEN_URL, CLIENT_ID_FILE, CLIENT_SECRET_FILE, TOKEN_SCOPE) fill any flag
// that was not set explicitly.
func LoadConfig(args []string, getenv func(string) string) (Config, error) {
	var cfg Config
	var upstream string

	fs := flag.NewFlagSet("token-proxy", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.Listen, "listen", "127.0.0.1:8081", "address of the proxy listener")
	fs.StringVar(&cfg.HealthListen, "health-listen", ":8082", "address of the health listener")
	fs.StringVar(&upstream, "upstream", "", "base URL of the model endpoint")
	fs.StringVar(&cfg.TokenURL, "token-url", "", "OAuth token endpoint")
	fs.StringVar(&cfg.ClientIDFile, "client-id-file", "", "file holding the client id")
	fs.StringVar(&cfg.ClientSecretFile, "client-secret-file", "", "file holding the client secret")
	fs.StringVar(&cfg.Scope, "scope", "", "optional OAuth scope")
	fs.DurationVar(&cfg.Timeout, "timeout", 10*time.Second, "token request timeout")
	if err := fs.Parse(args); err != nil {
		return Config{}, fmt.Errorf("parse flags: %w", err)
	}

	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	fromEnv := func(flagName, envName string, dst *string) {
		if !set[flagName] {
			if v := getenv(envName); v != "" {
				*dst = v
			}
		}
	}
	fromEnv("upstream", "UPSTREAM_URL", &upstream)
	fromEnv("token-url", "TOKEN_URL", &cfg.TokenURL)
	fromEnv("client-id-file", "CLIENT_ID_FILE", &cfg.ClientIDFile)
	fromEnv("client-secret-file", "CLIENT_SECRET_FILE", &cfg.ClientSecretFile)
	fromEnv("scope", "TOKEN_SCOPE", &cfg.Scope)

	for _, req := range []struct{ name, val string }{
		{"upstream", upstream},
		{"token-url", cfg.TokenURL},
		{"client-id-file", cfg.ClientIDFile},
		{"client-secret-file", cfg.ClientSecretFile},
	} {
		if req.val == "" {
			return Config{}, fmt.Errorf("validate config: %s is required", req.name)
		}
	}

	u, err := url.Parse(upstream)
	if err != nil {
		return Config{}, fmt.Errorf("parse upstream url: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return Config{}, errors.New("validate config: upstream must include scheme and host")
	}
	cfg.Upstream = u
	return cfg, nil
}
