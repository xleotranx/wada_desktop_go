package client

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadLocalEndpointAndMissingFiles(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := ReadLocalEndpoint(dir); err == nil {
		t.Fatal("expected error for missing runtime files")
	}
	if err := os.WriteFile(filepath.Join(dir, pidFileName), []byte(`{"pid":123,"listen":"127.0.0.1:6767"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, credentialFileName), []byte("secret-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec, cred, err := ReadLocalEndpoint(dir)
	if err != nil {
		t.Fatal(err)
	}
	if rec.PID != 123 || rec.Listen != "127.0.0.1:6767" || cred != "secret-token" {
		t.Fatalf("got rec=%+v cred=%q", rec, cred)
	}
}

func TestNormalizeWSURL(t *testing.T) {
	for in, want := range map[string]string{
		"127.0.0.1:6767":          "ws://127.0.0.1:6767/ws",
		"ws://127.0.0.1:6767":     "ws://127.0.0.1:6767/ws",
		"wss://example.com/ws":    "wss://example.com/ws",
	} {
		if got := normalizeWSURL(in); got != want {
			t.Fatalf("normalizeWSURL(%q) = %q, want %q", in, got, want)
		}
	}
}
