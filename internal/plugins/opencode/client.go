package opencode

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// client speaks one OpenCode server's /api HTTP surface. It is safe for
// concurrent use.
type client struct {
	base     string // like http://127.0.0.1:4096/api
	password string
	http     *http.Client
	cmd      *exec.Cmd // the managed process; nil for a remote server
	version  string
	stopOnce sync.Once
}

// bodyCap bounds one response: a stored conversation with images is
// megabytes, but not endless.
const bodyCap int64 = 32 << 20

func httpClient() *http.Client {
	// A blackholed host must not hang a call; the event stream, whose headers
	// arrive at once, stays open.
	return &http.Client{Transport: &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 8 * time.Second}).DialContext,
		ResponseHeaderTimeout: 20 * time.Second,
	}}
}

// apiBase turns a typed address into its /api endpoint: a missing scheme
// reads as http, and a trailing slash or /api falls away.
func apiBase(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("the server has no address")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("%s is not a server address", raw)
	}
	p := strings.TrimSuffix(strings.TrimSuffix(u.Path, "/"), "/api")
	return u.Scheme + "://" + u.Host + p + "/api", nil
}

// endpoint is the address shown to the user.
func (c *client) endpoint() string { return strings.TrimSuffix(c.base, "/api") }

func secret() string {
	key := make([]byte, 32)
	rand.Read(key)
	return base64.RawURLEncoding.EncodeToString(key)
}

// dial connects to a server that runs on its own.
func dial(ctx context.Context, conn Connection) (*client, error) {
	base, err := apiBase(conn.URL)
	if err != nil {
		return nil, err
	}
	c := &client{base: base, password: conn.Password, http: httpClient()}
	if err := c.waitReady(ctx, 8*time.Second); err != nil {
		c.stop()
		return nil, err
	}
	return c, nil
}

// spawn starts `opencode serve` on a free local port with a fresh password,
// adding env to the app's environment. The process ends with ctx or stop.
func spawn(ctx context.Context, exe, dir string, env []string) (*client, error) {
	path, err := exec.LookPath(exe)
	if err != nil {
		return nil, fmt.Errorf("opencode was not found: %w", err)
	}
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	pw := secret()
	cmd := exec.CommandContext(ctx, path, "serve", "--hostname", "127.0.0.1", "--port", strconv.Itoa(port))
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), env...), "OPENCODE_SERVER_PASSWORD="+pw)
	if err := startOwned(cmd); err != nil {
		return nil, err
	}
	c := &client{base: "http://127.0.0.1:" + strconv.Itoa(port) + "/api", password: pw, http: httpClient(), cmd: cmd}
	if err := c.waitReady(ctx, 20*time.Second); err != nil {
		c.stop()
		return nil, err
	}
	return c, nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// stop ends the managed process, if any; a remote server keeps running.
func (c *client) stop() {
	c.stopOnce.Do(func() {
		if c.cmd != nil && c.cmd.Process != nil {
			c.cmd.Process.Kill()
			c.cmd.Wait()
		}
		c.http.CloseIdleConnections()
	})
}

// waitReady polls /info until the server names its version.
func (c *client) waitReady(ctx context.Context, within time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, within)
	defer cancel()
	var last error
	for {
		raw, err := c.call(ctx, "GET", "/info", nil)
		if err == nil {
			if v := readVersion(raw); v != "" {
				c.version = v
				return nil
			}
			err = errors.New("the server did not name its version")
		}
		if errors.Is(err, errRefused) {
			return err // waiting won't change the answer
		}
		last = err
		select {
		case <-ctx.Done():
			return fmt.Errorf("the server did not come up in %s: %w", within, last)
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// readVersion reads /info, which some builds wrap in a data envelope.
func readVersion(raw []byte) string {
	var out struct {
		Version string `json:"version"`
		Data    struct {
			Version string `json:"version"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return ""
	}
	if out.Version != "" {
		return out.Version
	}
	return out.Data.Version
}

var errRefused = errors.New("the server refused the password (401)")

// call is one API round trip. OpenCode answers errors as tagged JSON bodies
// like {"_tag":…,"message":…}, which become Go errors.
func (c *client) call(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	raw, _, err := c.do(ctx, method, path, body)
	return raw, err
}

// do is call, with the response's headers.
func (c *client) do(ctx context.Context, method, path string, body any) (json.RawMessage, http.Header, error) {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return nil, nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, &buf)
	if err != nil {
		return nil, nil, err
	}
	req.SetBasicAuth("opencode", c.password)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, bodyCap+1))
	if err != nil {
		return nil, nil, err
	}
	if int64(len(raw)) > bodyCap {
		return nil, nil, fmt.Errorf("%s %s: the response is larger than %d MiB", method, path, bodyCap>>20)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, nil, errRefused
	}
	if resp.StatusCode >= 300 {
		var tagged struct {
			Tag     string `json:"_tag"`
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &tagged) == nil && tagged.Message != "" {
			return nil, nil, fmt.Errorf("%s: %s", tagged.Tag, tagged.Message)
		}
		return nil, nil, fmt.Errorf("%s %s: %s", method, path, resp.Status)
	}
	return raw, resp.Header, nil
}

// decode runs one call and decodes its body into out.
func (c *client) decode(ctx context.Context, method, path string, body, out any) error {
	raw, err := c.call(ctx, method, path, body)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s %s: the answer did not decode", method, path)
	}
	return nil
}

// wireEvent is one line of the event stream, decoded to its envelope.
type wireEvent struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// events follows the SSE feed. opened runs once the stream is established;
// h runs per event until the stream or ctx ends.
func (c *client) events(ctx context.Context, opened func(), h func(wireEvent)) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.base+"/event", nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth("opencode", c.password)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return errRefused
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("event stream: %s", resp.Status)
	}
	opened()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 8<<20) // tool output makes lines chunky
	for sc.Scan() {
		line, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok { // comments such as ": heartbeat"
			continue
		}
		var ev wireEvent
		if json.Unmarshal([]byte(line), &ev) == nil {
			h(ev)
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return errors.New("the event stream ended")
}
