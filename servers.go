package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

// localServerID is the built-in server's identity: the row the app always
// keeps, and the key under which the pins of its chats were already
// stored before servers could be added.
const localServerID = "local-opencode"

// serverConn is one opencode server the app can talk to: the local one it
// runs itself by default, and any it was pointed at.
type serverConn struct {
	id       string
	name     string
	local    bool   // the built-in row, kept whatever happens
	managed  bool   // it runs with the app, on a port the app picks
	url      string // where it listens; empty while the app runs it
	password string // the key the app connects with
}

// defaultLocalServer is the connection the app starts with: its own
// opencode serve, on a key it generates for each run.
func defaultLocalServer() serverConn {
	return serverConn{id: localServerID, name: "Local server", local: true, managed: true}
}

// serverHealth is how one server last answered, for Settings to show.
type serverHealth struct {
	status string // one of the health* constants; "" until something tried
	detail string // the version it reported, or why it did not
	addr   string // where it last listened
}

const (
	healthUntested   = ""           // nothing has reached for it yet
	healthConnecting = "connecting" // a spawn or a dial is on its way
	healthReady      = "ready"      // it answered
	healthDown       = "down"       // it did not
	healthStopped    = "stopped"    // the app stops its own server when it is idle
)

// serverKey makes a fresh secret for the app's own server, 43 URL-safe
// characters, as opencode serve takes.
func serverKey() string {
	key := make([]byte, 32)
	rand.Read(key)
	return base64.RawURLEncoding.EncodeToString(key)
}

// newServerID names a connection the user adds; the id outlives renames
// and edits, so its pins and model choices follow it.
func newServerID() string { return "srv-" + serverKey()[:10] }

// modelKey names one model of one server in the settings' model choices.
func modelKey(serverID, provider, id string) string {
	return serverID + "\x00" + provider + "/" + id
}

// serverLine words what one row of Settings says about a connection.
func serverLine(s serverConn, h serverHealth) string {
	status := ""
	switch h.status {
	case healthReady:
		status = "Connected"
		if h.detail != "" {
			status += " · " + h.detail
		}
	case healthConnecting:
		status = "Connecting…"
		if s.managed {
			status = "Starting…"
		}
	case healthStopped:
		status = "Stopped"
	case healthDown:
		status = "Not connected"
		if h.detail != "" {
			status += " — " + h.detail
		}
	default:
		status = "Not tested yet"
	}
	if h.addr != "" {
		return h.addr + "  ·  " + status
	}
	if s.url != "" {
		return s.url + "  ·  " + status
	}
	return status
}

// serverKeyLine words what the row says about the key.
func serverKeyLine(s serverConn) string {
	switch {
	case s.password != "":
		return "Key set"
	case s.managed:
		return "Key generated for each start"
	default:
		return "No key set"
	}
}

// apiBase is the /api endpoint of a server address the user typed: a
// missing scheme reads as http, and a trailing slash or /api falls away.
func apiBase(raw string) (string, error) {
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
func httpClient() *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 8 * time.Second}).DialContext,
		ResponseHeaderTimeout: 20 * time.Second,
	}}
}

// infoVersion reads the version out of a /info body, bare or wrapped.
func infoVersion(raw []byte) string {
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

// probeServer asks a server for its version without taking it over: what
// Settings tests a connection with while the pipe talks to another one.
func probeServer(ctx context.Context, cfg serverConn) (string, error) {
	base, err := apiBase(cfg.url)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", base+"/info", nil)
	if err != nil {
		return "", err
	}
	req.SetBasicAuth("opencode", cfg.password)
	resp, err := httpClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("the key was refused")
	}
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("%s: %s", base, resp.Status)
	}
	if v := infoVersion(raw); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("%s did not name its version", base)
}

// serverByID finds one connection as it stands now.
func (a *app) serverByID(id string) (serverConn, bool) {
	for _, s := range a.servers {
		if s.id == id {
			return s, true
		}
	}
	return serverConn{}, false
}

