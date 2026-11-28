package client

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestStdioBridgeStartsAndCarriesTheDaemon(t *testing.T) {
	dir := t.TempDir()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); _ = c.Close() }()
		}
	}()
	// start stands in for launching wadad: it writes the runtime files.
	started := 0
	start := func() error {
		started++
		rec, _ := json.Marshal(PIDRecord{PID: os.Getpid(), Listen: ln.Addr().String()})
		if err := os.WriteFile(filepath.Join(dir, credentialFileName), []byte("cred\n"), 0o600); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, pidFileName), rec, 0o600)
	}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- ServeStdioBridge(t.Context(), dir, start, inR, outW); _ = outW.Close() }()

	r := bufio.NewReader(outR)
	line, err := r.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var h bridgeHello
	if err := json.Unmarshal(line, &h); err != nil || h.Bridge != 1 || h.Credential != "cred" || started != 1 {
		t.Fatalf("hello %s (%v), started %d", line, err, started)
	}
	if _, err := inW.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(r, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("carried %q, %v", buf, err)
	}
	_ = inW.Close()
	if err := <-done; err != nil && !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
}

func TestStdioBridgeReportsAStartFailure(t *testing.T) {
	outR, outW := io.Pipe()
	go func() {
		_ = ServeStdioBridge(t.Context(), t.TempDir(), func() error { return errors.New("no wadad") }, eofReader{}, outW)
		_ = outW.Close()
	}()
	line, _ := bufio.NewReader(outR).ReadBytes('\n')
	var h bridgeHello
	if err := json.Unmarshal(line, &h); err != nil || h.Error != "start wadad: no wadad" {
		t.Fatalf("hello %s (%v)", line, err)
	}
}

type eofReader struct{}

func (eofReader) Read([]byte) (int, error) { return 0, io.EOF }
