package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// SSHScheme marks an endpoint reached over SSH: ssh://[user@]host[:port].
const SSHScheme = "ssh://"

// DefaultSSHCommand is what DialSSH runs on the host when the host record
// names no command: wadad's stdio bridge, from a login shell so its PATH is
// the user's.
const DefaultSSHCommand = `sh -lc 'exec wadad --stdio-bridge'`

// bridgeHello is the line the stdio bridge writes before the daemon's bytes.
type bridgeHello struct {
	Bridge     int    `json:"wadaBridge"`
	Credential string `json:"credential"`
	Error      string `json:"error,omitempty"`
}

// DialSSH connects to the wadad of an SSH host. It runs command (default
// DefaultSSHCommand) there with the system's ssh, without a terminal and
// without prompting, so the host must take the user's keys or agent. The
// bridge it starts finds or starts the host's daemon, gives its local
// credential and then carries the WebSocket connection over ssh's stdio.
func DialSSH(ctx context.Context, endpoint, command string, opts Options) (*Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "ssh" || u.Host == "" {
		return nil, fmt.Errorf("client: %q is no ssh://[user@]host[:port] endpoint", endpoint)
	}
	args := []string{"-T", "-o", "BatchMode=yes", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3"}
	if p := u.Port(); p != "" {
		args = append(args, "-p", p)
	}
	target := u.Hostname()
	if u.User != nil {
		target = u.User.Username() + "@" + target
	}
	if command == "" {
		command = DefaultSSHCommand
	}
	args = append(args, target, command)
	ssh := os.Getenv("WADA_SSH")
	if ssh == "" {
		ssh = "ssh"
	}
	cmd := exec.Command(ssh, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &tailBuffer{max: 4096}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("client: start ssh: %w", err)
	}
	conn := &pipeConn{r: bufio.NewReader(stdout), w: stdin, cmd: cmd}
	fail := func(err error) (*Client, error) {
		_ = conn.Close()
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
		return nil, err
	}

	hello := make(chan error, 1)
	var h bridgeHello
	go func() {
		line, err := conn.r.ReadBytes('\n')
		if err == nil {
			err = json.Unmarshal(line, &h)
		}
		hello <- err
	}()
	select {
	case err := <-hello:
		if err != nil {
			return fail(fmt.Errorf("client: ssh %s: no wadad bridge answered", target))
		}
	case <-ctx.Done():
		return fail(ctx.Err())
	}
	if h.Error != "" {
		return fail(fmt.Errorf("client: %s: %s", target, h.Error))
	}
	if h.Bridge != 1 {
		return fail(fmt.Errorf("client: %s: unknown wadad bridge", target))
	}
	if opts.Credential == "" {
		opts.Credential = h.Credential
	}

	dialer := websocket.Dialer{
		NetDialContext:   func(context.Context, string, string) (net.Conn, error) { return conn, nil },
		HandshakeTimeout: 15 * time.Second,
	}
	ws, _, err := dialer.DialContext(ctx, "ws://127.0.0.1/ws", http.Header{})
	if err != nil {
		return fail(err)
	}
	return dialTransport(ctx, ws, opts)
}

// ServeStdioBridge is the host's end of DialSSH: it finds the wadad of
// dataDir, starting it with start when none answers, writes the bridge's
// hello with the daemon's local credential, and then carries bytes between
// the daemon and in and out until either side ends.
func ServeStdioBridge(ctx context.Context, dataDir string, start func() error, in io.Reader, out io.Writer) error {
	if dataDir == "" {
		var err error
		if dataDir, err = DefaultDataDir(); err != nil {
			return err
		}
	}
	say := func(h bridgeHello) error {
		h.Bridge = 1
		b, _ := json.Marshal(h)
		_, err := out.Write(append(b, '\n'))
		return err
	}
	conn, cred, err := dialLocalTCP(dataDir)
	if err != nil {
		if err := os.MkdirAll(dataDir, 0o700); err != nil {
			_ = say(bridgeHello{Error: err.Error()})
			return err
		}
		if err := start(); err != nil {
			_ = say(bridgeHello{Error: "start wadad: " + err.Error()})
			return err
		}
		deadline := time.Now().Add(30 * time.Second)
		for {
			if conn, cred, err = dialLocalTCP(dataDir); err == nil {
				break
			}
			if time.Now().After(deadline) {
				_ = say(bridgeHello{Error: "wadad did not start: " + err.Error()})
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	defer conn.Close()
	if err := say(bridgeHello{Credential: cred}); err != nil {
		return err
	}
	done := make(chan error, 2)
	go func() {
		_, err := io.Copy(conn, in)
		if tc, ok := conn.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
		done <- err
	}()
	go func() {
		_, err := io.Copy(out, conn)
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// dialLocalTCP opens a TCP connection to the running wadad of dataDir.
func dialLocalTCP(dataDir string) (net.Conn, string, error) {
	rec, cred, err := ReadLocalEndpoint(dataDir)
	if err != nil {
		return nil, "", err
	}
	conn, err := net.DialTimeout("tcp", rec.Listen, 2*time.Second)
	if err != nil {
		return nil, "", err
	}
	return conn, cred, nil
}

// StartDetached starts exe with args as a daemon that outlives this process,
// logging to <dataDir>/wadad.log.
func StartDetached(dataDir, exe string, args ...string) error {
	logFile, err := os.OpenFile(filepath.Join(dataDir, "wadad.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	cmd := exec.Command(exe, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = detachedProcAttr()
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// pipeConn is ssh's stdio as a net.Conn. Deadlines are not kept: the
// client's pings notice a connection that stopped answering.
type pipeConn struct {
	r    *bufio.Reader
	w    io.WriteCloser
	cmd  *exec.Cmd
	once sync.Once
}

func (c *pipeConn) Read(b []byte) (int, error)  { return c.r.Read(b) }
func (c *pipeConn) Write(b []byte) (int, error) { return c.w.Write(b) }
func (c *pipeConn) Close() error {
	c.once.Do(func() {
		_ = c.w.Close()
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		_ = c.cmd.Wait()
	})
	return nil
}
func (*pipeConn) LocalAddr() net.Addr              { return pipeAddr{} }
func (*pipeConn) RemoteAddr() net.Addr             { return pipeAddr{} }
func (*pipeConn) SetDeadline(time.Time) error      { return nil }
func (*pipeConn) SetReadDeadline(time.Time) error  { return nil }
func (*pipeConn) SetWriteDeadline(time.Time) error { return nil }

type pipeAddr struct{}

func (pipeAddr) Network() string { return "ssh" }
func (pipeAddr) String() string  { return "ssh" }

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = t.b[len(t.b)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.b)
}

// IsSSHEndpoint reports that an endpoint is reached over SSH.
func IsSSHEndpoint(endpoint string) bool { return strings.HasPrefix(endpoint, SSHScheme) }

var errNoSSHCommand = errors.New("client: an ssh command is set only for an ssh:// endpoint")
