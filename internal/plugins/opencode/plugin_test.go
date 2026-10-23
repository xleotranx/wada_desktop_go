package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/plugintest"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
	"github.com/xleotranx/wada-kit/kittest"
)

// fakeServer is a minimal OpenCode server: /info, the event stream and the
// routes the tests call. Each stream connection receives what Push sends.
type fakeServer struct {
	*httptest.Server
	password string

	mu       sync.Mutex
	requests []string
	bodies   map[string]string
	streams  chan chan string // one per open event stream
	refuse   bool
	linked   map[string]bool // providers with a credential
	polls    int             // sign-in status checks
	reads    int             // provider list reads
}

func newFakeServer(t *testing.T) *fakeServer {
	f := &fakeServer{password: "pw", bodies: map[string]string{}, streams: make(chan chan string, 8),
		linked: map[string]bool{"opencode-go": true}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeServer) serve(w http.ResponseWriter, r *http.Request) {
	_, pw, _ := r.BasicAuth()
	f.mu.Lock()
	refuse := f.refuse
	f.mu.Unlock()
	if pw != f.password || refuse {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	body, _ := io.ReadAll(r.Body)
	route := r.Method + " " + strings.TrimPrefix(r.URL.RequestURI(), "/api")
	f.mu.Lock()
	if route != "GET /info" && route != "GET /event" && route != "GET /integration" {
		f.requests = append(f.requests, route)
		f.bodies[route] = strings.TrimSpace(string(body))
	}
	f.mu.Unlock()
	if f.serveProviders(w, route) {
		return
	}
	switch {
	case route == "POST /session/ses-9/prompt" && strings.Contains(string(body), "reject-contract-send"):
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"message":"rejected"}`)
	case route == "GET /info":
		fmt.Fprint(w, `{"data":{"version":"2.0.0"}}`)
	case route == "GET /event":
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		lines := make(chan string, 64)
		f.streams <- lines
		for {
			select {
			case <-r.Context().Done():
				return
			case line, ok := <-lines:
				if !ok {
					return
				}
				fmt.Fprintf(w, "data: %s\n\n", line)
				w.(http.Flusher).Flush()
			}
		}
	case strings.HasPrefix(route, "GET /session?"):
		fmt.Fprint(w, `{"data":[`+
			`{"id":"old","title":"Old","time":{"created":1}},`+
			`{"id":"child","parentID":"new","time":{"updated":9}},`+
			`{"id":"new","title":"New","location":{"directory":"/w/x/"},"time":{"updated":5}}],"cursor":{"next":"c2"}}`)
	case route == "POST /session":
		fmt.Fprint(w, `{"data":{"id":"ses-9","location":{"directory":"/w"},"model":{"providerID":"p","id":"default"}}}`)
	case strings.HasPrefix(route, "GET /fs/find?"):
		// An empty path names nothing and is dropped; "cmd/" loses its slash.
		fmt.Fprint(w, `{"location":{"directory":"/w"},"data":[{"path":"cmd/","type":"directory"},{"path":"","type":"directory"},{"path":"cmd/main.go","type":"file"},{"path":"README.md","type":"file"}]}`)
	case strings.HasPrefix(route, "GET /fs/read/docs/a%20b.md"):
		w.Header().Set("Content-Type", "text/markdown")
		fmt.Fprint(w, "# hi")
	case strings.HasPrefix(route, "GET /fs/read/"): // what a server without the route answers
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<!doctype html>")
	case route == "GET /session/ses-9/diff":
		fmt.Fprint(w, `{"data":[`+
			`{"file":"main.go","patch":"@@ -1 +1 @@\n-a\n+b\n","additions":1,"deletions":1,"status":"modified"},`+
			`{"file":"new.go","additions":4,"deletions":0,"status":"added"},{"additions":1}]}`)
	case route == "GET /model":
		fmt.Fprint(w, `{"data":[`+
			`{"id":"big","providerID":"p","name":"Big","cost":[{"input":1,"output":2}],"limit":{"context":200000,"output":8000}},`+
			`{"id":"free","providerID":"p","name":"Free","cost":[{"input":0,"output":0}],"limit":{"output":1}}]}`)
	case route == "GET /agent":
		fmt.Fprint(w, `{"data":[{"id":"build","name":"Build","mode":"primary"},{"id":"sub","mode":"subagent"},{"id":"secret","mode":"primary","hidden":true}]}`)
	case route == "POST /session/ses-9/model" && strings.Contains(string(body), "broken"):
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"_tag":"ModelNotFound","message":"no such model"}`)
	default:
		fmt.Fprint(w, `{"data":{}}`)
	}
}

