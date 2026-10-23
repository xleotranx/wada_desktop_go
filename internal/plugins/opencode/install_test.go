package opencode

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"wada-desktop-go/internal/contract/agent"

	"github.com/xleotranx/wada-kit/kernel/kerneltest"
	"github.com/xleotranx/wada-kit/kittest"
)

// tarball is an npm package holding package/bin/opencode with body.
func tarball(t *testing.T, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, data := range map[string]string{"package/package.json": "{}", "package/bin/" + binary: body} {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg})
		tw.Write([]byte(data))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func integrity(b []byte) string {
	sum := sha512.Sum512(b)
	return "sha512-" + base64.StdEncoding.EncodeToString(sum[:])
}

// serve answers every request with body and counts them.
func serve(t *testing.T, body []byte) (string, *atomic.Int32) {
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write(body)
	}))
	t.Cleanup(s.Close)
	return s.URL + "/opencode.tgz", &hits
}

func TestInstallDownloadsOnceAndRemovesOtherVersions(t *testing.T) {
	tgz := tarball(t, "#!/bin/sh\n")
	url, hits := serve(t, tgz)
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "1.0.0"), 0o755)
	r := release{Version: "9.9.9", URL: url, Integrity: integrity(tgz)}
	var last progress
	exe, err := install(context.Background(), root, r, func(p progress) { last = p })
	if err != nil {
		t.Fatal(err)
	}
	if exe != filepath.Join(root, "9.9.9", binary) {
		t.Fatalf("exe %q", exe)
	}
	info, err := os.Stat(exe)
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("exe %v, %v", info, err)
	}
	if last.Done != int64(len(tgz)) || last.Version != "9.9.9" {
		t.Fatalf("progress %+v", last)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 || entries[0].Name() != "9.9.9" {
		t.Fatalf("root holds %v", entries)
	}
	if left, _ := os.ReadDir(filepath.Join(root, "9.9.9")); len(left) != 1 {
		t.Fatalf("version folder holds %v", left)
	}
	if _, err := install(context.Background(), root, r, func(progress) {}); err != nil || hits.Load() != 1 {
		t.Fatalf("second install: %v after %d downloads", err, hits.Load())
	}
}

func TestInstallRefusesAMismatchedDownload(t *testing.T) {
	tgz := tarball(t, "#!/bin/sh\n")
	url, _ := serve(t, tgz)
	root := t.TempDir()
	r := release{Version: "9.9.9", URL: url, Integrity: integrity([]byte("something else"))}
	_, err := install(context.Background(), root, r, func(progress) {})
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("err %v", err)
	}
	if left, _ := os.ReadDir(filepath.Join(root, "9.9.9")); len(left) != 0 {
		t.Fatalf("left behind %v", left)
	}
}

func TestManagedServerRunsTheDownloadedBuild(t *testing.T) {
	t.Setenv("WADA_FAKE_OPENCODE", "1")
	tgz := tarball(t, "#!/bin/sh\nexec \""+os.Args[0]+"\" \"$@\"\n")
	url, _ := serve(t, tgz)
	host := &kittest.Host{Data: t.TempDir()}
	var p *Plugin
	h := kerneltest.New(t, host.New, kerneltest.Keep(&p, func() *Plugin {
		return &Plugin{dir: t.TempDir(), rel: release{Version: "9.9.9", URL: url, Integrity: integrity(tgz)}}
	}))
	waitFor(t, h, "ready", func() bool { return p.Status().State == agent.Ready })
	if _, err := os.Stat(filepath.Join(host.Data, "opencode", "bin", "9.9.9", binary)); err != nil {
		t.Fatal(err)
	}
	if p.fetch != nil {
		t.Fatalf("download still shown: %+v", p.fetch)
	}
	home := filepath.Join(host.Data, "opencode")
	if p.Status().Origin != "opencode "+home {
		t.Fatalf("origin %q", p.Status().Origin)
	}
	c, err := p.current()
	if err != nil || !slices.Contains(c.cmd.Env, "XDG_DATA_HOME="+filepath.Join(home, "data")) ||
		!slices.Contains(c.cmd.Env, "XDG_STATE_HOME="+filepath.Join(home, "state")) {
		t.Fatalf("server env %v, %v", c, err)
	}
	h.Disable("opencode")
}

func TestStatusShowsTheDownload(t *testing.T) {
	st := agent.Status{State: agent.Connecting}
	if got := statusText(st, &progress{Version: "2.1.2", Done: 3 << 20, Total: 90 << 20}); got != "Downloading OpenCode 2.1.2… 3 of 90 MB" {
		t.Fatalf("got %q", got)
	}
	if got := statusText(st, nil); got != "Connecting…" {
		t.Fatalf("got %q", got)
	}
}
