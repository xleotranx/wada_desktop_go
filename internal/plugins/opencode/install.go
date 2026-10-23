package opencode

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// release is the OpenCode build the app runs: an npm tarball whose
// package/bin/opencode (opencode.exe on Windows) is the server, checked against the registry's
// integrity before it is unpacked.
type release struct {
	Version   string
	URL       string
	Integrity string // "sha512-<base64>", as npm publishes it
}

// version is the OpenCode release the app downloads and runs.
const version = "2.1.2"

// releases holds the pinned build for each platform the app supports.
var releases = map[string]release{
	"darwin/arm64": {version,
		"https://registry.npmjs.org/@opencode/cli-darwin-arm64/-/cli-darwin-arm64-" + version + ".tgz",
		"sha512-H8Nx7ksbHpd5u0Q9BQMj2Mt5vs+dC/t3AslxrmJDY5TZbPyHt6OKJ5TMJc6nQZAFKPrVFGOA+TdzQ9tU5qt6Sg=="},
	// The baseline build runs on every Intel Mac, with or without AVX2.
	"darwin/amd64": {version,
		"https://registry.npmjs.org/@opencode/cli-darwin-x64-baseline/-/cli-darwin-x64-baseline-" + version + ".tgz",
		"sha512-s3gu8lFo7/pEC+GP5QBvVXeqMj5ZwhraG5vL3Han2DV9v30/m6hjIgejS75wdb/Y5p0SWgrDOJM/ZAHb8vCj6A=="},
	// As on Intel Macs, the baseline build runs with or without AVX2.
	"windows/amd64": {version,
		"https://registry.npmjs.org/@opencode/cli-windows-x64-baseline/-/cli-windows-x64-baseline-" + version + ".tgz",
		"sha512-TcrUfc1GcjeNtaCFebhCS8+zWhPqkuAhGEGQUkdlEy7Z1BwBHV6T4/c/IR54NRgUg45vA7zEIzGIghvUBHrnRQ=="},
	"windows/arm64": {version,
		"https://registry.npmjs.org/@opencode/cli-windows-arm64/-/cli-windows-arm64-" + version + ".tgz",
		"sha512-PWmp5UQ6sVNQaTiDXdQEw3sq2TumJDqs6cQsNZ9EW77mOzZcOcW7jVA+3u1bdkPDoYcmv+NfTLhOl3t+C6hPUQ=="},
}

// binary is the server's file name, in the tarball's package/bin and
// installed.
var binary = func() string {
	if runtime.GOOS == "windows" {
		return "opencode.exe"
	}
	return "opencode"
}()

func platformRelease() (release, error) {
	r, ok := releases[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok {
		return release{}, fmt.Errorf("no OpenCode build for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	return r, nil
}

// progress is a download under way: bytes received of total, which is 0
// when the server doesn't say.
type progress struct {
	Version     string
	Done, Total int64
}

// install returns r's executable under root (root/<version>/opencode),
// downloading and unpacking it first if it is missing. Other versions under
// root are removed once r is in place. report is called from the worker.
func install(ctx context.Context, root string, r release, report func(progress)) (string, error) {
	dir := filepath.Join(root, r.Version)
	exe := filepath.Join(dir, binary)
	if info, err := os.Stat(exe); err == nil && info.Mode().IsRegular() {
		return exe, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tgz, err := download(ctx, dir, r, report)
	if err != nil {
		return "", fmt.Errorf("downloading OpenCode %s: %w", r.Version, err)
	}
	defer os.Remove(tgz)
	if err := unpack(tgz, exe); err != nil {
		return "", fmt.Errorf("unpacking OpenCode %s: %w", r.Version, err)
	}
	if old, err := os.ReadDir(root); err == nil {
		for _, e := range old {
			if e.Name() != r.Version {
				os.RemoveAll(filepath.Join(root, e.Name()))
			}
		}
	}
	return exe, nil
}

// download saves r's tarball in dir and returns its path once it matches
// r.Integrity.
func download(ctx context.Context, dir string, r release, report func(progress)) (string, error) {
	want, ok := strings.CutPrefix(r.Integrity, "sha512-")
	if !ok {
		return "", fmt.Errorf("unsupported integrity %q", r.Integrity)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.URL, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s answered %s", r.URL, resp.Status)
	}
	f, err := os.CreateTemp(dir, "download-*.tgz")
	if err != nil {
		return "", err
	}
	keep := false
	defer func() {
		f.Close()
		if !keep {
			os.Remove(f.Name())
		}
	}()
	sum := sha512.New()
	p := progress{Version: r.Version, Total: max(resp.ContentLength, 0)}
	report(p)
	last := time.Now()
	buf := make([]byte, 256<<10)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return "", werr
			}
			sum.Write(buf[:n])
			p.Done += int64(n)
			if time.Since(last) > 100*time.Millisecond {
				last = time.Now()
				report(p)
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
	}
	report(p)
	if got := base64.StdEncoding.EncodeToString(sum.Sum(nil)); got != want {
		return "", errors.New("the download doesn't match the published checksum")
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	keep = true
	return f.Name(), nil
}

// unpack writes the tarball's package/bin/<binary> to exe, through a
// temporary file so a partial binary is never left in place.
func unpack(tgz, exe string) error {
	f, err := os.Open(tgz)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return errors.New("package/bin/" + binary + " is missing")
		}
		if err != nil {
			return err
		}
		if h.Name != "package/bin/"+binary || h.Typeflag != tar.TypeReg {
			continue
		}
		out, err := os.CreateTemp(filepath.Dir(exe), "opencode-*")
		if err != nil {
			return err
		}
		_, err = io.Copy(out, tr)
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err == nil {
			err = os.Chmod(out.Name(), 0o755)
		}
		if err == nil {
			err = os.Rename(out.Name(), exe)
		}
		if err != nil {
			os.Remove(out.Name())
		}
		return err
	}
}
