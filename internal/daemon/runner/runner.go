// Package runner hosts the shared CLI entrypoint for wadad, used by both the
// standalone wadad binary and `wada-desktop --daemon`.
package runner

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"
	"wada-desktop-go/internal/daemon/pig"
	"wada-desktop-go/internal/daemon/server"
)

// Version identifies this wadad build to attaching clients.
const Version = "0.1.0-dev"

// DefaultDataDir resolves Wada's data directory (WADA_DATA_DIR or OS config dir).
func DefaultDataDir() (string, error) {
	if env := strings.TrimSpace(os.Getenv("WADA_DATA_DIR")); env != "" {
		return filepath.Clean(env), nil
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg, "wada-desktop-go"), nil
}

// Run parses wadad flags, starts the headless daemon server, and blocks until
// interrupted or stopped via RPC.
func Run(args []string) error {
	defDir, err := DefaultDataDir()
	if err != nil {
		return fmt.Errorf("wadad: resolve config dir: %w", err)
	}
	fs := flag.NewFlagSet("wadad", flag.ContinueOnError)
	var (
		dataDir        = fs.String("data-dir", defDir, "Wada data directory")
		listen         = fs.String("listen", "127.0.0.1:6767", "HTTP/WebSocket listen address (or \"auto\")")
		password       = fs.String("password", os.Getenv("WADA_PASSWORD"), "Optional daemon password")
		hostnames      = fs.String("hostnames", os.Getenv("WADA_HOSTNAMES"), "Comma-separated allowed Host headers")
		desktopManaged = fs.Bool("desktop-managed", false, "Mark daemon as supervised by desktop app")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}

	listenAddr := *listen
	if listenAddr == "auto" {
		listenAddr = "127.0.0.1:0"
		if ln, lerr := net.Listen("tcp", "127.0.0.1:6767"); lerr == nil {
			_ = ln.Close()
			listenAddr = "127.0.0.1:6767"
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	rt, err := pig.New(ctx, pig.Options{Root: filepath.Join(*dataDir, "pig")})
	if err != nil {
		return fmt.Errorf("wadad: start PiG runtime: %w", err)
	}

	var pwHash []byte
	if *password != "" {
		pwHash, err = bcrypt.GenerateFromPassword([]byte(*password), bcrypt.DefaultCost)
		if err != nil {
			_ = rt.Close()
			return fmt.Errorf("wadad: hash password: %w", err)
		}
	}

	var extraHosts []string
	if *hostnames != "" {
		extraHosts = strings.Split(*hostnames, ",")
	}

	srv, err := server.New(ctx, server.Config{
		DataDir:        *dataDir,
		Listen:         listenAddr,
		Version:        Version,
		PasswordHash:   pwHash,
		Hostnames:      extraHosts,
		DesktopManaged: *desktopManaged,
		Backend:        rt,
		OnShutdown:     stop,
	})
	if err != nil {
		_ = rt.Close()
		return fmt.Errorf("wadad: init server: %w", err)
	}

	addr, err := srv.Start()
	if err != nil {
		_ = rt.Close()
		return fmt.Errorf("wadad: listen %s: %w", listenAddr, err)
	}
	log.Printf("wadad %s listening on %s (serverId=%s, dataDir=%s, managed=%v)", Version, addr, srv.ServerID(), *dataDir, *desktopManaged)

	<-ctx.Done()
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Printf("wadad: shutdown error: %v", err)
	}
	return nil
}
