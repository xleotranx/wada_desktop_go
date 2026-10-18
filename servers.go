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
	"github.com/egoist/mygo/transfer"
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
	password string // the password the app connects with
	// A paired connection's password is the token a pairing link handed
	// back, which lapses on its own after pairedLife.
	paired   bool
	pairedAt int64 // when that token was issued, in unix milliseconds
}

// pairedLife is how long a pairing token signs in for: the month opencode
// hands a paired client before a fresh link is needed.
const pairedLife = 30 * 24 * time.Hour

// pairedDaysLeft is how much of a pairing token's month is left, floored,
// as the settings row says it.
func pairedDaysLeft(pairedAt int64) int {
	if pairedAt == 0 {
		return 0
	}
	left := time.Until(time.UnixMilli(pairedAt).Add(pairedLife))
	if left <= 0 {
		return 0
	}
	return int(left / (24 * time.Hour))
}

// defaultLocalServer is the connection the app starts with: its own
// opencode serve, on a password it generates for each run.
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

// serverPasswordLine words what the row says about the password.
func serverPasswordLine(s serverConn) string {
	switch {
	case s.paired:
		if days := pairedDaysLeft(s.pairedAt); days > 0 {
			return fmt.Sprintf("Paired · signs in for %d more days", days)
		}
		return "Paired · the token has lapsed; pair again from Edit"
	case s.password != "":
		return "Password set"
	case s.managed:
		return "Password generated for each start"
	default:
		return "No password set"
	}
}

// refusedLine words a 401 for one connection: a pairing token lapses on
// its own after its month, while a password was simply refused.
func refusedLine(paired bool) string {
	if paired {
		return "the pairing has lapsed; pair again from Edit"
	}
	return "the password was refused (401)"
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

// pairLink splits a pairing link — the address `opencode pair` prints,
// with /auth/connect/<code> on it — into the server's address and the
// one-shot code. The address comes back the way the editor stores one:
// the scheme filled in, any path prefix kept, no /api on it. What is not
// a pairing link reads false, an ordinary address among them.
func pairLink(raw string) (addr, code string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", "", false
	}
	path := strings.TrimSuffix(u.Path, "/")
	at := strings.LastIndex(path, "/auth/connect/")
	if at < 0 {
		return "", "", false
	}
	prefix, code := path[:at], path[at+len("/auth/connect/"):]
	if code == "" || strings.Contains(code, "/") {
		return "", "", false
	}
	return u.Scheme + "://" + u.Host + prefix, code, true
}

// redeemPairLink trades one pairing link for the token that stands in for
// the server's password, the way opencode's own clients join a server: the
// link works once and expires in five minutes, the token signs in for a
// month. The link's code is the credential, so the request carries none.
func redeemPairLink(ctx context.Context, link string) (string, string, error) {
	addr, code, ok := pairLink(link)
	if !ok {
		return "", "", fmt.Errorf("that is not a pairing link")
	}
	base, err := apiBase(addr)
	if err != nil {
		return "", "", err
	}
	req, err := http.NewRequestWithContext(ctx, "GET",
		strings.TrimSuffix(base, "/api")+"/auth/connect/"+url.PathEscape(code), nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient().Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		var tagged struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &tagged) == nil && tagged.Message != "" {
			return "", "", fmt.Errorf("%s", tagged.Message)
		}
		return "", "", fmt.Errorf("%s: %s", addr, resp.Status)
	}
	var out struct {
		Token string `json:"token"`
		Data  struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return "", "", fmt.Errorf("%s did not read as a pairing answer", addr)
	}
	if out.Token == "" {
		out.Token = out.Data.Token
	}
	if out.Token == "" {
		return "", "", fmt.Errorf("%s handed back no token", addr)
	}
	return addr, out.Token, nil
}

