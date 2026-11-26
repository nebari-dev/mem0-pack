package ensuresecret

import (
	"context"
	"errors"
	"io"
	"regexp"
	"strings"
	"testing"
)

type fakeCreator struct {
	calls         int
	ns, name, key string
	value         []byte
	err           error
}

func (f *fakeCreator) Create(_ context.Context, ns, name, key string, value []byte) error {
	f.calls++
	f.ns, f.name, f.key, f.value = ns, name, key, value
	return f.err
}

func TestEnsure(t *testing.T) {
	cfg := Config{Namespace: "ns", Name: "mem0-admin", Key: "ADMIN_API_KEY"}
	boom := errors.New("boom")
	hex64 := regexp.MustCompile(`^[0-9a-f]{64}$`)

	tests := []struct {
		name        string
		random      io.Reader
		createErr   error
		wantErr     bool
		wantErrIs   error
		wantErrText string
		wantCalls   int
		wantValue   string
	}{
		{
			name:      "creates 64 hex chars from 32 bytes",
			random:    strings.NewReader(strings.Repeat("\xab", 32)),
			wantCalls: 1,
			wantValue: strings.Repeat("ab", 32),
		},
		{
			name:        "short reader errors and creates nothing",
			random:      strings.NewReader("short"),
			wantErr:     true,
			wantErrText: "read random bytes",
			wantCalls:   0,
		},
		{
			name:        "creator error is wrapped",
			random:      strings.NewReader(strings.Repeat("\x01", 32)),
			createErr:   boom,
			wantErr:     true,
			wantErrIs:   boom,
			wantErrText: "create secret",
			wantCalls:   1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeCreator{err: tc.createErr}
			err := Ensure(context.Background(), f, cfg, tc.random)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErrIs != nil && !errors.Is(err, tc.wantErrIs) {
				t.Errorf("err %v does not wrap %v", err, tc.wantErrIs)
			}
			if tc.wantErrText != "" && !strings.Contains(err.Error(), tc.wantErrText) {
				t.Errorf("err %q missing %q", err, tc.wantErrText)
			}
			if f.calls != tc.wantCalls {
				t.Fatalf("calls = %d, want %d", f.calls, tc.wantCalls)
			}
			if tc.wantCalls == 0 {
				return
			}
			if f.ns != "ns" || f.name != "mem0-admin" || f.key != "ADMIN_API_KEY" {
				t.Errorf("got ns/name/key %q/%q/%q", f.ns, f.name, f.key)
			}
			if !hex64.Match(f.value) {
				t.Errorf("value %q is not 64 hex chars", f.value)
			}
			if tc.wantValue != "" && string(f.value) != tc.wantValue {
				t.Errorf("value = %q, want %q", f.value, tc.wantValue)
			}
		})
	}
}
