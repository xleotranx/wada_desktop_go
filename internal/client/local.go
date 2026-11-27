package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"wada-desktop-go/internal/protocol"
)

const (
	pidFileName        = "wadad.pid"
	credentialFileName = "local-credential"
)

// PIDRecord mirrors wadad's runtime PID file without importing internal/daemon.
type PIDRecord struct {
	PID            int    `json:"pid"`
	StartedAt      int64  `json:"startedAt"`
	Listen         string `json:"listen"`
	Version        string `json:"version"`
	DesktopManaged bool   `json:"desktopManaged,omitempty"`
}

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

// ReadLocalEndpoint reads wadad.pid and local-credential from dataDir.
func ReadLocalEndpoint(dataDir string) (PIDRecord, string, error) {
	rawPID, err := os.ReadFile(filepath.Join(dataDir, pidFileName))
	if err != nil {
		return PIDRecord{}, "", err
	}
	var rec PIDRecord
	if err := json.Unmarshal(rawPID, &rec); err != nil {
		return PIDRecord{}, "", err
	}
	cred, err := os.ReadFile(filepath.Join(dataDir, credentialFileName))
	if err != nil {
		return PIDRecord{}, "", err
	}
	return rec, strings.TrimSpace(string(cred)), nil
}

// ConnectOrStartLocal attaches to an already-running healthy local wadad under
// dataDir, or spawns a detached `os.Executable() --daemon --desktop-managed`
// worker if none is running (or replaces an outdated desktop-managed daemon).
func ConnectOrStartLocal(ctx context.Context, dataDir, appVersion string, opts Options) (*Client, protocol.DaemonStatusResponse, error) {
	if dataDir == "" {
		var err error
		dataDir, err = DefaultDataDir()
		if err != nil {
			return nil, protocol.DaemonStatusResponse{}, err
		}
	}

	dialWithStatus := func(dctx context.Context) (*Client, protocol.DaemonStatusResponse, error) {
		cli, err := DialLocal(dctx, dataDir, opts)
		if err != nil {
			return nil, protocol.DaemonStatusResponse{}, err
		}
		st, err := cli.Status(dctx)
		if err != nil {
			_ = cli.Close()
			return nil, protocol.DaemonStatusResponse{}, err
		}
		return cli, st, nil
	}

	if cli, st, err := dialWithStatus(ctx); err == nil {
		if !st.DesktopManaged || appVersion == "" || st.Version == appVersion {
			return cli, st, nil
		}
		// Replace outdated desktop-managed daemon cleanly.
		_ = cli.Shutdown(ctx)
		_ = cli.Close()
		waitExitDeadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(waitExitDeadline) {
			if _, _, rerr := ReadLocalEndpoint(dataDir); rerr != nil {
				break
			}
			select {
			case <-ctx.Done():
				return nil, protocol.DaemonStatusResponse{}, ctx.Err()
			case <-time.After(50 * time.Millisecond):
			}
		}
	}

	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, protocol.DaemonStatusResponse{}, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, protocol.DaemonStatusResponse{}, fmt.Errorf("resolve executable for daemon launch: %w", err)
	}
	logFile, err := os.OpenFile(filepath.Join(dataDir, "wadad.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, protocol.DaemonStatusResponse{}, err
	}

	cmd := exec.Command(exe, "--daemon", "--desktop-managed", "--data-dir", dataDir, "--listen", "auto")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = detachedProcAttr()
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, protocol.DaemonStatusResponse{}, fmt.Errorf("start local wadad: %w", err)
	}
	_ = logFile.Close()
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait() }()

	deadline := time.Now().Add(10 * time.Second)
	for attempt := 0; time.Now().Before(deadline); attempt++ {
		if rec, _, rerr := ReadLocalEndpoint(dataDir); rerr == nil && (rec.PID == pid || attempt >= 5) {
			dialCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			cli, st, derr := dialWithStatus(dialCtx)
			cancel()
			if derr == nil {
				return cli, st, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, protocol.DaemonStatusResponse{}, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return nil, protocol.DaemonStatusResponse{}, errors.New("timed out waiting for local wadad to start")
}