// serveProviders answers the integration and credential routes.
func (f *fakeServer) serveProviders(w http.ResponseWriter, route string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch route {
	case "GET /integration":
		f.reads++
		var out []string
		for _, pr := range []struct{ id, name, methods string }{
			{"anthropic", "Anthropic", `[{"type":"key"},{"type":"env","names":["ANTHROPIC_API_KEY"]}]`},
			{"azure", "Azure", `[{"type":"key","label":"API key","form":[{"key":"resourceName","type":"string","title":"Resource name","required":true}]}]`},
			{"openai", "OpenAI", `[{"type":"key"},{"type":"oauth","id":"browser","label":"Sign in with ChatGPT"}]`},
			{"opencode-go", "OpenCode Go", `[{"type":"key"}]`},
		} {
			conns := `[]`
			if f.linked[pr.id] {
				conns = `[{"type":"credential","id":"cred-` + pr.id + `","label":"API key","method":"key"}]`
			}
			out = append(out, `{"id":"`+pr.id+`","name":"`+pr.name+`","methods":`+pr.methods+`,"connections":`+conns+`}`)
		}
		fmt.Fprint(w, `{"location":{"directory":"/w"},"data":[`+strings.Join(out, ",")+`]}`)
	case "POST /integration/anthropic/connect/key", "POST /integration/azure/connect/key":
		f.linked[strings.Split(route, "/")[2]] = true
		w.WriteHeader(http.StatusNoContent)
	case "POST /integration/openai/connect/oauth":
		fmt.Fprint(w, `{"location":{"directory":"/w"},"data":{"attemptID":"att-1","url":"https://auth.example/start","instructions":"Finish in your browser","mode":"auto","time":{"created":1,"expires":2}}}`)
	case "GET /integration/openai/connect/oauth/att-1":
		f.polls++
		status := "pending"
		if f.polls > 1 {
			status, f.linked["openai"] = "complete", true
		}
		fmt.Fprint(w, `{"location":{"directory":"/w"},"data":{"status":"`+status+`","time":{"created":1,"expires":2}}}`)
	case "DELETE /credential/cred-opencode-go":
		f.linked["opencode-go"] = false
		w.WriteHeader(http.StatusNoContent)
	default:
		return false
	}
	return true
}

// push sends events to the newest open stream.
func (f *fakeServer) stream(t *testing.T) chan string {
	t.Helper()
	select {
	case s := <-f.streams:
		return s
	case <-time.After(2 * time.Second):
		t.Fatal("no event stream opened")
		return nil
	}
}

func (f *fakeServer) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.requests)
}

// providerReads counts the GET /integration requests, which are otherwise
// left out of seen so tests can name the mutations alone.
func (f *fakeServer) providerReads() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads
}

func (f *fakeServer) body(route string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[route]
}

func TestBackendContract(t *testing.T) {
	f := newFakeServer(t)
	h, _ := start(t, f)
	plugintest.CheckBackend(t, plugintest.BackendCase{Harness: h, PluginID: "opencode", Session: "ses-9",
		RejectSend: func() (string, func() int) {
			return "reject-contract-send", func() int {
				n := 0
				for _, call := range f.seen() {
					if call == "POST /session/ses-9/prompt" {
						n++
					}
				}
				return n
			}
		},
	})
}

// start runs the plugin in a kernel configured for f.
func start(t *testing.T, f *fakeServer) (*kerneltest.Harness, *Plugin) {
	t.Helper()
	store := kerneltest.NewStore()
	raw, _ := json.Marshal(Connection{Remote: true, URL: f.URL, Password: f.password})
	store.Save(context.Background(), []kernel.Value{{Namespace: "opencode", Key: "connection", JSON: raw}})
	var p *Plugin
	h := kerneltest.Start(t, kerneltest.Config{Store: store}, (&kittest.Host{}).New, kerneltest.Keep(&p, func() *Plugin {
		return &Plugin{after: func(time.Duration) <-chan time.Time {
			ch := make(chan time.Time, 1)
			ch <- time.Now()
			return ch
		}}
	}))
	return h, p
}

