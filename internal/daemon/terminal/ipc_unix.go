//go:build !windows

package terminal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
)

func listenIPC(dataDir string) (ln net.Listener, endpoint string, cleanup func(), err error) {
	dir := filepath.Join(filepath.Clean(dataDir), "terminal-worker")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", nil, err
	}
	sockPath := filepath.Join(dir, "worker.sock")
	if len(sockPath) > 96 {
		sum := sha256.Sum256([]byte(dataDir))
		sockPath = filepath.Join(os.TempDir(), fmt.Sprintf("wt-%d-%s.sock", os.Getuid(), hex.EncodeToString(sum[:6])))
	}
	_ = os.Remove(sockPath)
	ln, err = net.Listen("unix", sockPath)
	if err != nil {
		return nil, "", nil, err
	}
	_ = os.Chmod(sockPath, 0o600)
	return ln, sockPath, func() { _ = os.Remove(sockPath) }, nil
}

func dialIPC(ctx context.Context, endpoint string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", endpoint)
}

func detachedProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
