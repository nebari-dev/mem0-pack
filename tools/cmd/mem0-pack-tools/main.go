// Command mem0-pack-tools holds the mem0 pack's small helper programs: the
// model token proxy and the one-time admin-key Secret generator.
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nebari-dev/mem0-pack/tools/internal/ensuresecret"
	"github.com/nebari-dev/mem0-pack/tools/internal/tokenproxy"
)

const (
	usage           = "usage: mem0-pack-tools <token-proxy|ensure-secret> [flags]\n"
	readHeaderLimit = 10 * time.Second
	idleLimit       = 120 * time.Second
	shutdownLimit   = 10 * time.Second
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "token-proxy":
		return runTokenProxy(args[1:], stderr)
	case "ensure-secret":
		return runEnsureSecret(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown subcommand %q\n%s", args[0], usage)
		return 2
	}
}

// isLoopback reports whether addr (host:port) binds only the loopback interface.
func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func runTokenProxy(args []string, stderr io.Writer) int {
	cfg, err := tokenproxy.LoadConfig(args, os.Getenv)
	if err != nil {
		fmt.Fprintf(stderr, "token-proxy: %v\n", err)
		return 2
	}
	if !isLoopback(cfg.Listen) {
		fmt.Fprintf(stderr, "token-proxy: -listen %q is not a loopback address; the proxy must not be reachable from outside its pod\n", cfg.Listen)
		return 2
	}
	log := slog.New(slog.NewJSONHandler(stderr, nil))

	hc := &http.Client{
		Timeout: cfg.Timeout,
		// A redirect would carry the client secret to another host.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	cc := tokenproxy.NewClientCredentials(cfg, hc, time.Now)

	proxySrv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           tokenproxy.NewHandler(cfg.Upstream, cc, log),
		ReadHeaderTimeout: readHeaderLimit,
		IdleTimeout:       idleLimit,
	}
	healthSrv := &http.Server{
		Addr:              cfg.HealthListen,
		Handler:           tokenproxy.NewHealth(cc),
		ReadHeaderTimeout: readHeaderLimit,
		IdleTimeout:       idleLimit,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	errc := make(chan error, 2)
	for _, s := range []*http.Server{proxySrv, healthSrv} {
		go func() { errc <- s.ListenAndServe() }()
	}
	log.Info("token-proxy started", "listen", cfg.Listen, "health", cfg.HealthListen)

	code := 0
	select {
	case <-ctx.Done():
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Error("serve", "error", err)
			code = 1
		}
	}
	sctx, cancel := context.WithTimeout(context.Background(), shutdownLimit)
	defer cancel()
	for _, s := range []*http.Server{proxySrv, healthSrv} {
		if err := s.Shutdown(sctx); err != nil {
			log.Error("shutdown", "error", err)
			code = 1
		}
	}
	return code
}

func runEnsureSecret(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ensure-secret", flag.ContinueOnError)
	fs.SetOutput(stderr)
	name := fs.String("name", "", "name of the Secret to create")
	key := fs.String("key", "", "data key that holds the generated value")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *name == "" || *key == "" {
		fmt.Fprintln(stderr, "ensure-secret: -name and -key are required")
		return 2
	}
	creator, ns, err := ensuresecret.InCluster()
	if err != nil {
		fmt.Fprintf(stderr, "ensure-secret: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cfg := ensuresecret.Config{Namespace: ns, Name: *name, Key: *key}
	if err := ensuresecret.Ensure(ctx, creator, cfg, rand.Reader); err != nil {
		fmt.Fprintf(stderr, "ensure-secret: %v\n", err)
		return 1
	}
	line, _ := json.Marshal(map[string]string{"msg": "secret ensured", "namespace": ns, "name": *name, "key": *key})
	fmt.Fprintf(stdout, "%s\n", line)
	return 0
}
