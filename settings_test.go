package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestSettingsOpenAndClose drives the settings page in and out: the rail's
// gear opens it on the Servers tab, the chat furniture steps aside, and
// Escape, the close shape and any rail place all leave it behind.
func TestSettingsOpenAndClose(t *testing.T) {
	a := newApp()
	a.sessionsLoaded = true
	a.sessions = []agentSession{{id: "ses_a", title: "Grep wiring"}}
	a.selectedSession = "ses_a"
	a.selected = "Grep wiring"
	tt := ui.NewTester(a.view, 1400, 920)

	if tt.HasText("Add server") {
		t.Fatal("the settings page stands open before anything asked")
	}
	if err := tt.Click("Settings"); err != nil {
		t.Fatalf("the rail gear: %v", err)
	}
	if !a.settingsOpen {
		t.Fatal("the gear did not open the settings")
	}
	tt.Frame()
	if !tt.HasText("Add server") || !tt.HasText("Local server") {
		t.Fatalf("the Servers tab is not drawn: %v", tt.Texts())
	}
	// The page takes the card: the conversations and the composer sit it out.
	if tt.HasText("Pinned") || tt.HasText("Compose") {
		t.Fatalf("the chat furniture stayed under the settings: %v", tt.Texts())
	}
	// The title bar names the page where the conversation's title stood.
	if title, ok := tt.Find("Settings"); !ok || title.Y >= 48 {
		t.Fatalf("the title bar does not name the settings: %+v", title)
	}

	// The nav moves to Models and back to Servers.
	if err := tt.Click("Settings Models"); err != nil {
		t.Fatalf("the Models item: %v", err)
	}
	if a.settingsTab != tabModels {
		t.Fatalf("tab %q after the click", a.settingsTab)
	}
	tt.Frame()
	if err := tt.Click("Settings Servers"); err != nil {
		t.Fatalf("the Servers item: %v", err)
	}
	if a.settingsTab != tabServers {
		t.Fatalf("tab %q after the click back", a.settingsTab)
	}

	// Escape closes the page.
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	if a.settingsOpen {
		t.Fatal("Escape did not close the settings")
	}
	tt.Frame()
	if tt.HasText("Add server") || !tt.HasText("Pinned") {
		t.Fatalf("the chat did not come back: %v", tt.Texts())
	}

	// The close shape does the same, and a rail place does too: the
	// selection lands back where it was.
	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Close settings"); err != nil {
		t.Fatal(err)
	}
	if a.settingsOpen {
		t.Fatal("the close shape did not close the settings")
	}
	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Home"); err != nil {
		t.Fatal(err)
	}
	if a.settingsOpen {
		t.Fatal("a rail place did not leave the settings")
	}
}

// TestServerMenuKeepsThePage: choosing from a row's menu leaves the page
// where it was. (Escape with a native menu up is the menu's own key to
// take; the headless tester does not model that, so it stays untested.)
func TestServerMenuKeepsThePage(t *testing.T) {
	a := newApp()
	a.servers = append(a.servers, serverConn{id: "srv-box", name: "Build box", url: "http://10.0.0.12:4096"})
	a.settingsOpen = true
	tt := ui.NewTester(a.view, 1400, 920)

	if err := tt.Click("Server options, Build box"); err != nil {
		t.Fatal(err)
	}
	if len(tt.Menu()) == 0 {
		t.Fatal("the menu did not open")
	}
	if err := tt.ChooseMenuItem("Test connection"); err != nil {
		t.Fatal(err)
	}
	if !a.settingsOpen || a.settingsTab != tabServers {
		t.Fatalf("the menu disturbed the page: open %v tab %q", a.settingsOpen, a.settingsTab)
	}
	tt.CloseMenu()
	tt.Frame()
	if !tt.HasText("Add server") {
		t.Fatalf("the page left with the menu: %v", tt.Texts())
	}
}

