package opencode

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"net"
	"net/http"
	"net/url"

	"strings"
	"time"
)

// serverKey makes a fresh secret for the app's own server, 43 URL-safe
// characters, as opencode serve takes.
func Secret() string {
	key := make([]byte, 32)
	rand.Read(key)
	return base64.RawURLEncoding.EncodeToString(key)
}

// refusedLine words a 401 for one connection: a pairing token lapses on
// its own after its month, while a password was simply refused.
func RefusedLine(paired bool) string {
	if paired {
		return "the pairing has lapsed; pair again from Edit"
	}
	return "the password was refused (401)"
}

// apiBase is the /api endpoint of a server address the user typed: a
// missing scheme reads as http, and a trailing slash or /api falls away.
func APIBase(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("give the server an address")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("%s is not an address to reach", raw)
	}
	path := strings.TrimSuffix(u.Path, "/")
	path = strings.TrimSuffix(path, "/api")
	return u.Scheme + "://" + u.Host + path + "/api", nil
}

// httpClient is what agents dial with: a host that blackholes must not
// hang a call forever, while the event stream, whose headers arrive at
// once, stays open.
func HTTPClient() *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 8 * time.Second}).DialContext,
		ResponseHeaderTimeout: 20 * time.Second,
	}}
}

// serverFacts is what a server's /info says about itself: the version it
// runs, the addresses it says it answers on, its process and what its
// capabilities allow.
type Facts struct {
	Version string
	URLs    []string
	PID     int
	PTY     bool // it offers persistent terminals
}

// infoBody is one /info answer's fields, which opencode wraps in a data
// envelope on some builds and not on others.
type infoBody struct {
	Version      string   `json:"version"`
	PID          int      `json:"pid"`
	URLs         []string `json:"urls"`
	Capabilities struct {
		PersistentPty bool `json:"persistentPty"`
	} `json:"capabilities"`
}

// readInfo reads a /info body, bare or wrapped, into what the app keeps of
// it. A body it cannot read reads as the zero facts.
func ReadInfo(raw []byte) Facts {
	var out struct {
		infoBody
		Data infoBody `json:"data"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return Facts{}
	}
	body := out.infoBody
	if body.Version == "" {
		body = out.Data
	}
	return Facts{Version: body.Version, URLs: body.URLs, PID: body.PID,
		PTY: body.Capabilities.PersistentPty}
}

// Config is an immutable snapshot of the connection credentials and lifetime.
type Config struct {
	URL, Password   string
	Paired, Managed bool
}
