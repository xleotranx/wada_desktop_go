// Command wadad runs Wada's headless coding-agent daemon.
package main

import (
	"context"
	"flag"
	"log"
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

const daemonVersion = "0.1.0-dev"

func defaultDataDir() string {
	if env := strings.TrimSpace(os.Getenv("WADA_DATA_DIR")); env != "" {
		return filepath.Clean(env)
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		log.Fatalf("wadad: resolve config dir: %v", err)
	}
	return filepath.Join(cfg, "wada-desktop-go")
}

func main() {
	var (
		dataDir        = flag.String("data-dir", defaultDataDir(), "Wada data directory")
		listen         = flag.String("listen", "127.0.0.1:6767", "HTTP/WebSocket listen address")
		password       = flag.String("password", os.Getenv("WADA_PASSWORD"), "Optional daemon password")
		hostnames      = flag.String("hostnames", os.Getenv("WADA_HOSTNAMES"), "Comma-separated allowed Host headers")
		desktopManaged = flag.Bool("desktop-managed", false, "Mark daemon as supervised by desktop app")
	)
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	rt, err := pig.New(ctx, pig.Options{Root: filepath.Join(*dataDir, "pig")})
	if err != nil {
		log.Fatalf("wadad: start PiG runtime: %v", err)
	}

	var pwHash []byte
	if *password != "" {
		pwHash, err = bcrypt.GenerateFromPassword([]byte(*password), bcrypt.DefaultCost)
		if err != nil {
			_ = rt.Close()
			log.Fatalf("wadad: hash password: %v", err)
		}
	}

	var extraHosts []string
	if *hostnames != "" {
		extraHosts = strings.Split(*hostnames, ",")
	}

	srv, err := server.New(ctx, server.Config{
		DataDir:        *dataDir,
		Listen:         *listen,
		Version:        daemonVersion,
		PasswordHash:   pwHash,
		Hostnames:      extraHosts,
		DesktopManaged: *desktopManaged,
		Backend:        rt,
		OnShutdown:     stop,
	})
	if err != nil {
		_ = rt.Close()
		log.Fatalf("wadad: init server: %v", err)
	}

	addr, err := srv.Start()
	if err != nil {
		_ = rt.Close()
		log.Fatalf("wadad: listen %s: %v", *listen, err)
	}
	log.Printf("wadad %s listening on %s (serverId=%s, dataDir=%s)", daemonVersion, addr, srv.ServerID(), *dataDir)

	<-ctx.Done()
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Printf("wadad: shutdown error: %v", err)
	}
}
