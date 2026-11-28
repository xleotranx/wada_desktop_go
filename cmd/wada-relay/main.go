// Command wada-relay runs Wada's stateless zero-knowledge Noise NK WebSocket relay.
package main

import (
	"cmp"
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"wada-desktop-go/internal/relay"
)

func main() {
	var (
		listen     = flag.String("listen", cmp.Or(os.Getenv("WADA_RELAY_LISTEN"), ":8787"), "HTTP/WebSocket listen address")
		authToken  = flag.String("auth-token", os.Getenv("WADA_RELAY_TOKEN"), "Optional Bearer token required for daemon and client relay connections")
		maxClients = flag.Int("max-clients", 64, "Maximum concurrent remote clients per daemon")
		proxies    = flag.String("trusted-proxies", os.Getenv("WADA_RELAY_TRUSTED_PROXIES"), "Comma-separated networks (CIDR) of proxies whose X-Forwarded-For names the client, e.g. a TLS proxy in front of the relay")
		check      = flag.String("check", "", "Probe this health URL (e.g. http://127.0.0.1:8787/healthz) and exit 0 if it answers OK, for container healthchecks")
	)
	flag.Parse()
	if *check != "" {
		os.Exit(probe(*check))
	}

	trusted, err := parsePrefixes(*proxies)
	if err != nil {
		log.Fatalf("wada-relay: -trusted-proxies: %v", err)
	}
	router := relay.NewRouter(relay.RouterConfig{
		AuthToken:           *authToken,
		MaxClientsPerDaemon: *maxClients,
		TrustedProxies:      trusted,
	})

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatalf("wada-relay: listen %s: %v", *listen, err)
	}
	srv := &http.Server{
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("wada-relay listening on %s (authRequired=%v, maxClients=%d)", ln.Addr().String(), *authToken != "", *maxClients)
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Fatalf("wada-relay: serve: %v", err)
		}
	}()

	<-ctx.Done()
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Printf("wada-relay: shutdown: %v", err)
	}
}

// parsePrefixes parses a comma-separated list of CIDR networks.
func parsePrefixes(list string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for part := range strings.SplitSeq(list, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		p, err := netip.ParsePrefix(part)
		if err != nil {
			return nil, err
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

// probe reports through its exit code whether url answers 200 OK.
func probe(url string) int {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		log.Printf("wada-relay: health check: %v", err)
		return 1
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("wada-relay: health check: %s", resp.Status)
		return 1
	}
	return 0
}
