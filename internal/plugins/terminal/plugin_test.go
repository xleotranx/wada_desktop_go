package terminal

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
	"github.com/xleotranx/wada-kit/kittest"
	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/tabs"
	contractterm "wada-desktop-go/internal/contract/terminal"
	"wada-desktop-go/internal/plugintest"
)

type fakeTermStream struct {
	info contractterm.Info
	snap []byte
	out  chan []byte

	mu     sync.Mutex
	inputs [][]byte
	closed bool
}

func newFakeTermStream(info contractterm.Info, snap string) *fakeTermStream {
	return &fakeTermStream{
		info: info,
		snap: []byte(snap),
		out:  make(chan []byte, 32),
	}
}

func (s *fakeTermStream) Info() contractterm.Info { return s.info }
func (s *fakeTermStream) Snapshot() []byte        { return s.snap }
func (s *fakeTermStream) Output() <-chan []byte   { return s.out }

func (s *fakeTermStream) WriteInput(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	s.inputs = append(s.inputs, cp)
	return nil
}

func (s *fakeTermStream) Resize(cols, rows int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.info.Cols = cols
	s.info.Rows = rows
	return nil
}

func (s *fakeTermStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *fakeTermStream) sentInputs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.inputs))
	for i, b := range s.inputs {
		out[i] = string(b)
	}
	return out
}

func (s *fakeTermStream) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

type fakeTermService struct {
	*plugintest.Agent

	mu        sync.Mutex
	seq       int
	list      []contractterm.Info
	snapshots map[string]string
	streams   map[string][]*fakeTermStream
	created   []contractterm.CreateRequest
	killed    []string
}

func newFakeTermService() *fakeTermService {
	a := plugintest.NewAgent()
	a.State = agent.Status{State: agent.Ready, Attempt: 1}
	return &fakeTermService{
		Agent:     a,
		snapshots: map[string]string{},
		streams:   map[string][]*fakeTermStream{},
	}
}

func (f *fakeTermService) NewPlugin() kernel.Plugin { return &fakeTermPlugin{f: f} }

type fakeTermPlugin struct{ f *fakeTermService }

func (p *fakeTermPlugin) Manifest() kernel.Manifest {
	base := p.f.Manifest()
	base.Provides = append(base.Provides, contractterm.ServiceKey)
	return base
}

func (p *fakeTermPlugin) Setup(c *kernel.Context) error {
	if err := p.f.Setup(c); err != nil {
		return err
	}
	return kernel.Provide[contractterm.Service](c, contractterm.ServiceKey, p.f)
}

func (f *fakeTermService) List(context.Context) ([]contractterm.Info, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.list), nil
}

func (f *fakeTermService) Create(_ context.Context, req contractterm.CreateRequest) (contractterm.Info, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	f.created = append(f.created, req)
	info := contractterm.Info{
		ID:        fmt.Sprintf("term_%d", f.seq),
		Name:      req.Name,
		Directory: req.Directory,
		Shell:     "/bin/zsh",
		Cols:      req.Cols,
		Rows:      req.Rows,
		PID:       1000 + f.seq,
		Running:   true,
	}
	if info.Name == "" {
		info.Name = fmt.Sprintf("zsh %d", f.seq)
	}
	f.list = append(f.list, info)
	if _, ok := f.snapshots[info.ID]; !ok {
		f.snapshots[info.ID] = "$ ready\r\n"
	}
	return info, nil
}

func (f *fakeTermService) Attach(_ context.Context, req contractterm.AttachRequest) (contractterm.Stream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var info contractterm.Info
	found := false
	for _, t := range f.list {
		if t.ID == req.ID {
			info = t
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("terminal %q not found", req.ID)
	}
	st := newFakeTermStream(info, f.snapshots[req.ID])
	f.streams[req.ID] = append(f.streams[req.ID], st)
	return st, nil
}

func (f *fakeTermService) Kill(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.killed = append(f.killed, id)
	f.list = slices.DeleteFunc(f.list, func(t contractterm.Info) bool { return t.ID == id })
	return nil
}

func (f *fakeTermService) latestStream(id string) *fakeTermStream {
	f.mu.Lock()
	defer f.mu.Unlock()
	arr := f.streams[id]
	if len(arr) == 0 {
		return nil
	}
	return arr[len(arr)-1]
}

func (f *fakeTermService) streamCount(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.streams[id])
}

