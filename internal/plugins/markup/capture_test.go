package markup

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestScreenCaptureDistinguishesCancellationFromFailure(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("screencapture is available on macOS")
	}
	for _, tc := range []struct {
		name, script, problem string
	}{
		{"cancel", "exit 0", ""},
		{"cancel with exit status", "exit 1", ""},
		{"failure without output", "exit 2", "exit status 2"},
		{"failure with diagnostic", "echo 'could not create image from display' >&2\nexit 1", "could not create image from display"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "screencapture"), []byte("#!/bin/sh\n"+tc.script+"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			capture := screencapture()
			if capture == nil {
				t.Fatal("capture unavailable")
			}
			data, err := capture(context.Background())
			if data != nil {
				t.Fatal("a command without an output file returned an image")
			}
			if tc.problem == "" {
				if err != nil {
					t.Fatalf("cancellation reported: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.problem) {
				t.Fatalf("error %v, want %q", err, tc.problem)
			}
		})
	}
}