// waitFor runs posted work until cond holds.
func waitFor(t *testing.T, h *kerneltest.Harness, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		h.Flush()
		time.Sleep(time.Millisecond)
	}
}

func service(t *testing.T, h *kerneltest.Harness) agent.Service {
	t.Helper()
	s, ok := kernel.Use(h, agent.ServiceKey)
	if !ok {
		t.Fatal("no agent service")
	}
	return s
}

// call runs a blocking Service call off the test goroutine, as kernel.Go
// work would, while the harness keeps running posts.
func call[T any](t *testing.T, h *kerneltest.Harness, fn func(context.Context) (T, error)) (T, error) {
	t.Helper()
	type result struct {
		v   T
		err error
	}
	done := make(chan result, 1)
	go func() { v, err := fn(context.Background()); done <- result{v, err} }()
	for {
		select {
		case r := <-done:
			return r.v, r.err
		case <-time.After(time.Millisecond):
			h.Flush()
		}
	}
}

func TestConnectsAndEmitsEventBatchesInOrder(t *testing.T) {
	f := newFakeServer(t)
	base := attempts.Load()
	h, p := start(t, f)
	var states []agent.ConnState
	kernel.On(h, agent.StatusChanged, func(s agent.Status) { states = append(states, s.State) })
	var got []agent.Event
	kernel.On(h, agent.Events, func(batch []agent.Event) { got = append(got, batch...) })

	lines := f.stream(t)
	waitFor(t, h, "ready", func() bool { return p.Status().State == agent.Ready })
	if st := p.Status(); st.Version != "2.0.0" || st.Endpoint != f.URL || st.Attempt != base+1 {
		t.Fatalf("status %+v", st)
	}
	if !slices.Equal(states, []agent.ConnState{agent.Connecting, agent.Ready}) && !slices.Equal(states, []agent.ConnState{agent.Ready}) {
		t.Fatalf("states %v", states)
	}
	for i := range 100 {
		lines <- fmt.Sprintf(`{"type":"session.text.delta","data":{"sessionID":"s","assistantMessageID":"m","ordinal":0,"delta":"%d"}}`, i)
	}
	lines <- `{"type":"future.kind","data":{}}`
	waitFor(t, h, "events", func() bool { return len(got) == 100 })
	for i, ev := range got {
		if ev.Text.Delta != fmt.Sprint(i) {
			t.Fatalf("event %d out of order: %q", i, ev.Text.Delta)
		}
	}
}

func TestServerCatalogEventsRefreshTheLists(t *testing.T) {
	f := newFakeServer(t)
	h, p := start(t, f)
	lines := f.stream(t)
	waitFor(t, h, "ready", func() bool { return p.Status().State == agent.Ready })
	waitFor(t, h, "providers", func() bool { return p.prov.loaded })
	var changed int
	kernel.On(h, agent.ModelsChanged, func(struct{}) { changed++ })
	var events int
	kernel.On(h, agent.Events, func([]agent.Event) { events++ })
	reads := f.providerReads()
	lines <- `{"type":"model.updated","data":{}}`
	waitFor(t, h, "catalog refresh", func() bool {
		return changed == 1 && f.providerReads() > reads
	})
	if events != 0 {
		t.Fatal("a catalog event was forwarded as an agent event")
	}
}

func TestMalformedEventIsReportedOnce(t *testing.T) {
	f := newFakeServer(t)
	h, p := start(t, f)
	lines := f.stream(t)
	waitFor(t, h, "ready", func() bool { return p.Status().State == agent.Ready })
	lines <- `{"type":"session.renamed","data":{"title":"no session"}}`
	lines <- `{"type":"session.renamed","data":{"title":"again"}}`
	waitFor(t, h, "the note", func() bool { return p.Status().Err != nil })
	if p.Status().State != agent.Ready || !strings.Contains(p.Status().Err.Error(), "malformed") {
		t.Fatalf("status %+v", p.Status())
	}
}

