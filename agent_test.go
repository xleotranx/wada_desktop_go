package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// fakeAgent is a tiny opencode server speaking just the endpoints the
// client uses, over an httptest.Server.
func fakeAgent(t *testing.T, prompts chan<- string, models chan<- string) (*agent, context.CancelFunc) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/info", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"version":"test"}`))
	})
	mux.HandleFunc("POST /api/session", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{"id":"ses_t1"}}`))
	})
	mux.HandleFunc("POST /api/session/ses_t1/model", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Model struct {
				ProviderID string `json:"providerID"`
				ID         string `json:"id"`
			} `json:"model"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		models <- in.Model.ProviderID + "/" + in.Model.ID
	})
	mux.HandleFunc("POST /api/session/ses_t1/prompt", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Text string `json:"text"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		prompts <- in.Text
		w.Write([]byte(`{"data":{"id":"msg_u1","type":"user"}}`))
	})
	mux.HandleFunc("GET /api/session", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[` +
			`{"id":"ses_b","title":"Older","time":{"created":50,"updated":100}},` +
			`{"id":"ses_a","title":"Grep wiring","time":{"created":150,"updated":300}}]}`))
	})
	mux.HandleFunc("GET /api/session/ses_a/message", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[` +
			`{"id":"msg_u","type":"user","time":{"created":1},"payload":{"text":"Run the wiring check"}},` +
			`{"id":"msg_r","type":"assistant","time":{"created":2},"content":[` +
			`{"type":"text","text":"Running it now."},` +
			`{"type":"tool","id":"call_9","name":"shell","state":{` +
			`"status":"completed","input":{"command":"go test ./wire"},` +
			`"content":[{"type":"text","text":"ok\n"}],` +
			`"metadata":{"status":"completed","exit":0}}}]},` +
			`{"id":"msg_u2","type":"user","time":{"created":3},"text":"Top-level hello"},` +
			`{"id":"msg_i","type":"idle","outcome":"succeeded"}]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	a := &agent{base: srv.URL + "/api", pw: "test", http: srv.Client()}
	return a, func() {}
}

// useFreeModel points one conversation at the app's free default, the way
// the runner does before a new chat's first words.
func useFreeModel(t *testing.T, ag *agent, ctx context.Context, sid string) (prov, name string) {
	t.Helper()
	prov, name = sessionModel()
	if err := ag.setModel(ctx, sid, prov, name, ""); err != nil {
		t.Fatal(err)
	}
	return prov, name
}

// TestAgentRoundTrip checks the client speaks the server's wrapping: a
// session id out of {"data":…} and the prompt text into the request body.
func TestAgentRoundTrip(t *testing.T) {
	prompts := make(chan string, 1)
	models := make(chan string, 1)
	a, _ := fakeAgent(t, prompts, models)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sid, _, err := a.createSession(ctx, "")
	if err != nil || sid != "ses_t1" {
		t.Fatalf("createSession %q %v", sid, err)
	}
	if err := a.prompt(ctx, sid, "hello there"); err != nil {
		t.Fatal(err)
	}
	select {
	case text := <-prompts:
		if text != "hello there" {
			t.Fatalf("prompt sent %q", text)
		}
	default:
		t.Fatal("the prompt body never reached the server")
	}
}

// TestAgentRefusesOversizedBody keeps a response over the read cap out of
// the JSON decoder, which would otherwise report a body cut in half as a
// syntax error.
func TestAgentRefusesOversizedBody(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/session/big/message", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"type":"user","payload":{"text":"`))
		w.Write(bytes.Repeat([]byte("x"), 4096))
		w.Write([]byte(`"}}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	ag := &agent{base: srv.URL + "/api", pw: "test", http: srv.Client(), bodyCap: 1024}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := ag.sessionMessages(ctx, "big"); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("an oversized body read as %v", err)
	}
	// Under the cap the same endpoint reads fine.
	ag.bodyCap = 8192
	msgs, err := ag.sessionMessages(ctx, "big")
	if err != nil || len(msgs) != 1 || len(msgs[0].Payload.Text) != 4096 {
		t.Fatalf("a body within the cap failed: %d messages, %v", len(msgs), err)
	}
}

