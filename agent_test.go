package main

import (
	"context"
	"encoding/json"
	"fmt"
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

// TestAgentModel checks every new conversation is pointed at the app's
// free default, and that WADA_MODEL overrides it.
func TestAgentModel(t *testing.T) {
	prompts := make(chan string, 1)
	models := make(chan string, 1)
	a, _ := fakeAgent(t, prompts, models)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, _, err := a.createSession(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if err := a.pickModel(ctx, "ses_t1"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-models:
		if got != "opencode-go/longcat-2.5-preview-free" {
			t.Fatalf("model set to %q", got)
		}
	default:
		t.Fatal("the model request never reached the server")
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

	sessions, next, err := a.listSessions(ctx, "", "")
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

	rows, _, err := a.listSessions(ctx, "", "wiring")
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
	a.sessions = []agentSession{{id: "ses_z"}, {id: "ses_a", title: "Grep wiring", updated: 2}}
	a.sessionsLoaded = true
	tt.Frame()
	if tt.HasText("Grep wiring") {
		t.Fatal("unpinned chat shown outside search")
	}
	a.togglePin(a.sessions[1])
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

	rows, next, err := a.listSessions(ctx, "", "")
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
	a.sessions = []agentSession{{id: "ses_r", title: "Grep wiring", dir: "/tmp"}}
	a.selectedSession = "ses_r"
	a.selected = "Grep wiring"
	tt := ui.NewTester(a.view, 1400, 920)

	// Rename: the dialog pre-fills the title; saving queues the job and
	// closes; an unedited save queues nothing.
	a.requestRename(a.sessions[0])
	tt.Frame()
	if !tt.HasText("Rename this chat") || a.renameDraft != "Grep wiring" {
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
	if a.renameOpen {
		t.Fatal("the rename dialog stays open")
	}
	a.renameDraft = "Sharper wiring"
	a.renameOpen = true
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
	a.confirmDelete(a.sessions[0])
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
	if a.confirmOpen {
		t.Fatal("the confirm dialog stays open")
	}

	// Once the server answers, the row goes and the open chat blanks.
	a.dropSession("ses_r")
	if len(a.sessions) != 0 || a.selectedSession != "" || len(a.blocks) != 0 {
		t.Fatalf("after drop: %d rows, session %q, %d blocks",
			len(a.sessions), a.selectedSession, len(a.blocks))
	}
}

// Removing Recents also removes its pagination action.
func TestSidebarHasNoOlderChatsRow(t *testing.T) {
	a := newApp()
	a.sessions = []agentSession{{id: "ses_a", title: "Grep wiring"}}
	a.sessionsLoaded = true
	tt := ui.NewTester(a.view, 1400, 920)
	if tt.HasText("Show older chats") {
		t.Fatalf("older-chats row shows with nothing to load: %v", tt.Texts())
	}

	a.nextCursor = "cur2"
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
	if !a.sessionsLoaded || a.selectedSession != "ses_a" ||
		a.selected != "Grep wiring" || len(a.blocks) != 0 {
		t.Fatalf("boot settle: loaded %v session %q label %q blocks %d",
			a.sessionsLoaded, a.selectedSession, a.selected, len(a.blocks))
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
	if len(a2.blocks) != 0 || !a2.sessionsLoaded {
		t.Fatalf("failed boot: %d blocks, loaded %v", len(a2.blocks), a2.sessionsLoaded)
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
	a.sessions = []agentSession{{id: "ses_a", title: "Grep wiring"}}
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
	a.models = []agentModel{{providerID: "opencode-go", id: "beam-free",
		name: "Beam Free", variants: []string{"low", "medium", "high", "max"}, free: true}}
	a.sessions = []agentSession{{id: "ses_z", title: "Weird turn",
		model: agentModelRef{providerID: "opencode-go", id: "beam-free"}}}
	a.apply(agentEvent{Type: "session.model.selected",
		Data: json.RawMessage(`{"sessionID":"ses_z","model":{"id":"beam-free","providerID":"opencode-go","variant":"high"}}`)})
	if a.sessions[0].model.variant != "high" ||
		a.sessions[0].model.id != "beam-free" {
		t.Fatalf("row model %+v", a.sessions[0].model)
	}
	name, variants := a.modelLabel(a.sessions[0].model)
	if name != "Beam Free" || len(variants) == 0 {
		t.Fatalf("label %q has no variants; catalog %v", name, a.models)
	}
}

// TestModelPicker drives the popover: the button carries the current
// name, the catalog lists free models first and closes the panel, the
// filter narrows it, and the effort chips switch the variant.
func TestModelPicker(t *testing.T) {
	a := newApp()
	a.models = []agentModel{
		{providerID: "opencode-go", id: "minimax-m3", name: "MiniMax-M3", free: false},
		{providerID: "opencode-go", id: "beam-free", name: "Beam Free",
			variants: []string{"low", "medium", "high", "max"}, free: true},
		{providerID: "opencode-go", id: "longcat-2.5-preview-free", name: "LongCat 2.5 Preview Free", free: true},
	}
	a.sessions = []agentSession{{id: "ses_z", title: "Free turn",
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
	if !(beam < longcat && longcat < paid) {
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
	a.models = models
	a.sessions = []agentSession{{id: "ses_z", title: "Turn",
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
	a.sessionsLoaded = true
	a.sessions = []agentSession{{id: "ses_z", title: "Turn", dir: tmp}}
	a.selectedSession = "ses_z"

	// A missing file keeps the dialog open with a note.
	a.attachOpen = true
	a.attachDraft = "nothere.go"
	a.attachFile()
	if !a.attachOpen || a.attachNote == "" {
		t.Fatalf("missing file %+v", a)
	}

	// A real file lands fenced in the draft, the dialog closed.
	a.attachDraft = "sample.go"
	a.attachFile()
	if a.attachOpen || a.attachNote != "" {
		t.Fatalf("attach state %+v", a)
	}
	mark := "(file: " + filepath.Join(tmp, "sample.go") + ")"
	if !strings.Contains(a.draft, mark) || !strings.Contains(a.draft, "```go") ||
		!strings.Contains(a.draft, "package main") {
		t.Fatalf("draft %q", a.draft)
	}

	// Asking again does not repeat it; a folder refuses politely.
	a.attachOpen = true
	a.attachDraft = "sample.go"
	a.attachFile()
	if !strings.Contains(a.attachNote, "already") {
		t.Fatalf("note %q", a.attachNote)
	}
	a.attachDraft, a.draft, a.attachNote = ".", "", ""
	a.attachFile()
	if !strings.Contains(a.attachNote, "not a file") {
		t.Fatalf("folder note %q", a.attachNote)
	}
}

// TestPermissionToggle drives the shell policy through the workspace's
// opencode.json: the ask rule lands, the file's other keys stand
// untouched, and clearing takes only what the app put in.
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
	perm := doc["permission"].(map[string]any)
	bash := perm["bash"].(map[string]any)
	if bash["*"] != "ask" || bash["fetch"] != nil {
		t.Fatalf("bash rules %v", bash)
	}

	// Clearing takes the wildcard only: the web rule and the model stay.
	if err := setPermissionAsk(tmp, false); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(filepath.Join(tmp, "opencode.json"))
	var kept map[string]any
	json.Unmarshal(raw, &kept)
	if kept["model"] == nil || !strings.Contains(string(raw), `"fetch"`) {
		t.Fatalf("clear broke others: %s", raw)
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

// TestPermissionPill shows the workspace's shell policy in the composer
// and moves it through the same file.
func TestPermissionPill(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "opencode.json"),
		[]byte(`{"permission":{"bash":{"*":"ask"}}}`), 0644)
	a := newApp()
	a.sessionsLoaded = true
	a.sessions = []agentSession{{id: "ses_z", title: "Turn", dir: tmp}}
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

// TestNewChatLiveRenders settles the complaint the old gate hid: words
// streaming into a fresh chat draw at once, though the server has not
// named it yet -- a new chat reads as an open one the moment it exists,
// and its auto-title lands into the open bar instead of waiting for a
// click.
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

	ag, err := startAgent(ctx, ".")
	if err != nil {
		t.Fatal(err)
	}
	defer ag.cmd.Process.Kill()

	evs := make(chan agentEvent, 256)
	go ag.events(ctx, func(ev agentEvent) { evs <- ev })

	sid, _, err := ag.createSession(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	// The real pipeline names the shared free model before the first
	// words run.
	if err := ag.pickModel(ctx, sid); err != nil {
		t.Fatal(err)
	}
	prov, name := sessionModel()
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
				ls, _, lerr := ag.listSessions(ctx, "", "")
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

	ag, err := startAgent(ctx, ".")
	if err != nil {
		t.Fatal(err)
	}
	defer ag.cmd.Process.Kill()

	evs := make(chan agentEvent, 256)
	go ag.events(ctx, func(ev agentEvent) { evs <- ev })

	sid, _, err := ag.createSession(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := ag.pickModel(ctx, sid); err != nil {
		t.Fatal(err)
	}
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

	ag, err := startAgent(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ag.cmd.Process.Kill()

	evs := make(chan agentEvent, 256)
	go ag.events(ctx, func(ev agentEvent) { evs <- ev })

	sid, _, err := ag.createSession(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := ag.pickModel(ctx, sid); err != nil {
		t.Fatal(err)
	}
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

	ag, err := startAgent(ctx, ".")
	if err != nil {
		t.Fatal(err)
	}
	defer ag.cmd.Process.Kill()

	evs := make(chan agentEvent, 256)
	go ag.events(ctx, func(ev agentEvent) { evs <- ev })

	sid, _, err := ag.createSession(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := ag.pickModel(ctx, sid); err != nil {
		t.Fatal(err)
	}
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

	ag, err := startAgent(ctx, ".")
	if err != nil {
		t.Fatal(err)
	}
	defer ag.cmd.Process.Kill()

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
	ls, _, lerr := ag.listSessions(ctx, "", "")
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
		found, _, serr := ag.listSessions(ctx, "", title)
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
	ls, _, lerr = ag.listSessions(ctx, "", "")
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
