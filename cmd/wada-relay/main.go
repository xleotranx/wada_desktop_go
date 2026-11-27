// Command wada-relay runs Wada's stateless zero-knowledge Noise NK WebSocket relay.
package main

import (
	"cmp"
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"wada-desktop-go/internal/relay"
)

func main() {
	var (
		listen     = flag.String("listen", cmp.Or(os.Getenv("WADA_RELAY_LISTEN"), ":8787"), "HTTP/WebSocket listen address")
		authToken  = flag.String("auth-token", os.Getenv("WADA_RELAY_TOKEN"), "Optional Bearer token required for daemon and client relay connections")
		maxClients = flag.Int("max-clients", 64, "Maximum concurrent remote clients per daemon")
	)
	flag.Parse()

	router := relay.NewRouter(relay.RouterConfig{
		AuthToken:           *authToken,
		MaxClientsPerDaemon: *maxClients,
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
