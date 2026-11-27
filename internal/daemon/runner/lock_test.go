package runner

import (
	"errors"
	"testing"
	"time"
)

func TestDataDirLockIsExclusiveAndWaits(t *testing.T) {
	dir := t.TempDir()
	release, err := lockDataDir(t.Context(), dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockDataDir(t.Context(), dir, 0); !errors.Is(err, errLocked) {
		t.Fatalf("second lock = %v, want errLocked", err)
	}
	time.AfterFunc(150*time.Millisecond, release)
	release2, err := lockDataDir(t.Context(), dir, 5*time.Second)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	release2()
}