func (f *fakeTermService) setSnapshot(id, snap string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snapshots[id] = snap
}

func waitCond(t *testing.T, h *kerneltest.Harness, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %s", what)
		}
		h.Flush()
		time.Sleep(time.Millisecond)
	}
}

type fixture struct {
	h    *kerneltest.Harness
	p    *Plugin
	svc  *fakeTermService
	host *kittest.Host
	tabs *plugintest.Tabs
}

func startTerminalTest(t *testing.T) fixture {
	t.Helper()
	f := fixture{svc: newFakeTermService(), host: &kittest.Host{Data: t.TempDir()},
		tabs: &plugintest.Tabs{Current: tabs.Tab{ID: "chat:s1", Kind: tabs.Conversation, Folder: "/repo/wada"}}}
	f.h = kerneltest.New(t, f.host.New, f.svc.NewPlugin, f.tabs.New, kerneltest.Keep(&f.p, func() *Plugin { return &Plugin{} }))
	return f
}

// pane draws the tab on show.
func (f fixture) pane() *ui.Tester {
	return ui.NewTester(func(c *ui.Context) { f.p.paneView(c, f.tabs.Current) }, 480, 600)
}

// showTerminal runs ⌘J and waits for the terminal to be attached.
func (f fixture) showTerminal(t *testing.T, id string) {
	t.Helper()
	f.p.showTerminal()
	waitCond(t, f.h, "attached "+id, func() bool { return f.p.streamID == id })
}

func TestShowTerminalOpensATabAndRendersTheSnapshot(t *testing.T) {
	f := startTerminalTest(t)
	f.showTerminal(t, "term_1")
	waitCond(t, f.h, "snapshot", func() bool { return f.p.screen.PlainText() != "" })

	f.svc.mu.Lock()
	created := slices.Clone(f.svc.created)
	f.svc.mu.Unlock()
	if len(created) != 1 || created[0].Directory != "/repo/wada" {
		t.Fatalf("created = %+v, want dir /repo/wada", created)
	}
	if !slices.Equal(f.tabs.Opened, []string{Kind + " term_1 /repo/wada"}) || f.tabs.Current.Ref != "term_1" {
		t.Fatalf("opened %q, current %+v", f.tabs.Opened, f.tabs.Current)
	}
	tt := f.pane()
	tt.Frame()
	for _, want := range []string{"zsh 1", "$ ready", "zsh · /repo/wada"} {
		if !tt.HasText(want) {
			t.Fatalf("%q is missing from the pane: %q", want, tt.Texts())
		}
	}

	// ⌘J again on the terminal does nothing; leaving the tab detaches
	// without killing the PTY, and ⌘J comes back to the same tab.
	f.p.showTerminal()
	f.tabs.Close("chat:none")
	f.tabs.Select("missing")
	f.tabs.List = append(f.tabs.List, tabs.Tab{ID: "chat:s1", Kind: tabs.Conversation, Folder: "/repo/wada"})
	f.tabs.Select("chat:s1")
	waitCond(t, f.h, "stream closed on detach", func() bool {
		st := f.svc.latestStream("term_1")
		return st != nil && st.isClosed() && f.p.streamID == ""
	})
	f.svc.mu.Lock()
	killed, made := len(f.svc.killed), len(f.svc.created)
	f.svc.mu.Unlock()
	if killed != 0 {
		t.Fatalf("leaving the tab killed %d terminals", killed)
	}
	f.showTerminal(t, "term_1")
	if made != 1 || len(f.tabs.Opened) != 1 {
		t.Fatalf("⌘J made %d terminals and opened %q", made, f.tabs.Opened)
	}
}