// TestServerRowShowsState keeps the row's words honest: the address, how
// the server last answered and whether a key is set, in every state the
// health map can hold.
func TestServerRowShowsState(t *testing.T) {
	local := defaultLocalServer()
	local.password = "key-1"
	remote := serverConn{id: "srv-box", name: "Build box", url: "http://10.0.0.12:4096"}

	for _, tc := range []struct {
		name string
		cfg  serverConn
		h    serverHealth
		want []string
	}{
		{"running local", local, serverHealth{status: healthReady, detail: "0.5.5", addr: "http://127.0.0.1:50112"},
			[]string{"http://127.0.0.1:50112", "Connected · 0.5.5", "Key set"}},
		{"starting local", local, serverHealth{status: healthConnecting},
			[]string{"Starting…", "Key set"}},
		{"stopped local", local, serverHealth{status: healthStopped},
			[]string{"Stopped", "Key set"}},
		{"untested remote", remote, serverHealth{},
			[]string{"http://10.0.0.12:4096", "Not tested yet", "No key set"}},
		{"refused remote", remote, serverHealth{status: healthDown, detail: "the key was refused"},
			[]string{"Not connected — the key was refused"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			line := serverLine(tc.cfg, tc.h)
			for _, want := range tc.want {
				if !strings.Contains(line+serverKeyLine(tc.cfg), want) {
					t.Fatalf("row %q / %q lacks %q", line, serverKeyLine(tc.cfg), want)
				}
			}
		})
	}

	// The card draws the active badge and the state dot.
	a := newApp()
	a.servers = append(a.servers, serverConn{id: "srv-box", name: "Build box", url: "http://10.0.0.12:4096"})
	a.setHealth("srv-box", serverHealth{status: healthDown, detail: "no answer"})
	a.settingsOpen = true
	a.settingsTab = tabServers
	tt := ui.NewTester(a.view, 1400, 920)
	if !tt.HasText("Active") || !tt.HasText("Local server") || !tt.HasText("Build box") {
		t.Fatalf("the rows are off: %v", tt.Texts())
	}
	if !tt.HasText("Not connected — no answer") {
		t.Fatalf("the failing server reads wrong: %v", tt.Texts())
	}
	if !tt.HasText("Use Build box") {
		t.Fatalf("the other server has no way to be used: %v", tt.Texts())
	}
}

// TestServerUseSwitches queues the dial, clears what belonged to the old
// server and hands the pins of the new one back.
func TestServerUseSwitches(t *testing.T) {
	a := newApp()
	a.servers = append(a.servers, serverConn{id: "srv-box", name: "Build box", url: "http://10.0.0.12:4096"})
	a.settingsOpen = true
	a.sessionsLoaded = true
	a.sessions = []agentSession{{id: "ses_local", title: "Local chat"}}
	a.selectedSession = "ses_local"
	a.selected = "Local chat"
	a.models = []agentModel{{providerID: "opencode-go", id: "beam-free", name: "Beam Free", free: true}}
	a.modelsLoaded = true
	a.pinned["ses_local"] = true
	a.pinOrder = []string{"ses_local"}
	a.pinsByServer["srv-box"] = []string{"ses_remote"}

	tt := ui.NewTester(a.view, 1400, 920)
	if err := tt.Click("Use Build box"); err != nil {
		t.Fatalf("the Use action: %v", err)
	}
	if a.activeServer != "srv-box" {
		t.Fatalf("active server %q after the click", a.activeServer)
	}
	if a.selectedSession != "" || len(a.sessions) != 0 || len(a.models) != 0 || a.modelsLoaded {
		t.Fatalf("the old server's view stayed: %q %v %v", a.selectedSession, a.sessions, a.models)
	}
	if !a.pinned["ses_remote"] || len(a.pinOrder) != 1 || a.pinOrder[0] != "ses_remote" || a.pinned["ses_local"] {
		t.Fatalf("pins did not follow the switch: %v %v", a.pinned, a.pinOrder)
	}
	if got := a.pinsByServer[localServerID]; len(got) != 1 || got[0] != "ses_local" {
		t.Fatalf("the local pins were not kept: %v", a.pinsByServer)
	}
	select {
	case job := <-a.jobs:
		if job.kind != "server" || job.server.id != "srv-box" || len(job.pins) != 1 || job.pins[0] != "ses_remote" {
			t.Fatalf("switch job %+v", job)
		}
	default:
		t.Fatal("the switch queued no dial")
	}
	// The local server is only run while it is active.
	if h := a.serverHealth[localServerID]; h.status != healthStopped {
		t.Fatalf("the local server's health says %+v", h)
	}
	tt.Frame()
	if !tt.HasText("Stopped") || !tt.HasText("Use Local server") {
		t.Fatalf("the stopped local row reads wrong: %v", tt.Texts())
	}
	if err := tt.Click("Server options, Local server"); err == nil {
		t.Fatal("the stopped local row kept a menu")
	}
	if err := tt.Click("Server options, Build box"); err != nil {
		t.Fatalf("the active row lost its menu: %v", err)
	}
	if menu := tt.Menu(); len(menu) < 2 || menu[0] != "Reconnect" {
		t.Fatalf("the active row's menu offers %v", menu)
	}
	tt.CloseMenu()
}

