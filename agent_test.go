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

	sid, err := a.createSession(ctx, "")
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

	if _, err := a.createSession(ctx, ""); err != nil {
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

	sessions, next, err := a.listSessions(ctx, "")
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
	a2.expanded["activity:call_9"] = true
	tt := ui.NewTester(a2.view, 1400, 920)
	if !tt.HasText("$ go test ./wire") || !tt.HasText("ok") {
		t.Fatalf("stored tool detail not drawn: %v", tt.Texts())
	}
}

// TestSidebarRecents lists the real conversations under Recents and opens
// one with a click.
func TestSidebarRecents(t *testing.T) {
	a := newApp()
	tt := ui.NewTester(a.view, 1400, 920)
	if !tt.HasText("Loading recents") {
		t.Fatalf("recents spinner missing: %v", tt.Texts())
	}

	// Once the runner filled the list, real rows appear; an untitled
	// conversation reads as a new chat row.
	a.sessions = []agentSession{{id: "ses_z"}, {id: "ses_a", title: "Grep wiring", updated: 2}}
	a.sessionsLoaded = true
	tt = ui.NewTester(a.view, 1400, 920)
	if !tt.HasText("Grep wiring") || !tt.HasText("New chat") {
		t.Fatalf("recents not listed: %v", tt.Texts())
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
	tt = ui.NewTester(a.view, 1400, 920)
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
	if _, err := a.createSession(ctx, "/home/you/far"); err != nil {
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
	if _, err := a.createSession(ctx, ""); err != nil {
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

	rows, next, err := a.listSessions(ctx, "")
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

// TestFolderDialog drives the folder chip and dialog: the chip shows
// where the chat works, the dialog queues a fresh chat in a typed folder
// and turns away from folders that are not there.
func TestFolderDialog(t *testing.T) {
	a := newApp()
	tt := ui.NewTester(a.view, 1400, 920)
	mark := dirBase(workdir())
	if !tt.HasText(mark) {
		t.Fatalf("no folder chip label: %v", tt.Texts())
	}
	if err := tt.Click(mark); err != nil {
		t.Fatalf("clicking the folder chip: %v", err)
	}
	if !a.dirOpen {
		t.Fatal("the chip did not open the dialog")
	}
	tt = ui.NewTester(a.view, 1400, 920)
	if !tt.HasText("Start a chat in a folder") {
		t.Fatalf("dialog not drawn: %v", tt.Texts())
	}

	// A folder that is not there leaves a note and queues nothing.
	a.dirDraft = "/does/not/exist/wada"
	if err := tt.Click("Start chat here"); err != nil {
		t.Fatal(err)
	}
	if a.dirNote != "that folder does not exist" {
		t.Fatalf("note %q", a.dirNote)
	}
	select {
	case job := <-a.jobs:
		t.Fatalf("queued a job for a missing folder: %+v", job)
	default:
	}

	// A real folder queues the new chat and closes the dialog.
	scratch := t.TempDir()
	a.dirDraft = scratch
	if err := tt.Click("Start chat here"); err != nil {
		t.Fatal(err)
	}
	select {
	case job := <-a.jobs:
		if job.kind != "new" || job.dir != scratch {
			t.Fatalf("new-chat job %+v", job)
		}
	default:
		t.Fatal("starting a chat here did not queue a job")
	}
	if a.dirOpen {
		t.Fatal("the dialog stays open after starting")
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
	tt = ui.NewTester(a.view, 1400, 920)
	if !tt.HasText("Rename this chat") || !tt.HasText("Grep wiring") {
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
	tt = ui.NewTester(a.view, 1400, 920)
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
	tt = ui.NewTester(a.view, 1400, 920)
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

// TestOlderChatsRow offers the next page of conversations when the list
// has one and queues it from the click.
func TestOlderChatsRow(t *testing.T) {
	a := newApp()
	a.sessions = []agentSession{{id: "ses_a", title: "Grep wiring"}}
	a.sessionsLoaded = true
	tt := ui.NewTester(a.view, 1400, 920)
	if tt.HasText("Show older chats") {
		t.Fatalf("older-chats row shows with nothing to load: %v", tt.Texts())
	}

	a.nextCursor = "cur2"
	tt = ui.NewTester(a.view, 1400, 920)
	if !tt.HasText("Show older chats") {
		t.Fatalf("older-chats row missing: %v", tt.Texts())
	}
	if err := tt.Click("Show older chats"); err != nil {
		t.Fatal(err)
	}
	select {
	case job := <-a.jobs:
		if job.kind != "more" || job.cursor != "cur2" {
			t.Fatalf("more job %+v", job)
		}
	default:
		t.Fatal("the older-chats row did not queue a job")
	}
}

// TestBootSettle pins how the boot lands: the list is in, the newest
// conversation is adopted for real, and no session leaves a clean
// transcript where the mock used to sit.
func TestBootSettle(t *testing.T) {
	a := newApp()
	if len(a.blocks) == 0 {
		t.Fatal("the boot starts on mock content")
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

// TestLiveAgent exercises the whole pipe against a real opencode: spawn,
// readiness, auth, one prompt, the streamed answer, then the sidebar list
// and restore of what was stored. Opt in by hand:
//
//	WADA_LIVE_AGENT=1 go test -run TestLiveAgent ./...
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

	sid, err := ag.createSession(ctx, "")
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
				ls, _, lerr := ag.listSessions(ctx, "")
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

	sid, err := ag.createSession(ctx, "")
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

	sid, err := ag.createSession(ctx, dir)
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
	sid, err := ag.createSession(ctx, "")
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
	ls, _, lerr := ag.listSessions(ctx, "")
	if lerr != nil {
		t.Fatal(lerr)
	}
	for _, s := range ls {
		if s.id == sid && s.title != title {
			t.Fatalf("stored title %q, wanted %q", s.title, title)
		}
	}
	if err := ag.deleteSession(ctx, sid); err != nil {
		t.Fatal(err)
	}
	ls, _, lerr = ag.listSessions(ctx, "")
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