func TestShowTerminalReusesARunningTerminalWithoutATab(t *testing.T) {
	f := startTerminalTest(t)
	f.svc.list = []contractterm.Info{
		{ID: "term_7", Name: "elsewhere", Directory: "/repo/other", Running: true},
		{ID: "term_8", Name: "server", Directory: "/repo/wada", Running: true},
	}
	f.showTerminal(t, "term_8")
	f.svc.mu.Lock()
	created := len(f.svc.created)
	f.svc.mu.Unlock()
	if created != 0 || f.tabs.Current.Ref != "term_8" {
		t.Fatalf("created %d, current %+v", created, f.tabs.Current)
	}
	if title := f.p.title("term_8"); title != "server" {
		t.Fatalf("title %q", title)
	}
}

func TestLiveANSIStreamInputAndControlKeys(t *testing.T) {
	f := startTerminalTest(t)
	f.showTerminal(t, "term_1")
	st := f.svc.latestStream("term_1")

	st.out <- []byte("\x1b[32mok\x1b[0m build done\r\nnext> ")
	waitCond(t, f.h, "live output applied", func() bool {
		return strings.Contains(f.p.screen.PlainText(), "ok build done")
	})

	tt := f.pane()
	tt.Frame()
	if !tt.HasText("ok build done") {
		t.Fatalf("pane missing live output: %q", tt.Texts())
	}

	f.p.input = "go test ./..."
	f.p.submitInput()
	waitCond(t, f.h, "command delivered", func() bool { return len(st.sentInputs()) == 1 })
	f.p.sendControl(0x03)
	waitCond(t, f.h, "ctrl-c delivered", func() bool { return len(st.sentInputs()) == 2 })
	got := st.sentInputs()
	if got[0] != "go test ./...\r" || got[1] != "\x03" {
		t.Fatalf("sentInputs = %q", got)
	}

	f.p.copyOutput()
	if !strings.Contains(f.host.Copied, "ok build done") {
		t.Fatalf("copied clipboard text = %q", f.host.Copied)
	}
}

func TestEachTabAttachesItsTerminalAndKillClosesIt(t *testing.T) {
	f := startTerminalTest(t)
	f.svc.list = []contractterm.Info{
		{ID: "term_1", Name: "server", Directory: "/repo/wada", Shell: "/bin/zsh", Running: true, PID: 101},
		{ID: "term_2", Name: "tests", Directory: "/repo/wada", Shell: "/bin/zsh", Running: true, PID: 102},
	}
	f.svc.snapshots["term_1"] = "server listening :8080\r\n"
	f.svc.snapshots["term_2"] = "PASS ok all tests\r\n"

	f.tabs.Open(Kind, "term_1", "/repo/wada")
	waitCond(t, f.h, "attached term_1", func() bool {
		return f.p.streamID == "term_1" && strings.Contains(f.p.screen.PlainText(), "server listening")
	})
	f.tabs.Open(Kind, "term_2", "/repo/wada")
	waitCond(t, f.h, "attached term_2", func() bool {
		return f.p.streamID == "term_2" && strings.Contains(f.p.screen.PlainText(), "PASS ok all tests")
	})
	tt := f.pane()
	tt.Frame()
	if !tt.HasText("PASS ok all tests") || tt.HasText("server listening :8080") {
		t.Fatalf("term_2's tab shows %q", tt.Texts())
	}

	f.p.killTerminal("term_2")
	waitCond(t, f.h, "term_2's tab closed", func() bool {
		return !slices.ContainsFunc(f.tabs.List, func(x tabs.Tab) bool { return x.Ref == "term_2" })
	})
	f.svc.mu.Lock()
	killed := slices.Clone(f.svc.killed)
	f.svc.mu.Unlock()
	if killed[0] != "term_2" || !slices.ContainsFunc(f.tabs.List, func(x tabs.Tab) bool { return x.Ref == "term_1" }) {
		t.Fatalf("killed %q, tabs %+v", killed, f.tabs.List)
	}
}