// activeServerConn is the connection the pipe should be talking to; the
// local one stands in when nothing else was chosen.
func (a *app) activeServerConn() serverConn {
	if s, ok := a.serverByID(a.activeServer); ok {
		return s
	}
	return defaultLocalServer()
}

// setHealth records how one server last answered.
func (a *app) setHealth(id string, h serverHealth) {
	if a.serverHealth == nil {
		a.serverHealth = map[string]serverHealth{}
	}
	a.serverHealth[id] = h
}

// useServer points the app at one connection: the pipe redials, the view
// clears for the new server's chats, and its own pins come back.
func (a *app) useServer(id string) {
	if id == a.activeServer {
		return
	}
	if _, ok := a.serverByID(id); !ok {
		return
	}
	old := a.activeServer
	a.activeServer = id
	a.pinsByServer[old] = slices.Clone(a.pinOrder)
	a.pinOrder = slices.Clone(a.pinsByServer[id])
	a.pinned = map[string]bool{}
	for _, sid := range a.pinOrder {
		a.pinned[sid] = true
	}
	if last, ok := a.serverByID(old); ok && last.managed {
		// The app runs its own server only while it is the active one.
		a.setHealth(old, serverHealth{status: healthStopped})
	}
	a.reconnect()
	a.saveState()
}

// reconnect dials the active server again, as its row's action and a
// switch both do.
func (a *app) reconnect() {
	a.clearForServer()
	select {
	case a.jobs <- agentJob{kind: jobServer, server: a.activeServerConn(), pins: slices.Clone(a.pinOrder)}:
	default:
	}
}

// clearForServer empties what belonged to the server the app is leaving:
// its chats, the transcript, the catalog and whatever streamed.
func (a *app) clearForServer() {
	a.sessions = nil
	a.sessionsLoaded = false
	a.nextCursor = ""
	a.moreLoading = false
	a.selected = ""
	a.selectedSession = ""
	a.freshTranscript()
	a.models = nil
	a.modelsLoaded = false
	a.pickerOpen = false
	a.searchResults = nil
	a.searchLoading = false
	a.searchVersion++
}

// dropServer forgets one connection and everything the app kept for it.
// The local server and the active one are never dropped.
func (a *app) dropServer(id string) {
	if id == localServerID || id == a.activeServer {
		return
	}
	kept := a.servers[:0]
	for _, s := range a.servers {
		if s.id != id {
			kept = append(kept, s)
		}
	}
	a.servers = kept
	delete(a.pinsByServer, id)
	delete(a.serverHealth, id)
	prefix := id + "\x00"
	for key := range a.modelOff {
		if strings.HasPrefix(key, prefix) {
			delete(a.modelOff, key)
		}
	}
	a.saveState()
}

// probe tests one connection in the background, its answer landing
// through the window the way the agent's notes do. Tests stand in for it,
// as they do for pickFolder. A server the app runs itself is never
// probed: its run is the pipe's story to tell.
func (a *app) runProbe(cfg serverConn) {
	if cfg.managed {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		h := serverHealth{addr: strings.TrimSuffix(cfg.url, "/")}
		version, err := probeServer(ctx, cfg)
		if err != nil {
			h.status, h.detail = healthDown, err.Error()
		} else {
			h.status, h.detail = healthReady, version
		}
		land := func() { a.setHealth(cfg.id, h) }
		if a.win == nil { // headless, as tests run the view
			mygo.RunOnMain(land)
			return
		}
		a.win.Update(land)
	}()
}

// probeUntried tests the connections nothing has reached for yet, so the
// page tells the user something the moment they open it.
func (a *app) probeUntried() {
	for _, s := range a.servers {
		if s.id == a.activeServer || s.managed {
			continue
		}
		if a.serverHealth[s.id].status == healthUntested {
			a.probe(s)
		}
	}
}
