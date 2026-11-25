package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunUsage(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantStderr string
	}{
		{"no args", []string{}, "usage:"},
		{"unknown subcommand", []string{"bogus"}, "unknown subcommand"},
		{"ensure-secret without name", []string{"ensure-secret", "-key", "k"}, "-name and -key are required"},
		{"ensure-secret without key", []string{"ensure-secret", "-name", "n"}, "-name and -key are required"},
		{"token-proxy missing config", []string{"token-proxy"}, "is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			if got := run(tt.args, &out, &errb); got != 2 {
				t.Fatalf("exit code = %d, want 2", got)
			}
			if !strings.Contains(errb.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", errb.String(), tt.wantStderr)
			}
		})
	}
}

func TestTokenProxyRefusesNonLoopbackListen(t *testing.T) {
	base := []string{
		"token-proxy",
		"-upstream", "http://model.invalid/v1",
		"-token-url", "http://idp.invalid/token",
		"-client-id-file", "/nonexistent/id",
		"-client-secret-file", "/nonexistent/secret",
	}
	for _, listen := range []string{":8081", "0.0.0.0:8081", "[::]:8081", "10.1.2.3:8081", "example.com:8081", "8081"} {
		t.Run(listen, func(t *testing.T) {
			var out, errb bytes.Buffer
			args := append(append([]string{}, base...), "-listen", listen)
			if got := run(args, &out, &errb); got != 2 {
				t.Fatalf("exit code = %d, want 2", got)
			}
			if !strings.Contains(errb.String(), "not a loopback address") {
				t.Errorf("stderr = %q, want loopback refusal", errb.String())
			}
		})
	}
}

func TestIsLoopback(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:8081", true},
		{"127.5.6.7:1", true},
		{"[::1]:8081", true},
		{"localhost:8081", true},
		{":8081", false},
		{"0.0.0.0:8081", false},
		{"[::]:8081", false},
		{"192.168.1.1:80", false},
		{"nohostport", false},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			if got := isLoopback(tt.addr); got != tt.want {
				t.Errorf("isLoopback(%q) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}
