package desktop

import (
	"context"
	"crypto/rand"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"wada-desktop-go/internal/agent"
	"wada-desktop-go/internal/runner"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
)

// localServerID is the built-in managed connection, scoped by backend storage.
const localServerID = "local"

// serverConn is one backend connection the app can talk to: the local one it
// runs itself by default, and any it was pointed at.
type serverConn struct {
	id       string
	name     string
	local    bool   // the built-in row, kept whatever happens
	managed  bool   // it runs with the app, on a port the app picks
	url      string // where it listens; empty while the app runs it
	password string // the password the app connects with
	// A paired connection's password is the token a pairing link handed
	// back, which lapses on its own after its backend-provided lifetime.
	paired   bool
	pairedAt int64 // when that token was issued, in unix milliseconds
}

// credentialDaysLeft counts complete days remaining in a backend's token lease.
func credentialDaysLeft(created int64, lifetime time.Duration) int {
	if created == 0 || lifetime <= 0 {
		return 0
	}
	left := time.Until(time.UnixMilli(created).Add(lifetime))
	if left <= 0 {
		return 0
	}
	return int(left / (24 * time.Hour))
}

// defaultLocalServer is the connection the app starts with: its own
// the local backend, on a password it generates for each run.
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

// newServerID names a connection the user adds; the id outlives renames
// and edits, so its pins and model choices follow it.
func newServerID() string { return "srv-" + rand.Text()[:10] }

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
func connectionPasswordLine(s serverConn, lifetime time.Duration) string {
	switch {
	case s.paired:
		if days := credentialDaysLeft(s.pairedAt, lifetime); days > 0 {
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

// runPair redeems one pairing link in the background, its answer landing
// through the window the way the probes do. Tests stand in for it, as they
// do for pickFolder.
func (a *app) runPair(link string, done func(addr, token string, err error)) {
	type pairingResult struct{ addr, token string }
	runOwned(a, a.registryRequest("pair:editor"), a.runtime.lifetime, func(ctx context.Context) (pairingResult, error) {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		pairing, ok := a.backend.(agent.Pairing)
		if !ok {
			return pairingResult{}, fmt.Errorf("this backend does not support pairing")
		}
		addr, token, err := pairing.RedeemPairLink(ctx, link)
		return pairingResult{addr, token}, err
	}, func(p pairingResult, err error) { done(p.addr, p.token, err) })
}

// mintedLink is one pairing link the app asked a server for, with the
// moment it was minted: the settings row shows it while it still works.
type mintedLink struct {
	link string
	at   time.Time
}

// copyPairLink mints a fresh pairing link on one server and puts it on the
// clipboard — the server's own way of letting another client in, without a
// trip to its machine. The link has to go out on a goroutine and the
// clipboard back on the UI thread, so the answer lands through the window
// the way the probes do; tests run it inline, as they do setAsk.
func (a *app) copyPairLink(s serverConn) {
	facts := a.connections.serverFacts[s.id]
	runOwned(a, a.registryRequest("mint:"+s.id), a.runtime.lifetime, func(ctx context.Context) (string, error) {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		pairing, ok := a.backend.(agent.Pairing)
		if !ok {
			return "", fmt.Errorf("this backend does not support pairing")
		}
		return pairing.MintPairLink(ctx, s.transport(), facts)
	}, func(link string, err error) {
		if current, ok := a.serverByID(s.id); !ok || current != s {
			return
		}
		if err != nil {
			a.streamLine("The pairing link did not mint: " + err.Error())
			return
		}
		if cerr := a.runtime.copy(link); cerr != nil {
			a.streamLine("The pairing link could not reach the clipboard: " + cerr.Error())
		} else {
			// The row already shows the link; the toast only confirms the
			// copy, so it names no "Pairing link" the settings then own.
			a.notify(ui.Toast{Title: "Copied to the clipboard", Type: components.ToastSuccess})
		}
		if a.connections.pairLinks == nil {
			a.connections.pairLinks = map[string]mintedLink{}
		}
		a.connections.pairLinks[s.id] = mintedLink{link: link, at: time.Now()}
	})
}

// copyToClipboard puts one string on the system clipboard.
func (a *app) copyToClipboard(text string) error {
	return mygo.Clipboard.Write(transfer.TextData(text))
}

// activePairLink is one server's minted pairing link while it still works;
// a link the clock has passed is nothing to show.
func (a *app) activePairLink(serverID string) string {
	m, ok := a.connections.pairLinks[serverID]
	pairing, supported := a.backend.(agent.Pairing)
	if !supported || !ok || time.Since(m.at) > pairing.PairingPolicy().LinkLifetime {
		return ""
	}
	return m.link
}

// serverFactsLine words what the server's own /info added to the row: the
// addresses it says it answers on that the app does not already name.
// Capabilities the app has no use for yet stay out of the reading.
func serverFactsLine(f agent.Facts, s serverConn, h serverHealth) string {
	urls := []string{}
	for _, u := range f.URLs {
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

// serverByID finds one connection as it stands now.
func (a *app) serverByID(id string) (serverConn, bool) {
	for _, s := range a.connections.servers {
		if s.id == id {
			return s, true
		}
	}
	return serverConn{}, false
}

// activeServerConn is the connection the pipe should be talking to; the
// local one stands in when nothing else was chosen. The app's own server
// keeps no address in its row — the saved settings must not carry an
// ephemeral port — so a dial takes the spawned one from the last status,
// and the password named once for this run before the server came up.
func (a *app) activeServerConn() serverConn {
	s := defaultLocalServer()
	if found, ok := a.serverByID(a.connections.activeServer); ok {
		s = found
	}
	if s.managed {
		if s.url == "" {
			s.url = a.connections.serverHealth[s.id].addr
		}
		if s.password == "" {
			s.password = a.runtime.localPassword
		}
	}
	return s
}

// localActive says the server on show is the one the app runs itself,
// which picks its projects out of the machine's own folders. A remote
// server lists its projects the way its project report says them.
func (a *app) localActive() bool {
	return a.activeServerConn().local
}

// setHealth records how one server last answered.
func (a *app) setHealth(id string, h serverHealth) {
	if a.connections.serverHealth == nil {
		a.connections.serverHealth = map[string]serverHealth{}
	}
	a.connections.serverHealth[id] = h
}

// setFacts records what one server's /info said about itself.
func (a *app) setFacts(id string, f agent.Facts) {
	if a.connections.serverFacts == nil {
		a.connections.serverFacts = map[string]agent.Facts{}
	}
	a.connections.serverFacts[id] = f
}

// useServer points the app at one connection: the pipe redials, the view
// clears for the new server's chats, and its own pins come back.
func (a *app) useServer(id string) {
	if id == a.connections.activeServer {
		return
	}
	if _, ok := a.serverByID(id); !ok {
		return
	}
	old := a.connections.activeServer
	// The unsent words belong to the server that was on show; the next
	// server's composer starts from its own draft, not this one's.
	a.stashDraft()
	a.conversation.draft = ""
	a.connections.activeServer = id
	// A banner names a chat of the server that raised it: one left over
	// must not open that ID in the server now on show.
	if a.runtime.clearNotices != nil {
		a.runtime.clearNotices()
	}
	if last, ok := a.serverByID(old); ok && last.managed {
		// The app runs its own server only while it is the active one.
		a.setHealth(old, serverHealth{status: healthStopped})
	}
	// The draft's folder and its model belong to the server's own world:
	// carried over, they would speak for a folder the next server has
	// never heard of, or a model it does not offer.
	a.reconnect()
	a.saveState()
}

// reconnect dials the active server again, as its row's action and a
// switch both do.
func (a *app) reconnect() {
	a.runtime.stopWork()
	a.runtime.workCtx, a.runtime.stopWork = context.WithCancel(a.runtime.lifetime)
	a.connections.route++
	a.clearForSwitch()
	if a.runtime.connectTo != nil {
		a.runtime.connectTo(runner.Generation(a.connections.route), a.runnerConnection())
	}
	if a.search.searchText != "" {
		a.reviseSearch()
	}
}

// clearForSwitch drops what renders over whatever server opens next: the
// chosen chat, its transcript, the picker and the search, plus a mid-load
// flag of the server coming up. Each server's conversations and catalog
// stay in its scope, so a switch back to one shows its chats while the
// pipe dials, and its dial refreshes the list.
func (a *app) clearForSwitch() {
	a.connections.booted = false
	a.resetDraft("")
	a.layout.pickerOpen = false
	a.scope().moreLoading = false
	a.search.searchResults = nil
	a.search.searchLoading = false
	a.search.searchVersion++

}

// dropServer forgets one connection and everything the app kept for it.
// The local server and the active one are never dropped.
func (a *app) dropServer(id string) {
	if id == localServerID || id == a.connections.activeServer {
		return
	}
	a.invalidateServerWork(id)
	a.connections.servers = slices.DeleteFunc(a.connections.servers, func(s serverConn) bool { return s.id == id })
	delete(a.connections.scopes, id)
	delete(a.connections.serverHealth, id)
	delete(a.connections.serverFacts, id)
	delete(a.connections.pairLinks, id)
	prefix := id + "\x00"
	maps.DeleteFunc(a.connections.modelOff, func(key string, _ bool) bool { return strings.HasPrefix(key, prefix) })
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
	request := a.registryRequest("probe:" + cfg.id)
	runOwned(a, request, a.runtime.lifetime, func(ctx context.Context) (agent.Facts, error) {
		ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		return a.backend.Probe(ctx, cfg.transport())
	}, func(facts agent.Facts, err error) {
		if current, ok := a.serverByID(cfg.id); !ok || current != cfg {
			return
		}
		h := serverHealth{addr: strings.TrimSuffix(cfg.url, "/")}
		if err != nil {
			h.status, h.detail = healthDown, err.Error()
		} else {
			h.status, h.detail = healthReady, facts.Version
		}
		a.setHealth(cfg.id, h)
		a.setFacts(cfg.id, facts)
	})
}

func (a *app) registryRequest(key string) *requestState {
	if a.connections.registryRequests == nil {
		a.connections.registryRequests = map[string]*requestState{}
	}
	if a.connections.registryRequests[key] == nil {
		a.connections.registryRequests[key] = new(requestState)
	}
	return a.connections.registryRequests[key]
}

func (a *app) invalidateServerWork(id string) {
	delete(a.connections.serverFacts, id)
	for _, prefix := range []string{"probe:", "mint:"} {
		key := prefix + id
		if request := a.connections.registryRequests[key]; request != nil {
			request.stop()
			delete(a.connections.registryRequests, key)
		}
	}
}

// probeUntried tests the connections nothing has reached for yet, so the
// page tells the user something the moment they open it.
func (a *app) probeUntried() {
	for _, s := range a.connections.servers {
		if s.id == a.connections.activeServer || s.managed {
			continue
		}
		if a.connections.serverHealth[s.id].status == healthUntested {
			a.runtime.probe(s)
		}
	}
}

func (s serverConn) transport() agent.Config {
	return agent.Config{URL: s.url, Password: s.password, Paired: s.paired, Managed: s.managed, Local: s.local}
}

func (a *app) pairLink(link string) (string, string, bool) {
	if pairing, ok := a.backend.(agent.Pairing); ok {
		return pairing.ParsePairLink(link)
	}
	return "", "", false
}

func (a *app) serverPasswordLine(s serverConn) string {
	var lifetime time.Duration
	if pairing, ok := a.backend.(agent.Pairing); ok {
		lifetime = pairing.PairingPolicy().TokenLifetime
	}
	return connectionPasswordLine(s, lifetime)
}