// TestServerEditorAddsRemote drives the add dialog: a nameless save stays
// with a note, a real one lands in the list and is probed.
func TestServerEditorAddsRemote(t *testing.T) {
	a := newApp()
	probed := []string{}
	a.probe = func(cfg serverConn) { probed = append(probed, cfg.id) }
	a.settingsOpen = true
	tt := ui.NewTester(a.view, 1400, 920)

	if err := tt.Click("Add server"); err != nil {
		t.Fatalf("the Add server action: %v", err)
	}
	if a.serverEditor == nil || !a.serverEditor.open {
		t.Fatal("the editor did not open")
	}
	tt.Frame()
	if !tt.HasText("Add a server") || !tt.HasText("Address") {
		t.Fatalf("the editor is not drawn: %v", tt.Texts())
	}
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	if a.serverEditor == nil || !strings.Contains(a.serverEditor.note, "name") {
		t.Fatalf("a nameless server saved anyway: %+v", a.serverEditor)
	}
	a.serverEditor.name = "Build box"
	a.serverEditor.url = "not an address"
	tt.Frame()
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	if a.serverEditor == nil || a.serverEditor.note == "" {
		t.Fatal("a bad address saved anyway")
	}
	a.serverEditor.url = "10.0.0.12:4096"
	a.serverEditor.password = "s3cret"
	tt.Frame()
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	if a.serverEditor != nil {
		t.Fatal("the editor stayed open after a save")
	}
	if len(a.servers) != 2 {
		t.Fatalf("servers after the add: %+v", a.servers)
	}
	added := a.servers[1]
	if added.name != "Build box" || added.url != "http://10.0.0.12:4096" || added.password != "s3cret" || added.local {
		t.Fatalf("the added server reads %+v", added)
	}
	if len(probed) != 1 || probed[0] != added.id {
		t.Fatalf("the new server was not probed: %v", probed)
	}
	// The list shows it with the key it was given.
	tt.Frame()
	if !tt.HasText("Build box") || !tt.HasText("Key set") {
		t.Fatalf("the row is not drawn: %v", tt.Texts())
	}
}

