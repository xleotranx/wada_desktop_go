package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// lockFileName is held exclusively by the one wadad serving a data directory,
// from before it opens its stores until after it removes its runtime files.
const lockFileName = "wadad.lock"

var errLocked = errors.New("data directory is locked by another wadad")

// lockDataDir takes the data directory's daemon lock, waiting up to wait for a
// daemon that is shutting down to let go of it. The returned func releases it.
func lockDataDir(ctx context.Context, dataDir string, wait time.Duration) (func(), error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dataDir, lockFileName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		err := tryLock(f)
		if err == nil {
			return func() { _ = unlock(f); _ = f.Close() }, nil
		}
		if !errors.Is(err, errLocked) {
			_ = f.Close()
			return nil, err
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("%w: %s", errLocked, dataDir)
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
