//go:build windows

package terminal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"path/filepath"
	"syscall"

	"github.com/Microsoft/go-winio"
)

const createNoWindow = 0x08000000

func listenIPC(dataDir string) (ln net.Listener, endpoint string, cleanup func(), err error) {
	sum := sha256.Sum256([]byte(filepath.Clean(dataDir)))
	pipePath := `\\.\pipe\wada-term-` + hex.EncodeToString(sum[:8])
	ln, err = winio.ListenPipe(pipePath, nil)
	if err != nil {
		return nil, "", nil, err
	}
	return ln, pipePath, func() {}, nil
}

func dialIPC(ctx context.Context, endpoint string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, endpoint)
}

func detachedProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | createNoWindow,
	}
}