// TestServerEditorEditsLocalKey keeps the local row's editor on its own
// terms: no address to type, a key to set, and a redial because the key
// travels with the server the app starts.
func TestServerEditorEditsLocalKey(t *testing.T) {
	a := newApp()
	a.settingsOpen = true
	tt := ui.NewTester(a.view, 1400, 920)

	if err := tt.Click("Edit Local server"); err != nil {
		t.Fatalf("the Edit action: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Edit Local server") || !tt.HasText("Run it with the app") {
		t.Fatalf("the local editor is not drawn: %v", tt.Texts())
	}
	if tt.HasText("Address") {
		t.Fatalf("the managed server asks for an address: %v", tt.Texts())
	}
	a.serverEditor.password = "chosen-key"
	tt.Frame()
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	if a.servers[0].password != "chosen-key" || a.servers[0].managed != true || a.servers[0].url != "" {
		t.Fatalf("the local server reads %+v", a.servers[0])
	}
	select {
	case job := <-a.jobs:
		if job.kind != "server" || job.server.password != "chosen-key" {
			t.Fatalf("the redial job %+v", job)
		}
	default:
		t.Fatal("editing the active server's key did not queue a redial")
	}

	// Pointing the local row at a server the user runs themselves needs an
	// address, and dials it instead of starting one.
	a.servers[0].password = ""
	if err := tt.Click("Edit Local server"); err != nil {
		t.Fatal(err)
	}
	a.serverEditor.name = "Local server"
	a.serverEditor.managed = false
	tt.Frame()
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	if a.serverEditor == nil || a.serverEditor.note == "" {
		t.Fatal("an addressless self-run server saved")
	}
	a.serverEditor.url = "http://127.0.0.1:4096"
	tt.Frame()
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	if a.servers[0].managed || a.servers[0].url != "http://127.0.0.1:4096" {
		t.Fatalf("the self-run local server reads %+v", a.servers[0])
	}
}

// TestDeleteServerConfirms keeps the local row and the active one out of
// harm's way, and takes a menu Delete through its dialog.
func TestDeleteServerConfirms(t *testing.T) {
	a := newApp()
	a.servers = append(a.servers,
		serverConn{id: "srv-box", name: "Build box", url: "http://10.0.0.12:4096"},
		serverConn{id: "srv-lab", name: "Lab", url: "http://10.0.0.13:4096", password: "k"},
	)
	a.pinsByServer["srv-box"] = []string{"ses_x"}
	a.modelOff[modelKey("srv-box", "opencode-go", "beam-free")] = true
	a.setHealth("srv-box", serverHealth{status: healthReady, detail: "0.5.5"})
	a.settingsOpen = true
	tt := ui.NewTester(a.view, 1400, 920)

	// The local row offers no Delete at all: reconnecting is all it has.
	if err := tt.Click("Server options, Local server"); err != nil {
		t.Fatal(err)
	}
	if menu := tt.Menu(); len(menu) != 1 || menu[0] != "Reconnect" {
		t.Fatalf("the local server's menu offers %v", menu)
	}
	tt.CloseMenu()

	if err := tt.Click("Server options, Build box"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Delete…"); err != nil {
		t.Fatal(err)
	}
	if a.serverConfirm == nil || !a.serverConfirm.open {
		t.Fatal("no confirmation was asked for")
	}
	tt.Frame()
	if !tt.HasText("Delete “Build box”?") {
		t.Fatalf("the dialog is not drawn: %v", tt.Texts())
	}
	if err := tt.Click("Delete"); err != nil {
		t.Fatal(err)
	}
	if len(a.servers) != 2 || a.servers[1].id != "srv-lab" {
		t.Fatalf("servers after the delete: %+v", a.servers)
	}
	if _, ok := a.pinsByServer["srv-box"]; ok {
		t.Fatalf("the deleted server kept its pins: %v", a.pinsByServer)
	}
	if a.modelOff[modelKey("srv-box", "opencode-go", "beam-free")] {
		t.Fatalf("the deleted server kept its model choices: %v", a.modelOff)
	}
	if _, ok := a.serverHealth["srv-box"]; ok {
		t.Fatal("the deleted server kept its health")
	}

	// The active row refuses the delete outright.
	a.activeServer = "srv-lab"
	a.requestDeleteServer(a.servers[1])
	if a.serverConfirm != nil {
		t.Fatal("the active server asked for a delete confirmation")
	}
}

// TestModelsTabToggles drives the Models tab: the counts, the per-model
// switch, and the two bulk actions over the whole catalog.
func TestModelsTabToggles(t *testing.T) {
	a := newApp()
	a.models = []agentModel{
		{providerID: "opencode-go", id: "beam-free", name: "Beam Free", free: true},
		{providerID: "opencode-go", id: "minimax-m3", name: "MiniMax-M3", variants: []string{"low", "high"}},
		{providerID: "wada", id: "flash", name: "Flash", free: true},
	}
	a.modelsLoaded = true
	a.settingsOpen = true
	a.settingsTab = tabModels
	tt := ui.NewTester(a.view, 1400, 920)

	if !tt.HasText("3 of 3 on") || !tt.HasText("Beam Free") || !tt.HasText("Flash") {
		t.Fatalf("the catalog is not drawn: %v", tt.Texts())
	}
	// The providers head their groups, alphabetically.
	texts := tt.Texts()
	if at(texts, "opencode-go") > at(texts, "wada") {
		t.Fatalf("providers are out of order: %v", texts)
	}

	// One switch takes one model out of the picker.
	if err := tt.Click("Keep MiniMax-M3 in the composer's picker"); err != nil {
		t.Fatalf("the switch: %v", err)
	}
	if !a.modelOff[modelKey(localServerID, "opencode-go", "minimax-m3")] {
		t.Fatalf("the switch did not take: %v", a.modelOff)
	}
	tt.Frame()
	if !tt.HasText("2 of 3 on") {
		t.Fatalf("the count did not follow: %v", tt.Texts())
	}
	if got := a.filteredModels(); len(got) != 2 {
		t.Fatalf("the picker offers %+v", got)
	}

	// Disable all empties the picker and warns; Enable all refills it.
	if err := tt.Click("Disable all"); err != nil {
		t.Fatal(err)
	}
	if on, total := a.enabledModelCount(); on != 0 || total != 3 {
		t.Fatalf("after Disable all: %d of %d", on, total)
	}
	if len(a.filteredModels()) != 0 {
		t.Fatal("the picker still offers models")
	}
	tt.Frame()
	if !tt.HasText("Every model is off") {
		t.Fatalf("the warning is missing: %v", tt.Texts())
	}
	if err := tt.Click("Enable all"); err != nil {
		t.Fatal(err)
	}
	if on, _ := a.enabledModelCount(); on != 3 {
		t.Fatalf("%d models on after Enable all", on)
	}

	// The search narrows the rows without changing what is on.
	a.modelsQuery = "wada"
	tt.Frame()
	if tt.HasText("Beam Free") || !tt.HasText("Flash") {
		t.Fatalf("the search did not narrow: %v", tt.Texts())
	}
	a.modelsQuery = ""
}

// TestModelsFollowTheServer keeps each server's choices its own: turning a
// model off on one leaves the other server's catalog alone.
func TestModelsFollowTheServer(t *testing.T) {
	a := newApp()
	a.servers = append(a.servers, serverConn{id: "srv-box", name: "Build box", url: "http://10.0.0.12:4096"})
	a.models = []agentModel{{providerID: "opencode-go", id: "beam-free", name: "Beam Free", free: true}}
	a.modelsLoaded = true
	a.setModelEnabled(a.models[0], false)
	if on, _ := a.enabledModelCount(); on != 0 {
		t.Fatal("the switch did not take on the local server")
	}
	a.activeServer = "srv-box"
	if on, _ := a.enabledModelCount(); on != 1 {
		t.Fatal("the switch leaked across servers")
	}
	if len(a.filteredModels()) != 1 {
		t.Fatal("the picker follows the wrong server's choices")
	}
}

// TestComposerPickerHidesDisabledModels drives the picker the settings
// feed: turned-off models stay out, an empty offer says why, and Manage
// models opens the tab behind it.
func TestComposerPickerHidesDisabledModels(t *testing.T) {
	a := newApp()
	a.models = []agentModel{
		{providerID: "opencode-go", id: "beam-free", name: "Beam Free", free: true},
		{providerID: "opencode-go", id: "minimax-m3", name: "MiniMax-M3"},
	}
	a.modelsLoaded = true
	a.sessions = []agentSession{{id: "ses_z", title: "Free turn",
		model: agentModelRef{providerID: "opencode-go", id: "beam-free"}}}
	a.selectedSession = "ses_z"
	a.selected = "Free turn"
	a.setModelEnabled(a.models[1], false)
	tt := ui.NewTester(a.view, 1400, 920)

	if err := tt.Click("Model Beam Free"); err != nil {
		t.Fatalf("opening the picker: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Beam Free") || tt.HasText("MiniMax-M3") {
		t.Fatalf("the picker ignored the settings: %v", tt.Texts())
	}
	if err := tt.Click("Manage models…"); err != nil {
		t.Fatalf("the manage row: %v", err)
	}
	if a.pickerOpen || !a.settingsOpen || a.settingsTab != tabModels {
		t.Fatalf("manage did not land on the Models tab: picker %v settings %v tab %q",
			a.pickerOpen, a.settingsOpen, a.settingsTab)
	}

	// Everything off: the picker says so and still offers the way out.
	a.settingsOpen = false
	a.setAllModelsEnabled(false)
	a.pickerOpen = true
	tt.Frame()
	if !tt.HasText("Every model is off") {
		t.Fatalf("the empty offer is unexplained: %v", tt.Texts())
	}
	if !tt.HasText("Manage models…") {
		t.Fatalf("the way to the settings is gone: %v", tt.Texts())
	}
}

// TestSendCarriesTheDefault keeps a new turn on the model the settings
// enabled: the job names it, so the runner does not fall back on its own.
func TestSendCarriesTheDefault(t *testing.T) {
	a := newApp()
	a.models = []agentModel{{providerID: "wada", id: "flash", name: "Flash", free: true}}
	a.modelsLoaded = true
	a.draft = "hello"
	a.send()
	select {
	case job := <-a.jobs:
		if job.kind != "prompt" || job.provider != "wada" || job.model != "flash" {
			t.Fatalf("send job %+v", job)
		}
	default:
		t.Fatal("the send queued nothing")
	}
}

// TestAPIBaseNormalizes keeps the typed addresses of the editor honest.
func TestAPIBaseNormalizes(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
		bad  bool
	}{
		{"http://10.0.0.12:4096", "http://10.0.0.12:4096/api", false},
		{"10.0.0.12:4096", "http://10.0.0.12:4096/api", false},
		{"http://10.0.0.12:4096/", "http://10.0.0.12:4096/api", false},
		{"https://box.local/api", "https://box.local/api", false},
		{"  http://127.0.0.1:5000/base/  ", "http://127.0.0.1:5000/base/api", false},
		{"", "", true},
		{"not an address", "", true},
	} {
		got, err := apiBase(tc.in)
		if tc.bad {
			if err == nil {
				t.Fatalf("apiBase(%q) = %q, want an error", tc.in, got)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Fatalf("apiBase(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
}

// TestProbeServer checks the connection test the settings run: a version
// out of /info, and the key's refusal told apart from a dead address.
func TestProbeServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, pw, ok := r.BasicAuth(); !ok || user != "opencode" || pw != "right-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"version":"0.5.5"}`))
	}))
	defer srv.Close()

	version, err := probeServer(context.Background(), serverConn{url: srv.URL, password: "right-key"})
	if err != nil || version != "0.5.5" {
		t.Fatalf("probe %q %v", version, err)
	}
	if _, err := probeServer(context.Background(), serverConn{url: srv.URL, password: "wrong"}); err == nil ||
		!strings.Contains(err.Error(), "refused") {
		t.Fatalf("a refused key reads %v", err)
	}
	if _, err := probeServer(context.Background(), serverConn{url: "http://127.0.0.1:1"}); err == nil {
		t.Fatal("a dead address probed fine")
	}
}

// TestBootOnAStoredRemote keeps a restart honest: the app boots onto the
// server it was last using, with its own list and pins, and the local
// server it left behind reads as stopped.
func TestBootOnAStoredRemote(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata.db")
	a := newApp()
	db, err := a.loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	a.metadata = startMetadataWriter(db, func(_ uint64, err error) {
		if err != nil {
			t.Error(err)
		}
	})
	a.servers = append(a.servers, serverConn{id: "srv-box", name: "Build box", url: "http://10.0.0.12:4096", password: "k"})
	a.pinsByServer["srv-box"] = []string{"ses_box"}
	a.activeServer = "srv-box"
	a.saveState()
	a.closeMetadata()

	b := newApp()
	db, err = b.loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if b.activeServer != "srv-box" || b.activeServerConn().id != "srv-box" {
		t.Fatalf("the app would dial %+v", b.activeServerConn())
	}
	if len(b.servers) != 2 || b.servers[1].name != "Build box" || b.servers[1].password != "k" {
		t.Fatalf("servers %+v", b.servers)
	}
	if len(b.pinOrder) != 1 || b.pinOrder[0] != "ses_box" || !b.pinned["ses_box"] {
		t.Fatalf("pins %+v %v", b.pinOrder, b.pinned)
	}
	if h := b.serverHealth[localServerID]; h.status != healthStopped {
		t.Fatalf("the idle local server reads %+v", h)
	}
}

// at is the first index of a text in the frame's list, or -1.
func at(texts []string, s string) int {
	for i, text := range texts {
		if text == s {
			return i
		}
	}
	return -1
}