func TestServiceCallsSpeakTheWire(t *testing.T) {
	f := newFakeServer(t)
	h, p := start(t, f)
	f.stream(t)
	waitFor(t, h, "ready", func() bool { return p.Status().State == agent.Ready })
	s := service(t, h)

	page, err := call(t, h, func(ctx context.Context) (agent.SessionPage, error) {
		return s.Sessions(ctx, agent.SessionQuery{Cursor: "c1", Search: "a b", Directory: "/w"})
	})
	if err != nil || page.Next != "c2" || len(page.Sessions) != 2 || page.Sessions[0].ID != "new" ||
		page.Sessions[0].Directory != "/w/x" || page.Sessions[1].Updated != 1 {
		t.Fatalf("page %+v, %v", page, err)
	}
	created, err := call(t, h, func(ctx context.Context) (agent.Session, error) {
		return s.Create(ctx, agent.CreateSession{Directory: "/w", Mode: "plan", Model: agent.ModelRef{Provider: "p", ID: "m", Variant: "high"}})
	})
	if err != nil || created.ID != "ses-9" || created.Model.ID != "m" {
		t.Fatalf("created %+v, %v", created, err)
	}
	failed, err := call(t, h, func(ctx context.Context) (agent.Session, error) {
		return s.Create(ctx, agent.CreateSession{Model: agent.ModelRef{Provider: "p", ID: "broken"}})
	})
	if failed.ID != "ses-9" || err == nil || !strings.Contains(err.Error(), "no such model") {
		t.Fatalf("partial create = %+v, %v", failed, err)
	}
	for _, fn := range []func(context.Context) (struct{}, error){
		func(ctx context.Context) (struct{}, error) {
			_, err := s.Send(ctx, agent.Send{Session: "ses-9", Text: "hi",
				Files: []agent.File{{Name: "a.txt", MIME: "text/plain", Data: []byte("x")}}})
			return struct{}{}, err
		},
		func(ctx context.Context) (struct{}, error) { return struct{}{}, s.Abort(ctx, "ses-9") },
		func(ctx context.Context) (struct{}, error) {
			return struct{}{}, s.Reply(ctx, agent.Reply{Session: "ses-9", Approval: "perm/1", Decision: agent.Always})
		},
		func(ctx context.Context) (struct{}, error) { return struct{}{}, s.Rename(ctx, "ses-9", "Title") },
		func(ctx context.Context) (struct{}, error) { return struct{}{}, s.Delete(ctx, "ses-9") },
	} {
		if _, err := call(t, h, fn); err != nil {
			t.Fatal(err)
		}
	}
	modes, err := call(t, h, func(ctx context.Context) ([]agent.Mode, error) {
		m, _ := kernel.Use(h, agent.ModesKey)
		return m.Modes(ctx)
	})
	if err != nil || len(modes) != 1 || modes[0].ID != "build" {
		t.Fatalf("modes %+v, %v", modes, err)
	}
	want := []string{
		"GET /session?limit=50&parentID=null&cursor=c1&search=a+b&directory=%2Fw",
		"POST /session", "POST /session/ses-9/model",
		"POST /session", "POST /session/ses-9/model",
		"POST /session/ses-9/prompt", "POST /session/ses-9/interrupt",
		"POST /session/ses-9/permission/perm%2F1/reply", "PATCH /session/ses-9", "DELETE /session/ses-9",
		"GET /agent",
	}
	if got := f.seen(); !slices.Equal(got, want) {
		t.Fatalf("requests\n got %q\nwant %q", got, want)
	}
	for route, body := range map[string]string{
		"POST /session/ses-9/permission/perm%2F1/reply": `{"decision":"always"}`,
		"POST /session/ses-9/prompt":                    `{"files":[{"name":"a.txt","uri":"data:text/plain;base64,eA=="}],"text":"hi"}`,
	} {
		if f.body(route) != body {
			t.Errorf("%s body = %s", route, f.body(route))
		}
	}
}

func TestRewindStagesWithoutFilesThenCommits(t *testing.T) {
	f := newFakeServer(t)
	h, p := start(t, f)
	f.stream(t)
	waitFor(t, h, "ready", func() bool { return p.Status().State == agent.Ready })
	rewinder, ok := kernel.Use(h, agent.RewindKey)
	if !ok {
		t.Fatal("the backend does not rewind")
	}
	next, err := call(t, h, func(ctx context.Context) (agent.SessionID, error) { return rewinder.Rewind(ctx, "ses-9", "msg_u") })
	if err != nil || next != "ses-9" {
		t.Fatalf("rewound to %q, %v", next, err)
	}
	seen := f.seen()
	stage := slices.Index(seen, "POST /session/ses-9/revert/stage")
	if stage < 0 || !slices.Contains(seen[stage:], "POST /session/ses-9/revert/commit") {
		t.Fatalf("requests %q", seen)
	}
	if body := f.body("POST /session/ses-9/revert/stage"); !strings.Contains(body, `"messageID":"msg_u"`) || !strings.Contains(body, `"files":false`) {
		t.Fatalf("stage body %s", body)
	}
}