func TestATabWhoseTerminalIsGoneCloses(t *testing.T) {
	f := startTerminalTest(t)
	f.tabs.Open(Kind, "term_9", "/repo/wada")
	waitCond(t, f.h, "the gone terminal's tab closed", func() bool { return len(f.tabs.List) == 0 })
}

func TestReattachesAfterDaemonReconnect(t *testing.T) {
	f := startTerminalTest(t)
	f.svc.list = []contractterm.Info{
		{ID: "term_1", Name: "shell", Directory: "/repo/wada", Running: true, PID: 200},
	}
	f.svc.setSnapshot("term_1", "first\r\n")

	f.showTerminal(t, "term_1")
	waitCond(t, f.h, "initial attach", func() bool { return f.svc.streamCount("term_1") == 1 })

	kernel.Emit(f.h, agent.StatusChanged, agent.Status{State: agent.Down, Attempt: 1})
	waitCond(t, f.h, "detached on socket drop", func() bool { return f.p.streamID == "" })

	f.svc.setSnapshot("term_1", "reconnected\r\n")
	kernel.Emit(f.h, agent.StatusChanged, agent.Status{State: agent.Ready, Attempt: 2})
	waitCond(t, f.h, "reattached after reconnect", func() bool {
		return f.p.streamID == "term_1" && strings.Contains(f.p.screen.PlainText(), "reconnected")
	})
}

func TestInputKeepsItsOrder(t *testing.T) {
	f := startTerminalTest(t)
	f.showTerminal(t, "term_1")
	st := f.svc.latestStream("term_1")

	want := make([]string, 100)
	for i := range want {
		want[i] = fmt.Sprintf("cmd %d\r", i)
		f.p.sendBytes([]byte(want[i]))
	}
	f.p.sendControl(0x03)
	want = append(want, "\x03")
	waitCond(t, f.h, "every input delivered", func() bool { return len(st.sentInputs()) == len(want) })
	if got := st.sentInputs(); !slices.Equal(got, want) {
		t.Fatalf("inputs arrived out of order: %q", got)
	}
}

func TestThePaneFitsThePTY(t *testing.T) {
	f := startTerminalTest(t)
	f.showTerminal(t, "term_1")
	st := f.svc.latestStream("term_1")

	tt := f.pane()
	tt.Frame()
	tt.Frame()
	waitCond(t, f.h, "PTY resized to the pane", func() bool {
		st.mu.Lock()
		defer st.mu.Unlock()
		return st.info.Cols != 0 && st.info.Cols < defaultCols
	})
	if f.p.cols >= defaultCols || f.p.rows <= minRows || f.p.screen.cols != f.p.cols {
		t.Fatalf("fitted size = %dx%d (screen %d cols)", f.p.cols, f.p.rows, f.p.screen.cols)
	}
}

func TestInputWhileAttachingWaitsForTheAttachment(t *testing.T) {
	f := startTerminalTest(t)
	f.showTerminal(t, "term_1")

	// Re-attach and type before the attachment has been posted back.
	f.p.attach("term_1")
	if f.p.streamID != "" {
		t.Fatal("attachment finished synchronously; the test needs it pending")
	}
	f.p.input = "echo early"
	f.p.submitInput()
	if f.p.errText != "" {
		t.Fatalf("input while attaching reported %q", f.p.errText)
	}
	waitCond(t, f.h, "second attachment", func() bool { return f.svc.streamCount("term_1") == 2 })
	st := f.svc.latestStream("term_1")
	waitCond(t, f.h, "early input delivered", func() bool { return len(st.sentInputs()) == 1 })
	if got := st.sentInputs()[0]; got != "echo early\r" {
		t.Fatalf("input = %q", got)
	}
}
