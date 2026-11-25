package tokenproxy

import (
	"testing"
	"time"
)

func TestLoadConfig(t *testing.T) {
	full := []string{"-upstream", "http://m:8080/v1", "-token-url", "http://k/token", "-client-id-file", "/id", "-client-secret-file", "/secret"}
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	allEnv := map[string]string{"UPSTREAM_URL": "http://env:9/v1", "TOKEN_URL": "http://env/token", "CLIENT_ID_FILE": "/eid", "CLIENT_SECRET_FILE": "/esecret", "TOKEN_SCOPE": "openid"}

	tests := []struct {
		name    string
		args    []string
		env     map[string]string
		wantErr bool
		check   func(t *testing.T, c Config)
	}{
		{name: "missing upstream", args: []string{"-token-url", "u", "-client-id-file", "a", "-client-secret-file", "b"}, wantErr: true},
		{name: "missing token url", args: []string{"-upstream", "http://m", "-client-id-file", "a", "-client-secret-file", "b"}, wantErr: true},
		{name: "missing client id file", args: []string{"-upstream", "http://m", "-token-url", "u", "-client-secret-file", "b"}, wantErr: true},
		{name: "missing client secret file", args: []string{"-upstream", "http://m", "-token-url", "u", "-client-id-file", "a"}, wantErr: true},
		{name: "upstream without scheme", args: []string{"-upstream", "m:8080/v1", "-token-url", "u", "-client-id-file", "a", "-client-secret-file", "b"}, wantErr: true},
		{name: "upstream bare host", args: []string{"-upstream", "model.example.com", "-token-url", "u", "-client-id-file", "a", "-client-secret-file", "b"}, wantErr: true},
		{name: "unknown flag", args: append([]string{"-bogus"}, full...), wantErr: true},
		{
			name: "defaults applied", args: full,
			check: func(t *testing.T, c Config) {
				if c.Listen != "127.0.0.1:8081" || c.HealthListen != ":8082" || c.Timeout != 10*time.Second {
					t.Errorf("defaults wrong: %+v", c)
				}
				if c.Upstream.Host != "m:8080" || c.Upstream.Path != "/v1" || c.Scope != "" {
					t.Errorf("values wrong: %+v", c)
				}
			},
		},
		{
			name: "env fills unset flags", env: allEnv,
			check: func(t *testing.T, c Config) {
				if c.Upstream.Host != "env:9" || c.TokenURL != "http://env/token" || c.ClientIDFile != "/eid" || c.ClientSecretFile != "/esecret" || c.Scope != "openid" {
					t.Errorf("env not applied: %+v", c)
				}
			},
		},
		{
			name: "flags win over env", args: append([]string{"-scope", "flag"}, full...), env: allEnv,
			check: func(t *testing.T, c Config) {
				if c.Upstream.Host != "m:8080" || c.ClientIDFile != "/id" || c.Scope != "flag" {
					t.Errorf("flags should win: %+v", c)
				}
			},
		},
		{
			name: "overrides listen and timeout", args: append([]string{"-listen", "127.0.0.1:9", "-health-listen", ":9", "-timeout", "3s"}, full...),
			check: func(t *testing.T, c Config) {
				if c.Listen != "127.0.0.1:9" || c.HealthListen != ":9" || c.Timeout != 3*time.Second {
					t.Errorf("overrides wrong: %+v", c)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := LoadConfig(tt.args, env(tt.env))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.check != nil {
				tt.check(t, c)
			}
		})
	}
}