func TestChangesReadTheSessionDiff(t *testing.T) {
	f := newFakeServer(t)
	h, p := start(t, f)
	f.stream(t)
	waitFor(t, h, "ready", func() bool { return p.Status().State == agent.Ready })
	changes, ok := kernel.Use(h, agent.ChangesKey)
	if !ok {
		t.Fatal("the backend does not list changes")
	}
	got, err := call(t, h, func(ctx context.Context) ([]agent.FileChange, error) { return changes.Changes(ctx, "ses-9") })
	want := []agent.FileChange{
		{Path: "main.go", Status: agent.FileModified, Added: 1, Removed: 1, Patch: "@@ -1 +1 @@\n-a\n+b\n"},
		{Path: "new.go", Status: agent.FileAdded, Added: 4},
	}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("changes %+v, %v", got, err)
	}
}

func TestModelsCarryTheirContextWindow(t *testing.T) {
	f := newFakeServer(t)
	h, p := start(t, f)
	f.stream(t)
	waitFor(t, h, "ready", func() bool { return p.Status().State == agent.Ready })
	models, ok := kernel.Use(h, agent.ModelsKey)
	if !ok {
		t.Fatal("the backend does not provide models")
	}
	got, err := call(t, h, func(ctx context.Context) ([]agent.Model, error) { return models.Models(ctx) })
	if err != nil || len(got) != 2 || got[0].Context != 200000 || got[0].Free || got[1].Context != 0 || !got[1].Free {
		t.Fatalf("models %+v, %v", got, err)
	}
}

func TestFilesAreFoundAndReadThroughTheServer(t *testing.T) {
	f := newFakeServer(t)
	h, p := start(t, f)
	f.stream(t)
	waitFor(t, h, "ready", func() bool { return p.Status().State == agent.Ready })
	files, ok := kernel.Use(h, agent.FilesKey)
	if !ok {
		t.Fatal("the backend does not provide files")
	}
	entries, err := call(t, h, func(ctx context.Context) ([]agent.FileEntry, error) { return files.Find(ctx, "/w/", "ma", 5) })
	if want := []agent.FileEntry{{Path: "cmd", IsDir: true}, {Path: "cmd/main.go"}, {Path: "README.md"}}; err != nil || !slices.Equal(entries, want) {
		t.Fatalf("found %+v, %v", entries, err)
	}
	if _, err := call(t, h, func(ctx context.Context) ([]agent.FileEntry, error) { return files.Find(ctx, "", "", 0) }); err != nil {
		t.Fatal(err)
	}
	data, err := call(t, h, func(ctx context.Context) ([]byte, error) { return files.Read(ctx, "/w", "docs/a b.md") })
	if err != nil || string(data) != "# hi" {
		t.Fatalf("read %q, %v", data, err)
	}
	if want := []string{
		"GET /fs/find?limit=5&location%5Bdirectory%5D=%2Fw&query=ma",
		"GET /fs/find?query=",
		"GET /fs/read/docs/a%20b.md?location%5Bdirectory%5D=%2Fw",
	}; !slices.Equal(f.seen(), want) {
		t.Fatalf("requests\n got %q\nwant %q", f.seen(), want)
	}
	// A path that leaves the folder never goes out, and a web page is not a file.
	for _, bad := range []string{"", "/etc/hosts", "../secret", "a/../../b"} {
		if _, err := call(t, h, func(ctx context.Context) ([]byte, error) { return files.Read(ctx, "/w", bad) }); err == nil {
			t.Errorf("read %q succeeded", bad)
		}
	}
	if _, err := call(t, h, func(ctx context.Context) ([]byte, error) { return files.Read(ctx, "/w", "gone.txt") }); err == nil ||
		!strings.Contains(err.Error(), "does not serve files") {
		t.Fatalf("an HTML answer was taken for a file: %v", err)
	}
	if n := len(f.seen()); n != 4 {
		t.Fatalf("%d requests; only the clean ones should go out: %q", n, f.seen())
	}
}