// TestAgentEventsStream checks the SSE reader decodes envelopes in order
// and skips the stream's comments.
func TestAgentEventsStream(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/event", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		fmt.Fprintln(w, ": heartbeat")
		fl.Flush()
		fmt.Fprintln(w, `data: {"type":"server.connected","data":{}}`)
		fl.Flush()
		fmt.Fprintln(w, `data: {"type":"session.text.delta","data":{"assistantMessageID":"msg_a","ordinal":0,"delta":"PONG"}}`)
		fl.Flush()
		<-r.Context().Done() // the stream ends when the client walks away
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	a := &agent{base: srv.URL + "/api", pw: "test", http: srv.Client()}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	evs := make(chan agentEvent, 8)
	go a.events(ctx, func(ev agentEvent) { evs <- ev })

	select {
	case ev := <-evs:
		if ev.Type != "server.connected" {
			t.Fatalf("first event %q", ev.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no first event")
	}
	select {
	case ev := <-evs:
		var d evTextData
		if json.Unmarshal(ev.Data, &d) != nil || d.Delta != "PONG" {
			t.Fatalf("delta event %s", ev.Data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no delta event")
	}
	select {
	case ev := <-evs:
		t.Fatalf("heartbeat decoded as %q", ev.Type)
	case <-time.After(200 * time.Millisecond):
	}
}

// TestApplyTurn walks one turn through apply and checks the kinds the
// transcript draws: a growing preview line, a settled text block, and one
// activity row per tool call with its detail.
func TestApplyTurn(t *testing.T) {
	a := newApp()
	upTo := len(a.blocks)
	step := func(typ, data string) {
		a.apply(agentEvent{Type: typ, Data: json.RawMessage(data)})
	}

	step("session.text.started", `{"sessionID":"s","assistantMessageID":"msg_a","ordinal":0}`)
	last := a.blocks[len(a.blocks)-1]
	if last.kind != "stream" || last.text != "" {
		t.Fatalf("preview row kind %q text %q", last.kind, last.text)
	}
	step("session.text.delta", `{"assistantMessageID":"msg_a","ordinal":0,"delta":"PONG"}`)
	step("session.text.delta", `{"assistantMessageID":"msg_a","ordinal":0,"delta":" now"}`)
	if last := a.blocks[len(a.blocks)-1]; last.text != "PONG now" {
		t.Fatalf("preview text %q", last.text)
	}
	step("session.text.ended", `{"assistantMessageID":"msg_a","ordinal":0,"text":"PONG now"}`)
	if last := a.blocks[len(a.blocks)-1]; last.kind != "text" || last.text != "PONG now" {
		t.Fatalf("settled block kind %q text %q", last.kind, last.text)
	}
	// A second paragraph of the same answer is its own row.
	step("session.text.started", `{"sessionID":"s","assistantMessageID":"msg_a","ordinal":1}`)
	step("session.text.ended", `{"assistantMessageID":"msg_a","ordinal":1,"text":"Also, hi again."}`)
	if last := a.blocks[len(a.blocks)-1]; last.kind != "text" || last.text != "Also, hi again." {
		t.Fatalf("second block kind %q text %q", last.kind, last.text)
	}

	step("session.tool.input.started", `{"sessionID":"s","assistantMessageID":"msg_a","id":"call_1","name":"shell"}`)
	step("session.tool.called", `{"sessionID":"s","assistantMessageID":"msg_a","id":"call_1","input":{"command":"echo hi"}}`)
	if row := a.blocks[len(a.blocks)-1]; row.kind != "activity" || row.text != "Ran command" {
		t.Fatalf("activity row %+v", row)
	}
	step("session.tool.success", `{"sessionID":"s","assistantMessageID":"msg_a","id":"call_1","content":[{"type":"text","text":"hi\n"}],"metadata":{"status":"completed","exit":0}}`)

	// The whole turn rendered: the agent's words and the expanded detail
	// of what its tool printed.
	a.selectedSession = "ses_seed"
	a.selected = "Wired turn"
	a.expanded["activity:call_1"] = true
	tt := ui.NewTester(a.view, 1400, 920)
	if got := len(a.blocks) - upTo; got < 3 {
		t.Fatalf("turn added %d rows", got)
	}
	if !tt.HasText("Ran command") || !tt.HasText("$ echo hi") || !tt.HasText("hi") {
		t.Fatalf("tool detail not drawn: %v", tt.Texts())
	}
}

// TestAgentModel checks what a new conversation is pointed at: the app's
// free default while the catalog still offers it, the first enabled model
// once the settings turn it off, and the free default again with the
// picker empty. WADA_MODEL keeps overriding the free one.
func TestAgentModel(t *testing.T) {
	prompts := make(chan string, 1)
	models := make(chan string, 1)
	ag, _ := fakeAgent(t, prompts, models)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, _, err := ag.createSession(ctx, ""); err != nil {
		t.Fatal(err)
	}
	useFreeModel(t, ag, ctx, "ses_t1")
	select {
	case got := <-models:
		if got != "opencode-go/longcat-2.5-preview-free" {
			t.Fatalf("model set to %q", got)
		}
	default:
		t.Fatal("the model request never reached the server")
	}

	// The settings narrow the choice: the free default steps aside for
	// the first model still on, and comes back when none is.
	a := newApp()
	free := agentModel{providerID: "opencode-go", id: "longcat-2.5-preview-free", name: "LongCat", free: true}
	other := agentModel{providerID: "wada", id: "flash", name: "Flash", free: true}
	a.scope().models = []agentModel{other, free}
	if ref := a.defaultModelRef(); ref.providerID != "opencode-go" || ref.id != "longcat-2.5-preview-free" {
		t.Fatalf("default %+v, want the free default", ref)
	}
	a.setModelEnabled(free, false)
	if ref := a.defaultModelRef(); ref.providerID != "wada" || ref.id != "flash" {
		t.Fatalf("default %+v, want the first enabled model", ref)
	}
	a.setModelEnabled(other, false)
	if ref := a.defaultModelRef(); ref.id != "longcat-2.5-preview-free" {
		t.Fatalf("default %+v, want the free default back", ref)
	}
}

// TestDefaultFollowsTheServer checks the chooser's order for a server whose
// catalog is not this app's: a model that runs for nothing beats the
// server's own default, the default stands when nothing free is offered or
// before the catalog has landed, and a default the settings turned off
// steps aside.
func TestDefaultFollowsTheServer(t *testing.T) {
	a := newApp()
	paid := agentModel{providerID: "wada", id: "pro", name: "Pro"}
	a.scope().models = []agentModel{paid}
	a.scope().serverDefault = agentModelRef{providerID: "wada", id: "their-default"}
	if ref := a.defaultModelRef(); ref.providerID != "wada" || ref.id != "their-default" {
		t.Fatalf("default %+v, want the server's own", ref)
	}
	freebie := agentModel{providerID: "wada", id: "flash", name: "Flash", free: true}
	a.scope().models = []agentModel{paid, freebie}
	if ref := a.defaultModelRef(); ref.id != "flash" {
		t.Fatalf("default %+v, want the free model", ref)
	}
	// A default the settings turned off leaves the catalog standing.
	a.setModelEnabled(agentModel{providerID: "wada", id: "their-default"}, false)
	a.scope().models = []agentModel{paid}
	if ref := a.defaultModelRef(); ref.id != "pro" {
		t.Fatalf("default %+v, want the paid catalog model", ref)
	}
	// Before the catalog lands, the server's own stands in place of a
	// model it may never have heard of.
	b := newApp()
	b.scope().serverDefault = agentModelRef{providerID: "remote", id: "their-default"}
	if ref := b.defaultModelRef(); ref.id != "their-default" {
		t.Fatalf("default %+v, want the server's own before a catalog", ref)
	}
}

// TestAgentDefaultModel reads the model the server would run itself, the
// fallback for a catalog that offers nothing this app knows.
func TestAgentDefaultModel(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/model/default", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"location":{"directory":"/w"},"data":` +
			`{"id":"minimax-m3","providerID":"opencode-go","variant":"max"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	a := &agent{base: srv.URL + "/api", pw: "test", http: srv.Client()}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ref, err := a.defaultModel(ctx)
	if err != nil || ref.providerID != "opencode-go" || ref.id != "minimax-m3" || ref.variant != "max" {
		t.Fatalf("default %+v %v", ref, err)
	}
	if _, err := (&agent{base: "http://127.0.0.1:1/api", http: httpClient()}).defaultModel(ctx); err == nil {
		t.Fatal("a dead address named a default")
	}
}

func TestSessionModelOverride(t *testing.T) {
	t.Setenv("WADA_MODEL", "opencode/glm-5.3-flash-free")
	if prov, name := sessionModel(); prov+"/"+name != "opencode/glm-5.3-flash-free" {
		t.Fatalf("override %s/%s", prov, name)
	}
}

// TestAgentSessions checks the sidebar list comes back newest first with
// its titles.
func TestAgentSessions(t *testing.T) {
	prompts := make(chan string, 1)
	models := make(chan string, 1)
	a, _ := fakeAgent(t, prompts, models)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sessions, next, err := a.listSessions(ctx, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if next != "" {
		t.Fatalf("unexpected cursor %q", next)
	}
	if len(sessions) != 2 ||
		sessions[0].id != "ses_a" || sessions[0].title != "Grep wiring" || sessions[0].updated != 300 ||
		sessions[1].id != "ses_b" {
		t.Fatalf("sessions %+v", sessions)
	}
}

// TestAgentProjects reads the folders a server works in: one row per
// project it knows, and a directory named twice turns up once. opencode
// hands the list over behind its data envelope or bare; both read alike.
func TestAgentProjects(t *testing.T) {
	shapes := []string{
		`{"data":[` +
			`{"id":"p1","canonical":"/srv/app"},` +
			`{"id":"p2","canonical":"/srv/docs"},` +
			`{"id":"p3","canonical":"/srv/app/"}` +
			`]}`,
		`[` +
			`{"id":"p1","canonical":"/srv/app"},` +
			`{"id":"p2","canonical":"/srv/docs"},` +
			`{"id":"p3","canonical":"/srv/app/"}` +
			`]`,
	}
	for _, body := range shapes {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/project", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(body))
		})
		// The server answers for the workspaces on its disk; a row for a
		// folder that is not there goes unanswered for, as a probe of one
		// reads, and drops out of the list.
		mux.HandleFunc("GET /api/fs/list", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("location[directory]") == "/srv/missing" {
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte(`{"_tag":"LocationNotFoundError","message":"Location not found: /srv/missing"}`))
				return
			}
			w.Write([]byte(`{"data":[]}`))
		})
		srv := httptest.NewServer(mux)
		a := &agent{base: srv.URL + "/api", pw: "test", http: srv.Client()}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		dirs, err := a.listProjects(ctx)
		cancel()
		srv.Close()
		if err != nil {
			t.Fatal(err)
		}
		if len(dirs) != 2 || dirs[0] != "/srv/app" || dirs[1] != "/srv/docs" {
			t.Fatalf("projects %+v", dirs)
		}
	}
}

// TestAgentProjectsOrderByActivity keeps the section's order the server's
// own: the folder a chat ran in most recently stands first, and rows the
// server wrote no activity for keep the order it listed them in.
func TestAgentProjectsOrderByActivity(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/project", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[` +
			`{"id":"p1","canonical":"/srv/app","time":{"created":1,"updated":1,"active":100}},` +
			`{"id":"p2","canonical":"/srv/docs","time":{"created":1,"updated":1,"active":300}},` +
			`{"id":"p3","canonical":"/srv/labs","time":{"created":1,"updated":1}},` +
			`{"id":"p4","canonical":"/srv/tools","time":{"created":1,"updated":1}}` +
			`]`))
	})
	mux.HandleFunc("GET /api/fs/list", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	a := &agent{base: srv.URL + "/api", pw: "test", http: srv.Client()}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dirs, err := a.listProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/srv/docs", "/srv/app", "/srv/labs", "/srv/tools"}
	if len(dirs) != len(want) {
		t.Fatalf("projects %+v, want %+v", dirs, want)
	}
	for i := range want {
		if dirs[i] != want[i] {
			t.Fatalf("projects %+v, want %+v", dirs, want)
		}
	}
}

// TestAgentProjectsFilterDropsPhantoms runs the same read against a list
// the server keeps beyond its disk: a project row for another machine's
// folder stays in its registry with no call to remove it, so the read
// asks each name of the file system and the misses never come through.
func TestAgentProjectsFilterDropsPhantoms(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/project", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[` +
			`{"id":"p1","canonical":"/Users/leotran/project/wada_labs/wada_desktop_go"},` +
			`{"id":"p2","canonical":"/tmp/opencode-wlpro"},` +
			`{"id":"p3","canonical":"/workspace/wada_cal"}` +
			`]`))
	})
	mux.HandleFunc("GET /api/fs/list", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("location[directory]") == "/workspace/wada_cal" {
			w.Write([]byte(`{"data":[]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"_tag":"LocationNotFoundError","message":"no"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	a := &agent{base: srv.URL + "/api", pw: "test", http: srv.Client()}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dirs, err := a.listProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 1 || dirs[0] != "/workspace/wada_cal" {
		t.Fatalf("the phantom rows did not drop: %+v", dirs)
	}
}

// TestAgentSessionRowsAskForRootsAndCost checks what the sidebar's read
// says and takes: root conversations only — a subagent's session belongs
// to the chat that spawned it — and the spending each row carries, which
// a build that hands children over anyway cannot slip past either.
func TestAgentSessionRowsAskForRootsAndCost(t *testing.T) {
	asked := make(chan string, 2)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/session", func(w http.ResponseWriter, r *http.Request) {
		asked <- r.URL.RawQuery
		w.Write([]byte(`{"data":[` +
			`{"id":"ses_root","title":"Reviewing the wiring","cost":0.3196,` +
			`"location":{"directory":"/srv/app"},"time":{"created":10,"updated":20}},` +
			`{"id":"ses_child","parentID":"ses_root","title":"Explore approvals","agent":"explore",` +
			`"location":{"directory":"/srv/app"},"time":{"created":11,"updated":21}}` +
			`],"cursor":{"next":""}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	a := &agent{base: srv.URL + "/api", pw: "test", http: srv.Client()}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, next, err := a.listSessions(ctx, "", "", "")
	if err != nil || next != "" {
		t.Fatalf("rows %v %v %q", rows, err, next)
	}
	query := <-asked
	if !strings.Contains(query, "parentID=null") {
		t.Fatalf("the read asked %q", query)
	}
	if len(rows) != 1 || rows[0].id != "ses_root" || rows[0].cost != 0.3196 {
		t.Fatalf("rows %+v", rows)
	}
	// A folder's own chats can be asked for the same way.
	if _, _, err := a.listSessions(ctx, "", "", "/srv/app"); err != nil {
		t.Fatal(err)
	}
	if q := <-asked; !strings.Contains(q, "directory=%2Fsrv%2Fapp") {
		t.Fatalf("the folder's read asked %q", q)
	}
}

// TestFreshSessionsPrepend keeps one later read from burying what is
// already listed: the server's newest page stands at the head in its own
// order, and the pages loaded before it follow, with the fresh copy of a
// row winning.
func TestFreshSessionsPrepend(t *testing.T) {
	kept := []agentSession{{id: "ses_a", updated: 30}, {id: "ses_b", updated: 20}}
	fresh := []agentSession{{id: "ses_c", updated: 40}, {id: "ses_a", updated: 31, cost: 0.5}}
	got := freshSessions(kept, fresh)
	if len(got) != 3 || got[0].id != "ses_c" || got[1].id != "ses_a" || got[2].id != "ses_b" {
		t.Fatalf("the list reads %+v", got)
	}
	if got[1].cost != 0.5 {
		t.Fatalf("the kept row did not take the fresh one: %+v", got[1])
	}
}

// TestRefreshAfterATurn queues the list read a finished turn earns, so the
// sidebar's order and the chat's spending catch up without a redial.
func TestRefreshAfterATurn(t *testing.T) {
	a := newApp()
	a.selectedSession = "ses_z"
	a.busySession = "ses_z"
	a.apply(agentEvent{Type: "session.execution.succeeded", Data: []byte(`{"sessionID":"ses_z"}`)})
	if a.busySession != "" {
		t.Fatalf("the turn is still busy: %q", a.busySession)
	}
	select {
	case job := <-a.jobs:
		if job.kind != "refresh" {
			t.Fatalf("the turn queued %+v", job)
		}
	default:
		t.Fatal("a finished turn did not queue a list read")
	}
}

// TestAgentSearch asks the store for a piece of a title, and the fake
// server checks the query the client actually sent.
func TestAgentSearch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/session", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("search") != "wiring" || q.Get("limit") != "50" {
			t.Errorf("query %q missed the search", r.URL.RawQuery)
		}
		w.Write([]byte(`{"data":[{"id":"ses_w","title":"Grep wiring",` +
			`"location":{"directory":"/nowhere"},` +
			`"model":{"id":"beam-free","providerID":"opencode-go"},` +
			`"time":{"created":1,"updated":2}}],"cursor":{"next":""}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	a := &agent{base: srv.URL + "/api", pw: "test", http: srv.Client()}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, _, err := a.listSessions(ctx, "", "wiring", "")
	if err != nil || len(rows) != 1 || rows[0].id != "ses_w" ||
		rows[0].title != "Grep wiring" || rows[0].model.id != "beam-free" {
		t.Fatalf("search rows %+v %v", rows, err)
	}
}

// TestRestoreBlocks turns a stored conversation into the same rows the
// live stream would draw.
func TestRestoreBlocks(t *testing.T) {
	prompts := make(chan string, 1)
	models := make(chan string, 1)
	a, _ := fakeAgent(t, prompts, models)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	msgs, err := a.sessionMessages(ctx, "ses_a")
	if err != nil || len(msgs) != 4 {
		t.Fatalf("messages %v %v", msgs, err)
	}
	blocks := restoreBlocks(msgs)
	if len(blocks) != 4 ||
		blocks[0].kind != "user" || blocks[0].text != "Run the wiring check" ||
		blocks[1].kind != "text" || blocks[1].text != "Running it now." ||
		blocks[2].kind != "activity" || blocks[2].text != "Ran command" ||
		blocks[3].kind != "user" || blocks[3].text != "Top-level hello" {
		t.Fatalf("restored blocks %+v", blocks)
	}
	// The tool row expands like a live one does.
	a2 := newApp()
	a2.blocks = blocks
	a2.selectedSession = "ses_seed"
	a2.selected = "Wiring check"
	a2.expanded["activity:call_9"] = true
	tt := ui.NewTester(a2.view, 1400, 920)
	if !tt.HasText("$ go test ./wire") || !tt.HasText("ok") {
		t.Fatalf("stored tool detail not drawn: %v", tt.Texts())
	}
}

// Only pinned chats appear in the sidebar until the user searches.
func TestSidebarPinnedWithoutRecents(t *testing.T) {
	a := newApp()
	tt := ui.NewTester(a.view, 1400, 920)
	if tt.HasText("Recents") || tt.HasText("Loading recents") {
		t.Fatalf("recents still visible: %v", tt.Texts())
	}

	// Once the runner filled the list, real rows appear; an untitled
	// conversation reads as a new chat row.
	a.scope().sessions = []agentSession{{id: "ses_z"}, {id: "ses_a", title: "Grep wiring", updated: 2}}
	a.scope().sessionsLoaded = true
	tt.Frame()
	if tt.HasText("Grep wiring") {
		t.Fatal("unpinned chat shown outside search")
	}
	a.togglePin(a.scope().sessions[1])
	tt.Frame()
	if !tt.HasText("Grep wiring") {
		t.Fatalf("pinned chat not listed: %v", tt.Texts())
	}
	if err := tt.Click("Grep wiring"); err != nil {
		t.Fatal(err)
	}
	if a.selected != "Grep wiring" || a.selectedSession != "ses_a" || len(a.blocks) != 0 {
		t.Fatalf("after choosing a session: %q %q %d", a.selected, a.selectedSession, len(a.blocks))
	}
	select {
	case job := <-a.jobs:
		if job.kind != "open" || job.sid != "ses_a" {
			t.Fatalf("queued %+v", job)
		}
	default:
		t.Fatal("opening did not queue a job")
	}
}

// TestSidebarScrollsWhenRowsOverflow keeps every pinned chat reachable
// once the list runs past the pane: the rows scroll under the New chat
// and search rows, which stay put.
func TestSidebarScrollsWhenRowsOverflow(t *testing.T) {
	a := newApp()
	a.scope().sessionsLoaded = true
	for i := range 40 {
		s := agentSession{id: fmt.Sprintf("ses_%02d", i), title: fmt.Sprintf("Pinned %02d", i)}
		a.scope().sessions = append(a.scope().sessions, s)
		a.togglePin(s)
	}
	tt := ui.NewTester(a.view, 1400, 920)
	top, ok := tt.Find("New chat")
	if !ok {
		t.Fatalf("the New chat row is missing: %v", tt.Texts())
	}
	if !tt.HasText("Pinned 00") {
		t.Fatalf("the first pinned chat is not listed: %v", tt.Texts())
	}
	if last, ok := tt.Find("Pinned 39"); !ok || last.W != 0 || last.H != 0 {
		t.Fatalf("the last pinned chat fits without a scroll: %+v", last)
	}

	// The wheel over the list reaches the tail it clipped away.
	tt.Scroll(200, 400, 0, 5000)
	last, ok := tt.Find("Pinned 39")
	if !ok || last.W == 0 || last.H == 0 {
		t.Fatalf("the scroll did not bring the last pinned chat in: %+v", last)
	}
	if err := tt.Click("Pinned 39"); err != nil {
		t.Fatalf("the scrolled row did not open: %v", err)
	}
	if a.selectedSession != "ses_39" {
		t.Fatalf("the scrolled row opened %q", a.selectedSession)
	}

	// What sits above the scroll keeps its place.
	if now, ok := tt.Find("New chat"); !ok || now.Y != top.Y {
		t.Fatalf("the New chat row moved from %v to %+v", top.Y, now)
	}
}

// TestAgentInterruptAndReply checks the two turn-control calls hit the
// paths the API documents and carry the decision in their body.
func TestAgentInterruptAndReply(t *testing.T) {
	mux := http.NewServeMux()
	got := make(chan string, 2)
	mux.HandleFunc("POST /api/session/ses_x/interrupt", func(w http.ResponseWriter, r *http.Request) {
		got <- "interrupt " + r.URL.Path
		w.Write([]byte(`{"interrupted":true}`))
	})
	mux.HandleFunc("POST /api/session/ses_x/permission/per_1/reply", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Decision string `json:"decision"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		got <- "reply " + r.URL.Path + " " + in.Decision
		w.Write([]byte(`{"data":true}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	a := &agent{base: srv.URL + "/api", pw: "test", http: srv.Client()}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := a.interrupt(ctx, "ses_x"); err != nil {
		t.Fatal(err)
	}
	if err := a.replyPermission(ctx, "ses_x", "per_1", "always"); err != nil {
		t.Fatal(err)
	}
	if one := <-got; one != "interrupt /api/session/ses_x/interrupt" {
		t.Fatalf("first call %q", one)
	}
	if two := <-got; two != "reply /api/session/ses_x/permission/per_1/reply always" {
		t.Fatalf("second call %q", two)
	}
}

// TestApplyPermission turns one approval into a card, flips the busy
// state through a turn and answers the card away, the way the runner
// does once its reply lands.
func TestApplyPermission(t *testing.T) {
	a := newApp()
	upTo := len(a.blocks)
	step := func(typ, data string) {
		a.apply(agentEvent{Type: typ, Data: json.RawMessage(data)})
	}

	step("session.execution.started", `{"sessionID":"s"}`)
	if a.busySession != "s" {
		t.Fatalf("busy after start %q", a.busySession)
	}
	step("permission.asked", `{"sessionID":"s","id":"per_1","action":"shell",`+
		`"resources":["echo asked-probe"],"save":["echo *"]}`)
	if len(a.blocks) != upTo+1 || a.blocks[len(a.blocks)-1].kind != "permission" {
		t.Fatalf("card not added: %+v", a.blocks[len(a.blocks)-1])
	}
	card := a.blocks[len(a.blocks)-1]
	if card.key != "permission:per_1" || card.req == nil ||
		card.req.action != "shell" || card.req.session != "s" ||
		len(card.req.resources) != 1 || card.req.resources[0] != "echo asked-probe" {
		t.Fatalf("card payload %+v", card)
	}
	if _, ok := a.pending["per_1"]; !ok {
		t.Fatal("the request is not held as pending")
	}

	// Answering queues the runner job addressed to the request.
	a.answerPerm(card.req, "once")
	select {
	case job := <-a.jobs:
		if job.kind != "answer" || job.sid != "s" || job.request != "per_1" || job.decision != "once" {
			t.Fatalf("answer job %+v", job)
		}
	default:
		t.Fatal("answering did not queue a job")
	}

	// The reply landing takes the card back out.
	a.dropPerm("per_1")
	if len(a.blocks) != upTo {
		t.Fatalf("card still drawn: %+v", a.blocks)
	}
	if _, ok := a.pending["per_1"]; ok {
		t.Fatal("the request is still pending")
	}

	// Interrupting clears the busy state the stop button feeds on.
	step("session.execution.interrupted", `{"sessionID":"s"}`)
	if a.busySession != "" {
		t.Fatalf("busy after interrupt %q", a.busySession)
	}

	// Switching conversations forgets everything outstanding.
	step("session.execution.started", `{"sessionID":"s"}`)
	a.freshTranscript()
	if a.busySession != "" || len(a.pending) != 0 {
		t.Fatalf("stale turn state after switch: %q %d", a.busySession, len(a.pending))
	}
}

// TestStopAndAnswerButtons drives the stop morph and the approval card
// for real: the composer shows a stop button while a turn runs and
// clicking the card's pill answers it.
func TestStopAndAnswerButtons(t *testing.T) {
	a := newApp()
	a.busySession = "ses_z"
	tt := ui.NewTester(a.view, 1400, 920)
	if !tt.HasText("Stop the agent") {
		t.Fatalf("no stop button while busy: %v", tt.Texts())
	}
	if err := tt.Click("Stop the agent"); err != nil {
		t.Fatal(err)
	}
	select {
	case job := <-a.jobs:
		if job.kind != "stop" || job.sid != "ses_z" {
			t.Fatalf("stop job %+v", job)
		}
	default:
		t.Fatal("stopping did not queue a job")
	}

	// The approval card renders what the agent asked for and answers
	// through its pills.
	a.pending["per_5"] = &agentPerm{id: "per_5", session: "ses_z", action: "shell",
		resources: []string{"rm -rf /tmp/scratch"}, save: []string{"rm *"}}
	a.blocks = []block{{kind: "permission", key: "permission:per_5", req: a.pending["per_5"]}}
	a.selectedSession = "ses_seed"
	a.selected = "Approval turn"
	tt.Frame()
	if !tt.HasText("Approval needed — run a shell command") ||
		!tt.HasText("$ rm -rf /tmp/scratch") ||
		!tt.HasText("Always would remember rm *") {
		t.Fatalf("approval card not drawn: %v", tt.Texts())
	}
	if err := tt.Click("Allow once"); err != nil {
		t.Fatal(err)
	}
	select {
	case job := <-a.jobs:
		if job.kind != "answer" || job.sid != "ses_z" || job.request != "per_5" || job.decision != "once" {
			t.Fatalf("answer job %+v", job)
		}
	default:
		t.Fatal("the pill's click did not queue a job")
	}
}

// TestAgentSessionLocation checks a new conversation lands in the
// directory asked for and that the sidebar rows carry their folder.
func TestAgentSessionLocation(t *testing.T) {
	mux := http.NewServeMux()
	global := make(chan string, 1)
	mux.HandleFunc("POST /api/session", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Location struct {
				Directory string `json:"directory"`
			} `json:"location"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		global <- in.Location.Directory
		row := `{"data":{"id":"ses_new"}}`
		if in.Location.Directory != "" {
			row = fmt.Sprintf(`{"data":{"id":"ses_new","location":{"directory":%q}}}`,
				in.Location.Directory)
		}
		w.Write([]byte(row))
	})
	mux.HandleFunc("GET /api/session", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"id":"ses_far","title":"Far away",` +
			`"location":{"directory":"/home/you/far"},"time":{"created":1,"updated":9}}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	a := &agent{base: srv.URL + "/api", pw: "test", http: srv.Client()}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// An explicitly named folder goes into the create body.
	if _, _, err := a.createSession(ctx, "/home/you/far"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-global:
		if got != "/home/you/far" {
			t.Fatalf("create location %q", got)
		}
	default:
		t.Fatal("the create body never carried a location")
	}
	// No folder keeps the plain body the server's own directory needs.
	if _, _, err := a.createSession(ctx, ""); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-global:
		if got != "" {
			t.Fatalf("plain create carried %q", got)
		}
	default:
		t.Fatal("the second create never reached the server")
	}

	rows, next, err := a.listSessions(ctx, "", "", "")
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows %v %v", rows, err)
	}
	if next != "" || rows[0].dir != "/home/you/far" {
		t.Fatalf("row folder %q cursor %q", rows[0].dir, next)
	}
}

// TestExpandPath turns typed folders into the paths sessions sit in.
func TestExpandPath(t *testing.T) {
	t.Setenv("HOME", "/tmp/wada-home")
	if got, err := expandPath("~/notes"); err == nil {
		t.Fatalf("$HOME is not real: %v %v", got, err)
	}

	cwd, err := expandPath(".")
	if err != nil || cwd[len(cwd)-1] == '.' {
		t.Fatalf("current dir %q %v", cwd, err)
	}
	if _, err := expandPath("/does/not/exist/wada"); err == nil ||
		err.Error() != "that folder does not exist" {
		t.Fatalf("missing folder error %v", err)
	}
}

// TestStartChatInFolder prepares a draft in a folder: a path that is not
// there only notes itself, and a real folder joins the sidebar as a
// project without creating an empty server chat.
func TestStartChatInFolder(t *testing.T) {
	a := newApp()
	a.startChatIn("/does/not/exist/wada")
	if a.draftProject != "" || len(a.projects) != 0 {
		t.Fatalf("missing folder landed a draft: %q, %v", a.draftProject, a.projects)
	}
	if len(a.blocks) != 1 || a.blocks[0].kind != "note" ||
		!strings.Contains(a.blocks[0].text, "that folder does not exist") {
		t.Fatalf("missing folder left no note: %v", a.blocks)
	}
	select {
	case job := <-a.jobs:
		t.Fatalf("queued a job for a missing folder: %+v", job)
	default:
	}

	scratch := t.TempDir()
	a.startChatIn(scratch)
	want, err := filepath.EvalSymlinks(scratch)
	if err != nil {
		t.Fatal(err)
	}
	if a.chatDir() != want || a.selectedSession != "" {
		t.Fatalf("draft directory %q, want %q", a.chatDir(), want)
	}
	if len(a.projects) != 1 || a.projects[0] != want {
		t.Fatalf("folders after starting: %v", a.projects)
	}
	if len(a.blocks) != 0 {
		t.Fatalf("starting a chat left the old note: %v", a.blocks)
	}
	select {
	case job := <-a.jobs:
		t.Fatalf("created an empty chat: %+v", job)
	default:
	}
}

// TestAgentListOps checks the two sidebar mutations the runner makes:
// rename PATCHes the title and delete DELETEs the conversation.
func TestAgentListOps(t *testing.T) {
	mux := http.NewServeMux()
	seen := make(chan string, 2)
	mux.HandleFunc("PATCH /api/session/ses_r", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Title string `json:"title"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		seen <- "patch " + in.Title
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("DELETE /api/session/ses_r", func(w http.ResponseWriter, r *http.Request) {
		seen <- "delete"
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	a := &agent{base: srv.URL + "/api", pw: "test", http: srv.Client()}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := a.renameSession(ctx, "ses_r", "Fresh title"); err != nil {
		t.Fatal(err)
	}
	if err := a.deleteSession(ctx, "ses_r"); err != nil {
		t.Fatal(err)
	}
	if one := <-seen; one != "patch Fresh title" {
		t.Fatalf("first op %q", one)
	}
	if two := <-seen; two != "delete" {
		t.Fatalf("second op %q", two)
	}
}

// TestDedupeSessions folds later pages in without repeating rows.
func TestDedupeSessions(t *testing.T) {
	all := []agentSession{{id: "a"}, {id: "b"}}
	got := dedupeSessions(all, []agentSession{{id: "b"}, {id: "c"}})
	if len(got) != 3 || got[0].id != "a" || got[1].id != "b" || got[2].id != "c" {
		t.Fatalf("dedupe %+v", got)
	}
}

// TestSessionDialogs drives the rename and delete dialogs the Recents
// kebab opens: the pills queue the two list jobs or leave things be.
func TestSessionDialogs(t *testing.T) {
	a := newApp()
	a.scope().sessions = []agentSession{{id: "ses_r", title: "Grep wiring", dir: "/tmp"}}
	a.selectedSession = "ses_r"
	a.selected = "Grep wiring"
	tt := ui.NewTester(a.view, 1400, 920)

	// Rename: the dialog pre-fills the title; saving queues the job and
	// closes; an unedited save queues nothing.
	a.requestRename(a.scope().sessions[0])
	tt.Frame()
	if !tt.HasText("Rename this chat") || a.rename == nil || a.rename.draft != "Grep wiring" {
		t.Fatalf("rename dialog not drawn: %v", tt.Texts())
	}
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	select {
	case job := <-a.jobs:
		t.Fatalf("unchanged title queued %+v", job)
	default:
	}
	if a.rename != nil {
		t.Fatal("the rename dialog stays open")
	}
	a.rename = &renameState{open: true, sid: "ses_r", draft: "Sharper wiring"}
	tt.Frame()
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	select {
	case job := <-a.jobs:
		if job.kind != "rename" || job.sid != "ses_r" || job.text != "Sharper wiring" {
			t.Fatalf("rename job %+v", job)
		}
	default:
		t.Fatal("saving the rename did not queue a job")
	}

	// Delete: a stock title conflict stands in; the dialog says what
	// disappears and the pill queues it.
	a.confirmDelete(a.scope().sessions[0])
	tt.Frame()
	if !tt.HasText("Delete “Grep wiring”?") {
		t.Fatalf("confirm dialog not drawn: %v", tt.Texts())
	}
	if err := tt.Click("Delete"); err != nil {
		t.Fatal(err)
	}
	select {
	case job := <-a.jobs:
		if job.kind != "delete" || job.sid != "ses_r" {
			t.Fatalf("delete job %+v", job)
		}
	default:
		t.Fatal("confirming the delete did not queue a job")
	}
	if a.confirm != nil {
		t.Fatal("the confirm dialog stays open")
	}

	// Once the server answers, the row goes and the open chat blanks.
	a.dropSession("ses_r")
	if len(a.scope().sessions) != 0 || a.selectedSession != "" || len(a.blocks) != 0 {
		t.Fatalf("after drop: %d rows, session %q, %d blocks",
			len(a.scope().sessions), a.selectedSession, len(a.blocks))
	}
}

// Removing Recents also removes its pagination action.
func TestSidebarHasNoOlderChatsRow(t *testing.T) {
	a := newApp()
	a.scope().sessions = []agentSession{{id: "ses_a", title: "Grep wiring"}}
	a.scope().sessionsLoaded = true
	tt := ui.NewTester(a.view, 1400, 920)
	if tt.HasText("Show older chats") {
		t.Fatalf("older-chats row shows with nothing to load: %v", tt.Texts())
	}

	a.scope().nextCursor = "cur2"
	tt.Frame()
	if tt.HasText("Show older chats") {
		t.Fatal("recents pagination still visible")
	}
}

// TestBootSettle pins how the boot lands: the list is in, the newest
// conversation is adopted for real, and no session leaves a clean
// transcript where the mock used to sit.
func TestBootSettle(t *testing.T) {
	a := newApp()
	if len(a.blocks) != 0 || a.selected != "" {
		t.Fatalf("the boot starts on %d blocks, selected %q", len(a.blocks), a.selected)
	}
	a.settleBoot([]agentSession{{id: "ses_a", title: "Grep wiring"}}, "", nil)
	if !a.scope().sessionsLoaded || a.selectedSession != "ses_a" ||
		a.selected != "Grep wiring" || len(a.blocks) != 0 {
		t.Fatalf("boot settle: loaded %v session %q label %q blocks %d",
			a.scope().sessionsLoaded, a.selectedSession, a.selected, len(a.blocks))
	}
	select {
	case job := <-a.jobs:
		if job.kind != "open" || job.sid != "ses_a" {
			t.Fatalf("boot job %+v", job)
		}
	default:
		t.Fatal("the boot did not queue the adoption")
	}

	// A startup without a server lands empty, not on the mock.
	a2 := newApp()
	a2.settleBoot(nil, "", fmt.Errorf("not connected"))
	if len(a2.blocks) != 0 || !a2.scope().sessionsLoaded {
		t.Fatalf("failed boot: %d blocks, loaded %v", len(a2.blocks), a2.scope().sessionsLoaded)
	}

	// An empty store reads the same: nothing mock survives the boot.
	a3 := newApp()
	a3.settleBoot(nil, "", nil)
	if len(a3.blocks) != 0 || a3.selectedSession != "" {
		t.Fatalf("empty boot: session %q, %d blocks", a3.selectedSession, len(a3.blocks))
	}
}

// TestFirstSendClean keeps the demo transcript out of a conversation the
// user starts from a fresh view: the first words append to nothing else.
func TestFirstSendClean(t *testing.T) {
	a := newApp()
	a.draft = "hello"
	a.send()
	select {
	case job := <-a.jobs:
		if job.kind != "prompt" || job.sid != "" {
			t.Fatalf("first-send job %+v", job)
		}
	default:
		t.Fatal("the first send did not queue a job")
	}
	if len(a.blocks) != 1 || a.blocks[0].kind != "user" || a.blocks[0].text != "hello" {
		t.Fatalf("first send blocks %+v", a.blocks)
	}
}

// TestStartNewChat resets the view and the addressee, and creates no
// session on its own: one comes into being with the first words.
func TestStartNewChat(t *testing.T) {
	a := newApp()
	a.scope().sessions = []agentSession{{id: "ses_a", title: "Grep wiring"}}
	a.selectedSession = "ses_a"
	a.selected = "Grep wiring"
	a.blocks = []block{{kind: "text", text: "leftover"}}
	a.startNewChat()
	if a.selectedSession != "" || a.selected != "" || len(a.blocks) != 0 {
		t.Fatalf("after new chat: session %q label %q blocks %d",
			a.selectedSession, a.selected, len(a.blocks))
	}
	select {
	case job := <-a.jobs:
		t.Fatalf("new chat created something eagerly: %+v", job)
	default:
	}
}

// TestListModels reads the catalog the way the picker shows it: names,
// reasoning-effort lists and whether a model runs for nothing.
func TestListModels(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/model", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[` +
			`{"id":"longcat-2.5-preview-free","providerID":"opencode-go","name":"LongCat 2.5 Preview Free",` +
			`"variants":[],"cost":[{"input":0,"output":0}]},` +
			`{"id":"beam-free","providerID":"opencode-go","name":"Beam Free",` +
			`"variants":[{"id":"low"},{"id":"medium"},{"id":"max"}],` +
			`"cost":[{"input":0,"output":0},{"input":0,"output":0,"tier":{"size":200000}}]},` +
			`{"id":"minimax-m3","providerID":"opencode-go","name":"MiniMax-M3",` +
			`"variants":[{"id":"none"},{"id":"thinking"}],"cost":[{"input":0.3,"output":1.2}]}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	a := &agent{base: srv.URL + "/api", pw: "test", http: srv.Client()}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, err := a.listModels(ctx)
	if err != nil || len(rows) != 3 {
		t.Fatalf("models %v %v", rows, err)
	}
	if !rows[0].free || len(rows[0].variants) != 0 {
		t.Fatalf("longcat %+v", rows[0])
	}
	if !rows[1].free || len(rows[1].variants) != 3 || rows[1].variants[2] != "max" {
		t.Fatalf("beam %+v", rows[1])
	}
	if rows[2].free {
		t.Fatal("minimax priced but marked free")
	}
}

// TestSetModelBody carries the provider, the id and the reasoning effort
// the picker chose into the POST body the server takes.
func TestSetModelBody(t *testing.T) {
	mux := http.NewServeMux()
	got := make(chan string, 2)
	mux.HandleFunc("POST /api/session/ses_m/model", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Model struct {
				ID         string `json:"id"`
				ProviderID string `json:"providerID"`
				Variant    string `json:"variant"`
			} `json:"model"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		got <- in.Model.ProviderID + "/" + in.Model.ID + "/" + in.Model.Variant
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	a := &agent{base: srv.URL + "/api", pw: "test", http: srv.Client()}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := a.setModel(ctx, "ses_m", "opencode-go", "beam-free", "high"); err != nil {
		t.Fatal(err)
	}
	if one := <-got; one != "opencode-go/beam-free/high" {
		t.Fatalf("body carried %q", one)
	}
}

// TestApplyModelEvent folds a reasoning-effort switch into the sidebar
// row, keeping the picker's chips in step with the server.
func TestApplyModelEvent(t *testing.T) {
	a := newApp()
	a.scope().models = []agentModel{{providerID: "opencode-go", id: "beam-free",
		name: "Beam Free", variants: []string{"low", "medium", "high", "max"}, free: true}}
	a.scope().sessions = []agentSession{{id: "ses_z", title: "Weird turn",
		model: agentModelRef{providerID: "opencode-go", id: "beam-free"}}}
	a.apply(agentEvent{Type: "session.model.selected",
		Data: json.RawMessage(`{"sessionID":"ses_z","model":{"id":"beam-free","providerID":"opencode-go","variant":"high"}}`)})
	if a.scope().sessions[0].model.variant != "high" ||
		a.scope().sessions[0].model.id != "beam-free" {
		t.Fatalf("row model %+v", a.scope().sessions[0].model)
	}
	name, variants := a.modelLabel(a.scope().sessions[0].model)
	if name != "Beam Free" || len(variants) == 0 {
		t.Fatalf("label %q has no variants; catalog %v", name, a.scope().models)
	}
}

// TestModelPicker drives the popover: the button carries the current
// name, the catalog lists free models first and closes the panel, the
// filter narrows it, and the effort chips switch the variant.
func TestModelPicker(t *testing.T) {
	a := newApp()
	a.scope().models = []agentModel{
		{providerID: "opencode-go", id: "minimax-m3", name: "MiniMax-M3", free: false},
		{providerID: "opencode-go", id: "beam-free", name: "Beam Free",
			variants: []string{"low", "medium", "high", "max"}, free: true},
		{providerID: "opencode-go", id: "longcat-2.5-preview-free", name: "LongCat 2.5 Preview Free", free: true},
	}
	a.scope().sessions = []agentSession{{id: "ses_z", title: "Free turn",
		model: agentModelRef{providerID: "opencode-go", id: "beam-free"}}}
	a.selectedSession = "ses_z"
	a.selected = "Free turn"
	// The catalog rows carry the fancy names, so the button reads
	// "Model Beam Free" and the effort chips sit ready beside the list.
	tt := ui.NewTester(a.view, 1400, 920)
	if !tt.HasText("Model Beam Free") {
		t.Fatalf("button label missing: %v", tt.Texts())
	}
	if err := tt.Click("Model Beam Free"); err != nil {
		t.Fatalf("opening the picker: %v", err)
	}
	if !a.pickerOpen {
		t.Fatal("the picker did not open")
	}
	tt.Frame()
	if !tt.HasText("MiniMax-M3") || !tt.HasText("opencode-go") {
		t.Fatalf("catalog not listed: %v", tt.Texts())
	}
	// The free rows stand above the paid one, each marked, names
	// alphabetically inside the free group.
	texts := tt.Texts()
	longcat, beam, paid := -1, -1, -1
	for i, s := range texts {
		switch s {
		case "LongCat 2.5 Preview Free":
			if longcat < 0 {
				longcat = i
			}
		case "Beam Free":
			if beam < 0 {
				beam = i
			}
		case "MiniMax-M3":
			if paid < 0 {
				paid = i
			}
		}
	}
	if beam >= longcat || longcat >= paid {
		t.Fatalf("order beam %d longcat %d paid %d", beam, longcat, paid)
	}

	// The filter narrows the rows to what matches.
	a.modelFilter = "beam"
	tt.Frame()
	if tt.HasText("MiniMax-M3") || !tt.HasText("Beam Free") {
		t.Fatalf("filter left noise: %v", tt.Texts())
	}
	if err := tt.Click("Pick Beam Free"); err != nil {
		t.Fatal(err)
	}
	select {
	case job := <-a.jobs:
		if job.kind != "model" || job.provider != "opencode-go" ||
			job.model != "beam-free" || job.variant != "" {
			t.Fatalf("pick job %+v", job)
		}
	default:
		t.Fatal("picking did not queue a job")
	}

	// The effort chips of the chat's model switch the variant.
	a.pickerOpen = true
	a.modelFilter = ""
	tt.Frame()
	if err := tt.Click("Reasoning high"); err != nil {
		t.Fatalf("chips missing: %v", err)
	}
	select {
	case job := <-a.jobs:
		if job.kind != "model" || job.model != "beam-free" || job.variant != "high" {
			t.Fatalf("variant job %+v", job)
		}
	default:
		t.Fatal("the chip did not queue a job")
	}
}

// TestModelPickerScrolls keeps the catalog inside the panel: the rows
// share the room with the search field and the effort chips through the
// scroll that grows, and the last rows only open from a scroll.
func TestModelPickerScrolls(t *testing.T) {
	a := newApp()
	models := []agentModel{}
	for i := range 28 {
		models = append(models, agentModel{providerID: "opencode-go",
			id: fmt.Sprintf("wada-%02d", i), name: fmt.Sprintf("Wada Model %02d", i), free: true})
	}
	models = append(models,
		agentModel{providerID: "opencode-go", id: "zed-9000", name: "Zed 9000 Max", free: false},
		agentModel{providerID: "opencode-go", id: "ado-1000", name: "Ado 1000 Pro New", free: false})
	a.scope().models = models
	a.scope().sessions = []agentSession{{id: "ses_z", title: "Turn",
		model: agentModelRef{providerID: "opencode-go", id: "wada-00"}}}
	a.selectedSession = "ses_z"
	a.selected = "Turn"
	tt := ui.NewTester(a.view, 1400, 920)
	if err := tt.Click("Model Wada Model 00"); err != nil {
		t.Fatalf("opening the picker: %v", err)
	}
	if !a.pickerOpen {
		t.Fatal("the picker did not open")
	}
	tt.Frame()

	// Every row that paints stays in the panel, above the model button it
	// hangs off: field, chips and the rounded box bound the drawn list,
	// and nothing spills over the composer.
	btn, ok := tt.Find("Model Wada Model 00")
	if !ok {
		t.Fatalf("model button missing: %v", tt.Texts())
	}
	row, ok := tt.Find("Pick Wada Model 00")
	if !ok {
		t.Fatalf("row missing: %v", tt.Texts())
	}
	rowX := row.X + row.W/2
	for _, name := range []string{"Pick Wada Model 00", "Wada Model 13", "Wada Model 27"} {
		r, ok := tt.Find(name)
		if !ok {
			t.Fatalf("row %q missing: %v", name, tt.Texts())
		}
		if r.W == 0 || r.H == 0 {
			continue // scrolled out of view: clipped away, not painted
		}
		if r.Y < 4 || r.Y+r.H > btn.Y-4 {
			t.Fatalf("row %q paints past the panel: %.1f..%.1f of 4..%.1f",
				name, r.Y, r.Y+r.H, btn.Y-4)
		}
	}

	// The tail of the list sits under the bottom of the panel until a
	// scroll brings it up: its rows clip to nothing, and nothing they
	// could not paint picks a model.
	zed, ok := tt.Find("Zed 9000 Max")
	if !ok || zed.W != 0 || zed.H != 0 {
		t.Fatalf("the last paid row is visible without a scroll: %+v", zed)
	}
	tt.Scroll(rowX, btn.Y-220, 0, 5000)
	zed, ok = tt.Find("Zed 9000 Max")
	if !ok || zed.W == 0 || zed.H == 0 || zed.Y < 4 || zed.Y+zed.H > btn.Y-4 {
		t.Fatalf("the scroll did not bring the tail in: %+v of 4..%.1f", zed, btn.Y-4)
	}
	if err := tt.Click("Zed 9000 Max"); err != nil {
		t.Fatalf("the scrolled tail did not open a row: %v", err)
	}
	if a.pickerOpen {
		t.Fatal("the picker stayed open")
	}
	select {
	case job := <-a.jobs:
		if job.kind != "model" || job.model != "zed-9000" || job.variant != "" {
			t.Fatalf("pick job %+v", job)
		}
	default:
		t.Fatal("picking did not queue a job")
	}

	// A filter that nothing matches says so inside the panel.
	a.pickerOpen = true
	a.modelFilter = "no such model"
	tt.Frame()
	if !tt.HasText("No models in the catalog match.") {
		t.Fatalf("empty note missing: %v", tt.Texts())
	}
	r, ok := tt.Find("No models in the catalog match.")
	if !ok || r.W == 0 || r.H == 0 || r.Y < 4 || r.Y+r.H > btn.Y-4 {
		t.Fatalf("empty note paints past the panel: %+v of 4..%.1f", r, btn.Y-4)
	}
}

// TestLiveAgent exercises the whole pipe against a real opencode: spawn,
// readiness, auth, one prompt, the streamed answer, then the sidebar list
// and restore of what was stored. Opt in by hand:
//
//	WADA_LIVE_AGENT=1 go test -run TestLiveAgent ./...
//
// TestAttachFile quotes workspace files into the draft: relative paths
// ride the chat's folder, marks de-duplicate and misses stay in the
// dialog.
func TestAttachFile(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "sample.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	a := newApp()
	a.scope().sessionsLoaded = true
	a.scope().sessions = []agentSession{{id: "ses_z", title: "Turn", dir: tmp}}
	a.selectedSession = "ses_z"

	// A missing file keeps the dialog open with a note.
	a.attach = &attachState{open: true, draft: "nothere.go"}
	a.attachFile()
	if a.attach == nil || a.attach.note == "" {
		t.Fatalf("missing file %+v", a)
	}

	// A real file lands fenced in the draft, the dialog closed.
	a.attach.draft = "sample.go"
	a.attachFile()
	if a.attach != nil {
		t.Fatalf("attach state %+v", a)
	}
	mark := "(file: " + filepath.Join(tmp, "sample.go") + ")"
	if !strings.Contains(a.draft, mark) || !strings.Contains(a.draft, "```go") ||
		!strings.Contains(a.draft, "package main") {
		t.Fatalf("draft %q", a.draft)
	}

	// Asking again does not repeat it; a folder refuses politely.
	a.attach = &attachState{open: true, draft: "sample.go"}
	a.attachFile()
	if !strings.Contains(a.attach.note, "already") {
		t.Fatalf("note %q", a.attach.note)
	}
	a.attach.draft, a.draft = ".", ""
	a.attachFile()
	if !strings.Contains(a.attach.note, "not a file") {
		t.Fatalf("folder note %q", a.attach.note)
	}
}

// TestRemoteAttachFile quotes server files into the draft of a remote
// chat: relative names ride the chat's folder, the mark names the file
// where the server keeps it, misses stay in the dialog.
func TestRemoteAttachFile(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/fs/read/docs/readme.md", func(w http.ResponseWriter, r *http.Request) {
		if q := r.URL.Query().Get("location[directory]"); q != "/srv/app" {
			t.Errorf("the file asked against %q", r.URL.RawQuery)
		}
		w.Write([]byte("from the server\n"))
	})
	mux.HandleFunc("GET /api/fs/read/missing.md", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"_tag":"FileNotFoundError","message":"File not found: missing.md"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	a := newApp()
	a.servers = append(a.servers, serverConn{id: "srv-box", name: "Build box", url: srv.URL, password: "test"})
	a.activeServer = "srv-box"
	a.scope().sessionsLoaded = true
	a.scope().sessions = []agentSession{{id: "ses_r", title: "Remote turn", dir: "/srv/app"}}
	a.selectedSession = "ses_r"

	// A file that is not there keeps the dialog open with a note.
	a.attach = &attachState{open: true, draft: "missing.md"}
	a.attachFile()
	if a.attach == nil || a.attach.note != "that file is not there" {
		t.Fatalf("missing file %+v", a.attach)
	}

	// A real file lands fenced in the draft, the dialog closed, the mark
	// naming the server folder.
	a.attach.draft = "docs/readme.md"
	a.attachFile()
	if a.attach != nil {
		t.Fatalf("attach state %+v", a)
	}
	mark := "(file: " + filepath.Join("/srv/app", "docs/readme.md") + ")"
	if !strings.Contains(a.draft, mark) || !strings.Contains(a.draft, "from the server") ||
		!strings.Contains(a.draft, "```md") {
		t.Fatalf("draft %q", a.draft)
	}

	// An absolute name at home in the folder reads through the same call.
	a.attach = &attachState{open: true, draft: "/srv/app/docs/readme.md"}
	a.attachFile()
	if a.attach == nil || !strings.Contains(a.attach.note, "already") {
		t.Fatalf("dedup missed the server folder: %+v", a.attach)
	}
}

// TestRemotePolicyLevelAndWrite reads the shell level of a server
// workspace and moves it like a local folder's rule would: the config
// documents name the project's file, the write carries the rule in and
// out through its file API, and the config's other keys stand untouched.
func TestRemotePolicyLevelAndWrite(t *testing.T) {
	state := map[string]any{"shell": "zsh"} // the project's own config
	var docs []byte
	flush := func() {
		raw, _ := json.Marshal([]agentConfigEntry{
			{Type: "document", Path: "/home/dev/.config/opencode/opencode.json",
				Info: map[string]any{"shell": "bash"}},
			{Type: "directory", Path: "/home/dev/.config/opencode"},
			{Type: "document", Path: "/srv/app/opencode.json", Info: state},
		})
		docs = raw
	}
	flush()
	writes := make(chan []byte, 2)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		if q := r.URL.Query().Get("location[directory]"); q != "/srv/app" {
			t.Errorf("the config asked against %q", r.URL.RawQuery)
		}
		w.Write([]byte(docs))
	})
	mux.HandleFunc("POST /api/experimental/fs/write", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get("Content-Type") != "application/octet-stream" {
			t.Errorf("write went out as %q", r.Header.Get("Content-Type"))
		}
		writes <- body
		var doc map[string]any
		if json.Unmarshal(body, &doc) != nil {
			t.Errorf("the config wrote as broken JSON %q", string(body))
		}
		state = doc
		flush()
		w.Write([]byte(`{}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cfg := serverConn{url: srv.URL, password: "test"}

	level, err := remotePermissionLevel(context.Background(), cfg, "/srv/app")
	if err != nil || level != "full" {
		t.Fatalf("level %q %v", level, err)
	}

	level, err = remoteSetPermissionAsk(context.Background(), cfg, "/srv/app", true)
	if err != nil || level != "ask" {
		t.Fatalf("ask %q %v", level, err)
	}
	raw := <-writes
	var asked map[string]any
	if json.Unmarshal(raw, &asked) != nil {
		t.Fatalf("the written config %q", string(raw))
	}
	rules := permissionRules(asked)
	if len(rules) != 1 || asked["shell"] != "zsh" {
		t.Fatalf("the written config %q", string(raw))
	}
	if action, resource, effect, ok := ruleFields(rules[0]); !ok ||
		action != "shell" || resource != "*" || effect != "ask" {
		t.Fatalf("the written rule reads %v", rules[0])
	}
	level, err = remotePermissionLevel(context.Background(), cfg, "/srv/app")
	if err != nil || level != "ask" {
		t.Fatalf("level %q %v", level, err)
	}

	level, err = remoteSetPermissionAsk(context.Background(), cfg, "/srv/app", false)
	if err != nil || level != "full" {
		t.Fatalf("clear %q %v", level, err)
	}
	raw = <-writes
	var cleared map[string]any
	if json.Unmarshal(raw, &cleared) != nil {
		t.Fatalf("the cleared config %q", string(raw))
	}
	if len(permissionRules(cleared)) != 0 || cleared["permissions"] != nil || cleared["shell"] != "zsh" {
		t.Fatalf("the cleared config kept the rule: %q", string(raw))
	}
	level, err = remotePermissionLevel(context.Background(), cfg, "/srv/app")
	if err != nil || level != "full" {
		t.Fatalf("level %q %v", level, err)
	}

	// A rule the workspace already carries leaves the file alone.
	state = map[string]any{"permissions": []any{
		map[string]any{"action": "shell", "resource": "*", "effect": "ask"}}}
	flush()
	level, err = remoteSetPermissionAsk(context.Background(), cfg, "/srv/app", true)
	if err != nil || level != "ask" {
		t.Fatalf("no-op ask %q %v", level, err)
	}
	select {
	case raw := <-writes:
		t.Fatalf("a carried rule rewrote the file: %q", string(raw))
	default:
	}
}

// TestPermissionToggle drives the shell policy through the workspace's
// opencode.json: the ask rule lands in the V2 permissions array, the
// file's other keys stand untouched, and clearing takes only what the app
// put in.
func TestPermissionToggle(t *testing.T) {
	tmp := t.TempDir()
	cfg := "{\n  \"model\": \"opencode-go/minimax-m3\",\n  \"permission\": {\n    \"bash\": {\"*\": \"allow\"},\n    \"web\": {\"fetch\": \"deny\"}\n  }\n}"
	if err := os.WriteFile(filepath.Join(tmp, "opencode.json"), []byte(cfg), 0644); err != nil {
		t.Fatal(err)
	}
	level, err := permissionLevel(tmp)
	if err != nil || level != "full" {
		t.Fatalf("level %q %v", level, err)
	}
	if err := setPermissionAsk(tmp, true); err != nil {
		t.Fatal(err)
	}
	if level, _ = permissionLevel(tmp); level != "ask" {
		t.Fatalf("level %q after the ask rule", level)
	}
	var doc map[string]any
	raw, _ := os.ReadFile(filepath.Join(tmp, "opencode.json"))
	json.Unmarshal(raw, &doc)
	if doc["model"] != "opencode-go/minimax-m3" {
		t.Fatalf("other keys lost: %s", raw)
	}
	rules := permissionRules(doc)
	if len(rules) != 1 {
		t.Fatalf("rules %v", rules)
	}
	if action, resource, effect, ok := ruleFields(rules[0]); !ok ||
		action != "shell" || resource != "*" || effect != "ask" {
		t.Fatalf("the rule reads %v", rules[0])
	}
	// The V1 keys the file carried are the user's: the app only owns the
	// ask wildcard, and this file allows instead.
	if star, ok := v1BashStar(doc); !ok || star != "allow" {
		t.Fatalf("a user's V1 wildcard was taken: %s", raw)
	}

	// Clearing takes the app's rule only: the model, the V1 keys and the
	// web rule stay.
	if err := setPermissionAsk(tmp, false); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(filepath.Join(tmp, "opencode.json"))
	var kept map[string]any
	json.Unmarshal(raw, &kept)
	if kept["model"] == nil || !strings.Contains(string(raw), `"fetch"`) || !strings.Contains(string(raw), `"allow"`) {
		t.Fatalf("clear broke others: %s", raw)
	}
	if len(permissionRules(kept)) != 0 || kept["permissions"] != nil {
		t.Fatalf("the cleared config kept the rule: %s", raw)
	}
	if level, _ = permissionLevel(tmp); level != "full" {
		t.Fatalf("level %q after clearing", level)
	}

	// A folder without a config grows one; unreadable JSON refuses.
	empty := t.TempDir()
	if err := setPermissionAsk(empty, true); err != nil {
		t.Fatal(err)
	}
	if level, _ = permissionLevel(empty); level != "ask" {
		t.Fatalf("fresh config level %q", level)
	}
	bad := t.TempDir()
	os.WriteFile(filepath.Join(bad, "opencode.json"), []byte("{\n  // a thought\n}"), 0644)
	if _, err := permissionLevel(bad); err == nil {
		t.Fatal("an unreadable config read fine")
	}
	if err := setPermissionAsk(bad, true); err == nil {
		t.Fatal("the unreadable config was overwritten")
	}
}

// TestShellGateReadsBothSpellings keeps the pill honest about configs the
// app did not write: V2 rules in the order they claim, the V1 map an older
// app left, and what denies counting as a door held.
func TestShellGateReadsBothSpellings(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  map[string]any
		want string
	}{
		{"nothing said", map[string]any{}, "full"},
		{"unrelated rules", map[string]any{"permissions": []any{
			map[string]any{"action": "read", "resource": "*", "effect": "allow"}}}, "full"},
		{"a bare shell ask", map[string]any{"permissions": []any{
			map[string]any{"action": "shell", "resource": "*", "effect": "ask"}}}, "ask"},
		{"a bare shell deny", map[string]any{"permissions": []any{
			map[string]any{"action": "shell", "resource": "*", "effect": "deny"}}}, "ask"},
		{"a blanket ask", map[string]any{"permissions": []any{
			map[string]any{"action": "*", "resource": "*", "effect": "ask"}}}, "ask"},
		{"the last match wins", map[string]any{"permissions": []any{
			map[string]any{"action": "shell", "resource": "*", "effect": "ask"},
			map[string]any{"action": "shell", "resource": "*", "effect": "allow"}}}, "full"},
		{"a narrower rule is not the gate", map[string]any{"permissions": []any{
			map[string]any{"action": "shell", "resource": "git push *", "effect": "deny"}}}, "full"},
		{"the V1 wildcard", map[string]any{"permission": map[string]any{
			"bash": map[string]any{"*": "ask"}}}, "ask"},
		{"a V1 allow", map[string]any{"permission": map[string]any{
			"bash": map[string]any{"*": "allow"}}}, "full"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := shellGate(tc.doc); got != tc.want {
				t.Fatalf("gate %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSetShellGateTouchesItsOwn keeps the writing honest: the V2 ask rule
// lands and leaves, a V1 wildcard the app wrote migrates as it goes, and a
// deny the user set keeps standing through a full-access click.
func TestSetShellGateTouchesItsOwn(t *testing.T) {
	// A config an older app wrote: clearing the app's rule takes the V1
	// wildcard and its containers with it, and asking again writes the V2
	// form, leaving the keys the app does not own alone.
	doc := map[string]any{"shell": "zsh", "permission": map[string]any{
		"bash": map[string]any{"*": "ask"}, "web": map[string]any{"fetch": "deny"}}}
	if !setShellGate(doc, false) {
		t.Fatal("the V1 gate did not clear")
	}
	if _, ok := v1BashStar(doc); ok {
		t.Fatalf("the V1 wildcard stayed: %+v", doc)
	}
	if perm, _ := doc["permission"].(map[string]any); perm["web"] == nil {
		t.Fatalf("the V1 keys the app does not own were lost: %+v", doc)
	}
	if !setShellGate(doc, true) || shellGate(doc) != "ask" || doc["shell"] != "zsh" {
		t.Fatalf("the migrated config reads %+v", doc)
	}
	if rules := permissionRules(doc); len(rules) != 1 {
		t.Fatalf("the V2 rule did not land: %+v", rules)
	}
	if setShellGate(doc, true) {
		t.Fatal("a carried rule wrote again")
	}
	if !setShellGate(doc, false) || doc["permissions"] != nil {
		t.Fatalf("clearing the V2 rule left %+v", doc)
	}
	if setShellGate(doc, false) {
		t.Fatal("a second clear wrote again")
	}

	// A deny is the user's: full access stops the asking, it does not lift
	// a block, and the pill says as much.
	denied := map[string]any{"permissions": []any{
		map[string]any{"action": "shell", "resource": "*", "effect": "deny"}}}
	if setShellGate(denied, false) {
		t.Fatal("a deny-only config was rewritten")
	}
	if got := shellGate(denied); got != "ask" {
		t.Fatalf("a deny read %q", got)
	}
	if rules := permissionRules(denied); len(rules) != 1 {
		t.Fatalf("the deny was taken: %+v", rules)
	}
}

// TestPermissionPill shows the workspace's shell policy in the composer
// and moves it through the same file.
func TestPermissionPill(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "opencode.json"),
		[]byte(`{"permission":{"bash":{"*":"ask"}}}`), 0644)
	a := newApp()
	a.scope().sessionsLoaded = true
	a.scope().sessions = []agentSession{{id: "ses_z", title: "Turn", dir: tmp}}
	a.selectedSession = "ses_z"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates := make(chan policyResult, 8)
	go watchPolicy(ctx, a.policyDirs, a.policyWrites, func(result policyResult) { updates <- result })
	settle := func() {
		t.Helper()
		select {
		case result := <-updates:
			a.acceptPolicy(result)
		case <-time.After(2 * time.Second):
			t.Fatal("policy worker did not reply")
		}
	}
	tt := ui.NewTester(a.view, 1400, 920)
	settle()
	if !tt.HasText("Asks first") {
		t.Fatalf("pill missed the ask state: %v", tt.Texts())
	}
	a.setAsk(false)
	settle()
	tt.Frame()
	if !tt.HasText("Full access") || tt.HasText("Asks first") {
		t.Fatalf("pill stuck on ask: %v", tt.Texts())
	}
	a.setAsk(true)
	settle()
	if level, _ := permissionLevel(tmp); level != "ask" {
		t.Fatalf("the pill did not write the rule back: %q", level)
	}
}

// TestPermissionPillRemote drives the pill against a server's own shell
// level: the chat opens leaning ask, the server's answer renames it, and
// the menu carries the rule over the API like the local file would.
func TestPermissionPillRemote(t *testing.T) {
	state := map[string]any{"shell": "zsh"}
	var docs []byte
	flush := func() {
		docs, _ = json.Marshal([]agentConfigEntry{
			{Type: "document", Path: "/srv/app/opencode.json", Info: state},
		})
	}
	flush()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(docs))
	})
	mux.HandleFunc("POST /api/experimental/fs/write", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &state)
		flush()
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	a := newApp()
	a.servers = append(a.servers, serverConn{id: "srv-box", name: "Build box", url: srv.URL})
	a.activeServer = "srv-box"
	a.scope().sessionsLoaded = true
	a.scope().sessions = []agentSession{{id: "ses_r", title: "Remote turn", dir: "/srv/app"}}
	a.selectedSession = "ses_r"
	tt := ui.NewTester(a.view, 1400, 920)
	settle := func(want string) {
		t.Helper()
		for range 40 {
			tt.Frame()
			if tt.HasText(want) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("the pill never read %q: %v", want, tt.Texts())
	}
	settle("Full access")
	a.setAsk(true)
	settle("Asks first")
	rules := permissionRules(state)
	if len(rules) != 1 {
		t.Fatalf("the pill left no rule behind: %v", state)
	}
	if action, resource, effect, ok := ruleFields(rules[0]); !ok ||
		action != "shell" || resource != "*" || effect != "ask" {
		t.Fatalf("the pill left %v behind: %v", rules[0], state)
	}
}

// TestNewChatLiveRenders settles the complaint the old gate hid: words
// streaming into a fresh chat draw at once, though the server has not
// named it yet -- a new chat reads as an open one the moment it exists,
// and its auto-title lands into the open bar instead of waiting for a
// click.
// TestTopBarShowsWhatTheChatSpent keeps the spending beside the title:
// cents for a chat that has spent some, nothing for the free models the
// app runs on by default, and no chat's money on the settings page.
func TestTopBarShowsWhatTheChatSpent(t *testing.T) {
	a := newApp()
	a.scope().sessions = []agentSession{{id: "ses_paid", title: "Reviewing the wiring", cost: 0.3196}}
	a.selectedSession, a.selected = "ses_paid", "Reviewing the wiring"
	tt := ui.NewTester(a.view, 1400, 920)
	if !tt.HasText("Reviewing the wiring") || !tt.HasText("$0.32") {
		t.Fatalf("the spending is not beside the title: %v", tt.Texts())
	}

	a.scope().sessions = []agentSession{{id: "ses_free", title: "Reviewing the wiring"}}
	a.selectedSession = "ses_free"
	tt.Frame()
	if tt.HasText("$0.00") || tt.HasText("$0.0000") {
		t.Fatalf("a chat that spent nothing shows spending: %v", tt.Texts())
	}

	a.scope().sessions = []agentSession{{id: "ses_free", title: "Reviewing the wiring", cost: 1.5}}
	a.settingsOpen = true
	tt.Frame()
	if tt.HasText("$1.50") {
		t.Fatal("the settings page shows a chat's spending")
	}

	// Fractions of a cent keep their digits rather than rounding away.
	if got := spentLine(0.00042); got != "$0.0004" {
		t.Fatalf("a fraction of a cent reads %q", got)
	}
	if got := spentLine(0.3196); got != "$0.32" {
		t.Fatalf("cents read %q", got)
	}
}

func TestNewChatLiveRenders(t *testing.T) {
	a := newApp()
	// The state right after the runner opened a fresh conversation: a
	// real session id, no title yet.
	a.selectedSession = "ses_fresh"
	a.selected = "New chat"
	a.blocks = []block{{kind: "user", text: "run the checks"},
		{kind: "stream", text: "On it — this is half of an answer"}}
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Advance(time.Second) // the reply's words pace in
	if !tt.HasText("run the checks") || !tt.HasText("On it — this is half of an answer") {
		t.Fatalf("the fresh chat drew nothing: %v", tt.Texts())
	}

	// The auto-title folds onto the conversation on show.
	a.apply(agentEvent{Type: "session.renamed",
		Data: []byte(`{"sessionID":"ses_fresh","title":"Check runner"}`)})
	if a.selected != "Check runner" {
		t.Fatalf("the open chat did not take its rename: %q", a.selected)
	}
	tt.Frame()
	if !tt.HasText("run the checks") {
		t.Fatal("the rename cleared the transcript")
	}

	// A session the user is not looking at renames silently.
	a.apply(agentEvent{Type: "session.renamed",
		Data: []byte(`{"sessionID":"ses_fresh2","title":"Other"}`)})
	if a.selected != "Check runner" {
		t.Fatalf("another chat's rename stole the bar: %q", a.selected)
	}
}

// TestFinishCutTurns settles what a torn pipe leaves: the half-said
// answers stand as their words, asks and the spinning stop release.
func TestFinishCutTurns(t *testing.T) {
	a := newApp()
	a.selectedSession = "ses_z"
	a.busySession = "ses_z"
	a.blocks = []block{
		{kind: "text", text: "Earlier answer"},
		{kind: "stream", text: "half of an answer"},
		{kind: "stream", text: ""},
	}
	a.live["ses:part"] = 1
	a.pending["req_1"] = &agentPerm{}
	a.finishCutTurns()

	var text []string
	for _, b := range a.blocks {
		if b.kind == "stream" {
			t.Fatalf("a line still streams: %+v", a.blocks)
		}
		if b.kind == "text" {
			text = append(text, b.text)
		}
	}
	if strings.Join(text, "|") != "Earlier answer|half of an answer|(the answer was cut short)" {
		t.Fatalf("settled %q", text)
	}
	if a.busySession != "" || len(a.live) != 0 || len(a.pending) != 0 {
		t.Fatalf("state stuck: busy %q live %v pend %v", a.busySession, a.live, a.pending)
	}
}

func TestLiveAgent(t *testing.T) {
	if os.Getenv("WADA_LIVE_AGENT") == "" {
		t.Skip("set WADA_LIVE_AGENT=1 to run against a real opencode server")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	ag, err := startAgentFor(ctx, defaultLocalServer(), ".")
	if err != nil {
		t.Fatal(err)
	}
	defer ag.stop()

	evs := make(chan agentEvent, 256)
	go ag.events(ctx, func(ev agentEvent) { evs <- ev })

	sid, _, err := ag.createSession(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	// The real pipeline names the shared free model before the first
	// words run.
	prov, name := useFreeModel(t, ag, ctx, sid)
	if err := ag.prompt(ctx, sid, "Reply with exactly: PONG"); err != nil {
		t.Fatal(err)
	}
	var text string
	for {
		select {
		case ev := <-evs:
			switch ev.Type {
			case "session.text.ended":
				var d evTextData
				if json.Unmarshal(ev.Data, &d) == nil {
					text = d.Text
				}
			case "session.execution.succeeded", "session.execution.failed":
				if text != "PONG" {
					t.Fatalf("final answer %q, events so far ended", text)
				}
				// The turn shows up in the sidebar list, and the stored
				// messages restore into the same rows as live.
				ls, _, lerr := ag.listSessions(ctx, "", "", "")
				if lerr != nil {
					t.Fatal(lerr)
				}
				found := false
				for _, s := range ls {
					if s.id == sid {
						found = true
					}
				}
				if !found {
					t.Fatal("the new session is not in the stored list")
				}
				msgs, merr := ag.sessionMessages(ctx, sid)
				if merr != nil {
					t.Fatal(merr)
				}
				for _, m := range msgs {
					if m.Type == "assistant" {
						if m.Model.ProviderID != prov || m.Model.ID != name {
							t.Fatalf("the turn ran on %s/%s, wanted %s/%s",
								m.Model.ProviderID, m.Model.ID, prov, name)
						}
					}
				}
				blocks := restoreBlocks(msgs)
				if len(blocks) < 2 ||
					blocks[0].kind != "user" ||
					blocks[0].text != "Reply with exactly: PONG" ||
					blocks[len(blocks)-1].kind != "text" ||
					!strings.Contains(blocks[len(blocks)-1].text, "PONG") {
					t.Fatalf("restored transcript %+v", blocks)
				}
				return
			}
		case <-ctx.Done():
			t.Fatalf("no turn end in time; last answer %q", text)
		}
	}
}

// TestLivePermission drives a real approval round trip against opencode
// running in a scratch workspace set to ask before every shell command:
// the agent asks, the app answers "once" and the turn carries on. Opt in
// by hand:
//
//	WADA_LIVE_AGENT=1 go test -run TestLivePermission ./...
//
// TestLiveModel switches a live conversation onto the other free model,
// once plain and once with a reasoning effort, and checks both the
// selected event and the model the turn really ran on.
func TestLiveModel(t *testing.T) {
	if os.Getenv("WADA_LIVE_AGENT") == "" {
		t.Skip("set WADA_LIVE_AGENT=1 to run against a real opencode server")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	ag, err := startAgentFor(ctx, defaultLocalServer(), ".")
	if err != nil {
		t.Fatal(err)
	}
	defer ag.stop()

	evs := make(chan agentEvent, 256)
	go ag.events(ctx, func(ev agentEvent) { evs <- ev })

	sid, _, err := ag.createSession(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	useFreeModel(t, ag, ctx, sid)
	// Beam Free is the other zero-cost model, with reasoning efforts.
	if err := ag.setModel(ctx, sid, "opencode-go", "beam-free", ""); err != nil {
		t.Fatal(err)
	}
	if err := ag.setModel(ctx, sid, "opencode-go", "beam-free", "high"); err != nil {
		t.Fatal(err)
	}
	if err := ag.prompt(ctx, sid, "Reply with exactly: PONG"); err != nil {
		t.Fatal(err)
	}

	selected, sawHigh, done := 0, false, false
	for !done {
		select {
		case ev := <-evs:
			switch ev.Type {
			case "session.model.selected":
				var d struct {
					SessionID string `json:"sessionID"`
					Model     struct {
						ID         string `json:"id"`
						ProviderID string `json:"providerID"`
						Variant    string `json:"variant"`
					} `json:"model"`
				}
				if json.Unmarshal(ev.Data, &d) == nil && d.SessionID == sid &&
					d.Model.ProviderID == "opencode-go" && d.Model.ID == "beam-free" {
					selected++
					switch d.Model.Variant {
					case "high":
						sawHigh = true
					case "", "default":
						// the plain switch reports first
					default:
						t.Fatalf("the effort switch reported %q", d.Model.Variant)
					}
				}
			case "session.execution.succeeded", "session.execution.failed":
				done = true
			case "session.execution.started":
			}
		case <-ctx.Done():
			t.Fatalf("the turn never finished; %d selections seen", selected)
		}
	}
	if selected == 0 || !sawHigh {
		t.Fatalf("%d selections seen, high effort among them: %v", selected, sawHigh)
	}
	msgs, merr := ag.sessionMessages(ctx, sid)
	if merr != nil {
		t.Fatal(merr)
	}
	for _, m := range msgs {
		if m.Type == "assistant" {
			if m.Model.ID != "beam-free" || m.Model.Variant != "high" {
				t.Fatalf("the turn ran on %s/%s+%s", m.Model.ProviderID, m.Model.ID, m.Model.Variant)
			}
		}
	}
}

func TestLivePermission(t *testing.T) {
	if os.Getenv("WADA_LIVE_AGENT") == "" {
		t.Skip("set WADA_LIVE_AGENT=1 to run against a real opencode server")
	}
	dir := t.TempDir()
	cfg := `{"$schema":"https://opencode.ai/config.json","permission":{"bash":{"*":"ask"}}}`
	if err := os.WriteFile(filepath.Join(dir, "opencode.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	ag, err := startAgentFor(ctx, defaultLocalServer(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ag.stop()

	evs := make(chan agentEvent, 256)
	go ag.events(ctx, func(ev agentEvent) { evs <- ev })

	sid, _, err := ag.createSession(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	useFreeModel(t, ag, ctx, sid)
	if err := ag.prompt(ctx, sid, "Run this shell command and report exactly what it prints: echo asked-probe"); err != nil {
		t.Fatal(err)
	}

	var sawTool, answered bool
	for {
		select {
		case ev := <-evs:
			switch ev.Type {
			case "permission.asked":
				var d evPermissionData
				if json.Unmarshal(ev.Data, &d) != nil || d.SessionID != sid {
					continue
				}
				if len(d.Resources) == 0 || d.Action != "shell" {
					t.Fatalf("asked %+v", d)
				}
				if err := ag.replyPermission(ctx, sid, d.ID, "once"); err != nil {
					t.Fatal(err)
				}
				answered = true
			case "session.tool.success":
				sawTool = true
			case "session.execution.succeeded":
				if !answered || !sawTool {
					t.Fatalf("turn finished but unanswered %v / no tool ran %v", answered, sawTool)
				}
				// Nothing pending is left on the server either.
				raw, lerr := ag.call(ctx, "GET", "/permission/request", nil)
				if lerr != nil {
					t.Fatal(lerr)
				}
				var out struct {
					Data []struct {
						SessionID string `json:"sessionID"`
					} `json:"data"`
				}
				json.Unmarshal(raw, &out)
				for _, r := range out.Data {
					if r.SessionID == sid {
						t.Fatal("the request is still pending on the server")
					}
				}
				return
			case "session.execution.interrupted", "session.execution.failed":
				t.Fatalf("the turn ended early: %s", ev.Type)
			}
		case <-ctx.Done():
			t.Fatalf("no approval round trip in time; answered %v, tool %v", answered, sawTool)
		}
	}
}

// TestLiveLocation has a conversation work in a directory other than the
// server's own: a session made for a scratch folder runs its shell there.
// Opt in by hand:
//
//	WADA_LIVE_AGENT=1 go test -run TestLiveLocation ./...
func TestLiveLocation(t *testing.T) {
	if os.Getenv("WADA_LIVE_AGENT") == "" {
		t.Skip("set WADA_LIVE_AGENT=1 to run against a real opencode server")
	}
	dir, err := os.MkdirTemp("", "wada-loc")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	ag, err := startAgentFor(ctx, defaultLocalServer(), ".")
	if err != nil {
		t.Fatal(err)
	}
	defer ag.stop()

	evs := make(chan agentEvent, 256)
	go ag.events(ctx, func(ev agentEvent) { evs <- ev })

	sid, _, err := ag.createSession(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	useFreeModel(t, ag, ctx, sid)
	if err := ag.prompt(ctx, sid, "Run this shell command and report exactly what it prints: pwd"); err != nil {
		t.Fatal(err)
	}

	var out, last string
	var kinds []string
	var said string
	for {
		select {
		case ev := <-evs:
			last = ev.Type
			if !strings.HasSuffix(ev.Type, "updated") {
				kinds = append(kinds, ev.Type)
			}
			if ev.Type == "session.text.ended" {
				var d evTextData
				if json.Unmarshal(ev.Data, &d) == nil && d.SessionID == sid {
					said = d.Text
				}
			}
			if ev.Type == "session.execution.succeeded" && out == "" {
				t.Fatalf("the turn finished without running the shell\ntype storm: %v\nthe model said: %q", kinds, said)
			}
			if ev.Type != "session.tool.success" {
				continue
			}
			var d evToolData
			if err := json.Unmarshal(ev.Data, &d); err != nil || d.SessionID != sid {
				continue
			}
			for _, o := range textOf(d.Content) {
				if out == "" {
					out = o
				}
			}
		case <-ctx.Done():
			t.Fatalf("no tool output in time; last %q (%q)", out, last)
		}
		if out != "" {
			if !strings.Contains(out, filepath.Base(dir)) {
				t.Fatalf("the shell ran in %q, wanted %q", out, dir)
			}
			return
		}
	}
}

// TestLiveSessionOps renames a real conversation and deletes it, then
// checks the store agrees: the titled list carries the rename and the
// delete takes the session out of it for good. Opt in by hand:
//
//	WADA_LIVE_AGENT=1 go test -run TestLiveSessionOps ./...
func TestLiveSessionOps(t *testing.T) {
	if os.Getenv("WADA_LIVE_AGENT") == "" {
		t.Skip("set WADA_LIVE_AGENT=1 to run against a real opencode server")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ag, err := startAgentFor(ctx, defaultLocalServer(), ".")
	if err != nil {
		t.Fatal(err)
	}
	defer ag.stop()

	evs := make(chan agentEvent, 64)
	go ag.events(ctx, func(ev agentEvent) { evs <- ev })

	title := "Wada ops " + time.Now().Format("150405")
	sid, _, err := ag.createSession(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := ag.renameSession(ctx, sid, title); err != nil {
		t.Fatal(err)
	}

	renamed := false
	deadline := time.After(15 * time.Second)
	for !renamed {
		select {
		case ev := <-evs:
			if ev.Type != "session.renamed" {
				continue
			}
			var d struct {
				SessionID string `json:"sessionID"`
				Title     string `json:"title"`
			}
			if json.Unmarshal(ev.Data, &d) == nil &&
				d.SessionID == sid && d.Title == title {
				renamed = true
			}
		case <-deadline:
			t.Fatal("no rename event in time")
		}
	}

	// The store keeps the new title, and deleting removes the session.
	ls, _, lerr := ag.listSessions(ctx, "", "", "")
	if lerr != nil {
		t.Fatal(lerr)
	}
	for _, s := range ls {
		if s.id == sid && s.title != title {
			t.Fatalf("stored title %q, wanted %q", s.title, title)
		}
	}
	// The search over the store finds it by its title; the index trails
	// the rename briefly, so give it time rather than race it.
	matched := false
	indexWait := time.After(5 * time.Second)
	var rows int
	for !matched {
		found, _, serr := ag.listSessions(ctx, "", title, "")
		if serr != nil {
			t.Fatal(serr)
		}
		rows = len(found)
		for _, s := range found {
			if s.id == sid {
				matched = true
			}
		}
		if matched {
			break
		}
		select {
		case <-indexWait:
			t.Fatalf("the search did not return the renamed session; %d rows", rows)
		case <-time.After(300 * time.Millisecond):
		}
	}

	if err := ag.deleteSession(ctx, sid); err != nil {
		t.Fatal(err)
	}
	ls, _, lerr = ag.listSessions(ctx, "", "", "")
	if lerr != nil {
		t.Fatal(lerr)
	}
	for _, s := range ls {
		if s.id == sid {
			t.Fatal("the deleted session is still in the list")
		}
	}
	if _, gerr := ag.call(ctx, "GET", "/session/"+sid, nil); gerr == nil {
		t.Fatal("the deleted session still reads from the store")
	}
}
