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
	"wada-desktop-go/internal/daemon/harness"
	"wada-desktop-go/internal/daemon/orchestrator"
	"wada-desktop-go/internal/daemon/pig"
	"wada-desktop-go/internal/daemon/preview"
	"wada-desktop-go/internal/daemon/server"
	"wada-desktop-go/internal/daemon/store"
	"wada-desktop-go/internal/daemon/terminal"
	"wada-desktop-go/internal/daemon/workspaces"
	"wada-desktop-go/internal/daemon/worktree"
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
	defaultBranchPrefix, ok := os.LookupEnv("WADA_BRANCH_PREFIX")
	if !ok {
		defaultBranchPrefix = worktree.DefaultBranchPrefix
	}
	fs := flag.NewFlagSet("wadad", flag.ContinueOnError)
	var (
		dataDir        = fs.String("data-dir", defDir, "Wada data directory")
		worktreeDir    = fs.String("worktree-dir", os.Getenv("WADA_WORKTREE_DIR"), "Absolute directory for new Git worktrees (default: worktrees under the data directory)")
		branchPrefix   = fs.String("branch-prefix", defaultBranchPrefix, "Git branch namespace for new worktrees (default wada/, empty disables the prefix)")
		listen         = fs.String("listen", "127.0.0.1:6767", "HTTP/WebSocket listen address (or \"auto\")")
		password       = fs.String("password", os.Getenv("WADA_PASSWORD"), "Optional daemon password")
		hostnames      = fs.String("hostnames", os.Getenv("WADA_HOSTNAMES"), "Comma-separated allowed Host headers")
		relayURL       = fs.String("relay-url", os.Getenv("WADA_RELAY_URL"), "Optional wada-relay URL for encrypted remote access")
		relayToken     = fs.String("relay-token", os.Getenv("WADA_RELAY_TOKEN"), "Optional Bearer token for connecting to wada-relay")
		appURL         = fs.String("app-url", os.Getenv("WADA_APP_URL"), "Base web app URL used in pairing links (default https://app.wada.dev)")
		publicURL      = fs.String("public-url", os.Getenv("WADA_PUBLIC_URL"), "Optional direct LAN/Tailscale URL advertised in pairing offers")
		webDir         = fs.String("web-dir", os.Getenv("WADA_WEB_DIR"), "Optional built Web/PWA directory served by wadad on /")
		desktopManaged = fs.Bool("desktop-managed", false, "Mark daemon as supervised by desktop app")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	storage := *worktreeDir
	if storage == "" {
		storage = filepath.Join(*dataDir, "worktrees")
	} else if !filepath.IsAbs(storage) {
		return fmt.Errorf("wadad: worktree-dir must be an absolute path")
	}
	// Terminals, harness workers and git inherit this process's environment;
	// they have no use for the daemon's secrets.
	for _, name := range []string{"WADA_PASSWORD", "WADA_RELAY_TOKEN"} {
		_ = os.Unsetenv(name)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	wtMgr, err := worktree.NewAt(ctx, storage, *branchPrefix)
	if err != nil {
		return fmt.Errorf("wadad: %w", err)
	}

	listenAddr := *listen
	if listenAddr == "auto" {
		listenAddr = "127.0.0.1:0"
		if ln, lerr := net.Listen("tcp", "127.0.0.1:6767"); lerr == nil {
			_ = ln.Close()
			listenAddr = "127.0.0.1:6767"
		}
	}

	// One daemon per data directory: a replacement waits here while the daemon
	// it replaces shuts down and removes its runtime files.
	unlock, err := lockDataDir(ctx, *dataDir, 15*time.Second)
	if err != nil {
		return fmt.Errorf("wadad: %w", err)
	}
	defer unlock()

	st, err := store.Open(ctx, *dataDir)
	if err != nil {
		return fmt.Errorf("wadad: open database: %w", err)
	}
	reg, err := workspaces.Open(ctx, st, workspaces.Options{Inbox: filepath.Join(*dataDir, "workspaces", "inbox")})
	if err != nil {
		_ = st.Close()
		return fmt.Errorf("wadad: read projects: %w", err)
	}
	spaces := workspaces.NewService(reg, wtMgr)
	harnesses := harness.DefaultRegistry()
	termSup := terminal.NewSupervisor(terminal.SupervisorOptions{
		DataDir:     *dataDir,
		KeepOnClose: true,
	})

	broker := &preview.Broker{}
	var orch *orchestrator.Orchestrator
	rt, err := pig.New(ctx, pig.Options{
		Root: filepath.Join(*dataDir, "pig"),
		ChiefTools: func(getCtx func() pig.ChiefContext) []pig.Tool {
			if orch == nil {
				return nil
			}
			return orch.ChiefTools(getCtx)
		},
	})
	if err != nil {
		_ = termSup.Close()
		_ = st.Close()
		return fmt.Errorf("wadad: start PiG runtime: %w", err)
	}

	orch, err = orchestrator.New(ctx, orchestrator.Options{
		DataDir:    *dataDir,
		PiG:        rt,
		Worktrees:  wtMgr,
		Workspaces: spaces,
		Preview:    broker,
		Registry:   harnesses,
		Terminals:  termSup,
	})
	if err != nil {
		_ = rt.Close()
		_ = termSup.Close()
		_ = st.Close()
		return fmt.Errorf("wadad: start orchestrator: %w", err)
	}

	var pwHash []byte
	if *password != "" {
		pwHash, err = bcrypt.GenerateFromPassword([]byte(*password), bcrypt.DefaultCost)
		if err != nil {
			_ = orch.Close()
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
		RelayURL:       strings.TrimSpace(*relayURL),
		RelayToken:     strings.TrimSpace(*relayToken),
		AppURL:         strings.TrimSpace(*appURL),
		PublicURL:      strings.TrimSpace(*publicURL),
		WebDir:         strings.TrimSpace(*webDir),
		DesktopManaged: *desktopManaged,
		Store:          st,
		Workspaces:     spaces,
		Preview:        broker,
		Backend:        orch,
		OnShutdown:     stop,
	})
	if err != nil {
		_ = orch.Close()
		return fmt.Errorf("wadad: init server: %w", err)
	}

	addr, err := srv.Start()
	if err != nil {
		_ = orch.Close()
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