func TestReconnectsAfterTheStreamDrops(t *testing.T) {
	f := newFakeServer(t)
	base := attempts.Load()
	h, p := start(t, f)
	close(f.stream(t))
	var downs int
	kernel.On(h, agent.StatusChanged, func(s agent.Status) {
		if s.State == agent.Down {
			downs++
		}
	})
	f.stream(t)
	waitFor(t, h, "second attempt ready", func() bool { return p.Status().State == agent.Ready && p.Status().Attempt == base+2 })
	if downs != 1 {
		t.Fatalf("%d Down statuses, want 1", downs)
	}
}

func TestRefusedPasswordIsDownAndNotConnected(t *testing.T) {
	f := newFakeServer(t)
	f.refuse = true
	h, _ := start(t, f)
	var down *agent.Status
	kernel.On(h, agent.StatusChanged, func(s agent.Status) {
		if s.State == agent.Down && down == nil {
			down = &s
		}
	})
	waitFor(t, h, "down", func() bool { return down != nil })
	if !errors.Is(down.Err, errRefused) {
		t.Fatalf("status %+v", down)
	}
	if _, err := service(t, h).Sessions(context.Background(), agent.SessionQuery{}); !errors.Is(err, agent.ErrNotConnected) {
		t.Fatalf("Sessions while down: %v", err)
	}
}

func TestConfigureReplacesTheConnection(t *testing.T) {
	first, second := newFakeServer(t), newFakeServer(t)
	h, p := start(t, first)
	first.stream(t)
	waitFor(t, h, "ready on first", func() bool { return p.Status().State == agent.Ready })
	if err := p.Configure(Connection{Remote: true, URL: "::bad::"}); err == nil {
		t.Fatal("bad address accepted")
	}
	if err := p.Configure(Connection{Remote: true, URL: second.URL, Password: second.password}); err != nil {
		t.Fatal(err)
	}
	second.stream(t)
	waitFor(t, h, "ready on second", func() bool { return p.Status().State == agent.Ready && p.Status().Endpoint == second.URL })
	h.Flush()
	if got := p.Connection(); got.URL != second.URL {
		t.Fatalf("saved connection %+v", got)
	}
}

func TestDisableStopsTheConnection(t *testing.T) {
	f := newFakeServer(t)
	h, p := start(t, f)
	f.stream(t)
	waitFor(t, h, "ready", func() bool { return p.Status().State == agent.Ready })
	if err := h.Disable("opencode"); err != nil {
		t.Fatal(err)
	}
	if _, ok := kernel.Use(h, agent.ServiceKey); ok {
		t.Fatal("service still provided")
	}
	if _, err := p.Sessions(context.Background(), agent.SessionQuery{}); !errors.Is(err, agent.ErrNotConnected) {
		t.Fatalf("disabled plugin still connected: %v", err)
	}
}

func TestSettingsSectionSavesTheConnection(t *testing.T) {
	first, second := newFakeServer(t), newFakeServer(t)
	h, p := start(t, first)
	first.stream(t)
	waitFor(t, h, "ready", func() bool { return p.Status().State == agent.Ready })
	tt := ui.NewTester(func(c *ui.Context) { ui.Column(c).Children(func() { p.section(c) }) }, 700, 500)
	tt.Frame()
	if !tt.HasText("Connected to "+first.URL+" · OpenCode 2.0.0") || !tt.HasText("Server address") {
		t.Fatalf("section %q", tt.Texts())
	}
	p.form = form{conn: Connection{Remote: true, URL: "::bad::"}, dirty: true}
	tt.Frame()
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !p.form.dirty || p.form.err == "" {
		t.Fatalf("bad address saved: %+v", p.form)
	}
	p.form = form{conn: Connection{Remote: true, URL: second.URL, Password: second.password}, dirty: true}
	tt.Frame()
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	second.stream(t)
	waitFor(t, h, "ready on second", func() bool { return p.Status().Endpoint == second.URL && p.Status().State == agent.Ready })
	if p.form.dirty || p.Connection().URL != second.URL {
		t.Fatalf("form %+v connection %+v", p.form, p.Connection())
	}
}