// runPair redeems one pairing link in the background, its answer landing
// through the window the way the probes do. Tests stand in for it, as they
// do for pickFolder.
func (a *app) runPair(link string, done func(addr, token string, err error)) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		addr, token, err := redeemPairLink(ctx, link)
		land := func() { done(addr, token, err) }
		if a.win == nil { // headless, as tests run the view
			mygo.RunOnMain(land)
			return
		}
		a.win.Update(land)
	}()
}

// pairLinkLife is how long a minted pairing link works for: opencode's
// links are single use and expire in five minutes.
const pairLinkLife = 5 * time.Minute

// mintedLink is one pairing link the app asked a server for, with the
// moment it was minted: the settings row shows it while it still works.
type mintedLink struct {
	link string
	at   time.Time
}

// mintPairLink asks one server for a fresh pairing link, the way
// `opencode pair` does: a short-lived code for another client to join
// with, on an address the server names for itself, else the one the app
// dials it at.
func mintPairLink(ctx context.Context, cfg serverConn, facts serverFacts) (string, error) {
	ag, err := standaloneAgent(cfg)
	if err != nil {
		return "", err
	}
	raw, err := ag.call(ctx, "POST", "/pair", struct{}{})
	if err != nil {
		return "", err
	}
	var out struct {
		Code string `json:"code"`
		Data struct {
			Code string `json:"code"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return "", fmt.Errorf("the pairing answer did not read")
	}
	code := out.Code
	if code == "" {
		code = out.Data.Code
	}
	if code == "" {
		return "", fmt.Errorf("the server minted no pairing code")
	}
	addr := strings.TrimSuffix(cfg.url, "/")
	if len(facts.urls) > 0 {
		addr = strings.TrimSuffix(facts.urls[0], "/")
	}
	if addr == "" {
		addr = ag.addr
	}
	return addr + "/auth/connect/" + url.PathEscape(code), nil
}

// copyPairLink mints a fresh pairing link on one server and puts it on the
// clipboard — the server's own way of letting another client in, without a
// trip to its machine. The link has to go out on a goroutine and the
// clipboard back on the UI thread, so the answer lands through the window
// the way the probes do; tests run it inline, as they do setAsk.
func (a *app) copyPairLink(s serverConn) {
	facts := a.serverFacts[s.id]
	mint := func() (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return mintPairLink(ctx, s, facts)
	}
	land := func(link string, err error) {
		if err != nil {
			a.streamLine("The pairing link did not mint: " + err.Error())
			return
		}
		if cerr := a.copy(link); cerr != nil {
			a.streamLine("The pairing link could not reach the clipboard: " + cerr.Error())
		}
		if a.pairLinks == nil {
			a.pairLinks = map[string]mintedLink{}
		}
		a.pairLinks[s.id] = mintedLink{link: link, at: time.Now()}
	}
	if a.win == nil { // headless, as tests run the view
		link, err := mint()
		land(link, err)
		return
	}
	go func() {
		link, err := mint()
		a.win.Update(func() { land(link, err) })
	}()
}

// copyToClipboard puts one string on the system clipboard.
func (a *app) copyToClipboard(text string) error {
	return mygo.Clipboard.Write(transfer.TextData(text))
}

// activePairLink is one server's minted pairing link while it still works;
// a link the clock has passed is nothing to show.
func (a *app) activePairLink(serverID string) string {
	m, ok := a.pairLinks[serverID]
	if !ok || time.Since(m.at) > pairLinkLife {
		return ""
	}
	return m.link
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

// serverFacts is what a server's /info says about itself: the version it
// runs, the addresses it says it answers on, its process and what its
// capabilities allow.
type serverFacts struct {
	version string
	urls    []string
	pid     int
	pty     bool // it offers persistent terminals
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
func readInfo(raw []byte) serverFacts {
	var out struct {
		infoBody
		Data infoBody `json:"data"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return serverFacts{}
	}
	body := out.infoBody
	if body.Version == "" {
		body = out.Data
	}
	return serverFacts{version: body.Version, urls: body.URLs, pid: body.PID,
		pty: body.Capabilities.PersistentPty}
}

// serverFactsLine words what the server's own /info added to the row: the
// addresses it says it answers on that the app does not already name.
// Capabilities the app has no use for yet stay out of the reading.
func serverFactsLine(f serverFacts, s serverConn, h serverHealth) string {
	urls := []string{}
	for _, u := range f.urls {
		u = strings.TrimSuffix(u, "/")
		if u == "" || u == strings.TrimSuffix(h.addr, "/") || u == strings.TrimSuffix(s.url, "/") {
			continue
		}
		urls = append(urls, u)
	}
	if len(urls) == 0 {
		return ""
	}
	return "Reachable at " + strings.Join(urls, ", ")
}

// probeServer asks a server for its facts without taking it over: what
// Settings tests a connection with while the pipe talks to another one.
func probeServer(ctx context.Context, cfg serverConn) (serverFacts, error) {
	base, err := apiBase(cfg.url)
	if err != nil {
		return serverFacts{}, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", base+"/info", nil)
	if err != nil {
		return serverFacts{}, err
	}
	req.SetBasicAuth("opencode", cfg.password)
	resp, err := httpClient().Do(req)
	if err != nil {
		return serverFacts{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		return serverFacts{}, fmt.Errorf("%s", refusedLine(cfg.paired))
	}
	if resp.StatusCode >= 300 {
		return serverFacts{}, fmt.Errorf("%s: %s", base, resp.Status)
	}
	if facts := readInfo(raw); facts.version != "" {
		return facts, nil
	}
	return serverFacts{}, fmt.Errorf("%s did not name its version", base)
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

// localActive says the server on show is the one the app runs itself,
// which picks its projects out of the machine's own folders. A remote
// server lists its projects the way its project report says them.
func (a *app) localActive() bool {
	return a.activeServerConn().local
}

// setHealth records how one server last answered.
func (a *app) setHealth(id string, h serverHealth) {
	if a.serverHealth == nil {
		a.serverHealth = map[string]serverHealth{}
	}
	a.serverHealth[id] = h
}

// setFacts records what one server's /info said about itself.
func (a *app) setFacts(id string, f serverFacts) {
	if a.serverFacts == nil {
		a.serverFacts = map[string]serverFacts{}
	}
	a.serverFacts[id] = f
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
	// The draft's folder and its model belong to the server's own world:
	// carried over, they would speak for a folder the next server has
	// never heard of, or a model it does not offer.
	a.draftProject = ""
	a.draftModel = agentModelRef{}
	a.reconnect()
	a.saveState()
}

// reconnect dials the active server again, as its row's action and a
// switch both do.
func (a *app) reconnect() {
	a.clearForSwitch()
	select {
	case a.jobs <- agentJob{kind: jobServer, server: a.activeServerConn(), pins: slices.Clone(a.pinOrder)}:
	default:
	}
}

// clearForSwitch drops what renders over whatever server opens next: the
// chosen chat, its transcript, the picker and the search, plus a mid-load
// flag of the server coming up. Each server's conversations and catalog
// stay in its scope, so a switch back to one shows its chats while the
// pipe dials, and its dial refreshes the list.
func (a *app) clearForSwitch() {
	a.selected = ""
	a.selectedSession = ""
	a.freshTranscript()
	a.pickerOpen = false
	a.scope().moreLoading = false
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
	delete(a.scopes, id)
	delete(a.serverHealth, id)
	delete(a.serverFacts, id)
	delete(a.pairLinks, id)
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
		facts, err := probeServer(ctx, cfg)
		if err != nil {
			h.status, h.detail = healthDown, err.Error()
		} else {
			h.status, h.detail = healthReady, facts.version
		}
		land := func() {
			a.setHealth(cfg.id, h)
			a.setFacts(cfg.id, facts)
		}
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
