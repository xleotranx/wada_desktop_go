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
	chatcontract "wada-desktop-go/internal/contract/chat"
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

func (f *fakeTermService) Capture(_ context.Context, id string, _ int) (contractterm.CaptureResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	snap := f.snapshots[id]
	return contractterm.CaptureResult{Text: snap, Snapshot: []byte(snap)}, nil
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

func startTerminalTest(t *testing.T) (*kerneltest.Harness, *Plugin, *fakeTermService, *kittest.Host) {
	t.Helper()
	svc := newFakeTermService()
	chatFake := &plugintest.Chat{}
	host := &kittest.Host{Data: t.TempDir()}
	var p *Plugin
	h := kerneltest.New(t, host.New, svc.NewPlugin, chatFake.New, kerneltest.Keep(&p, func() *Plugin { return &Plugin{} }))
	kernel.Emit(h, chatcontract.Changed, chatcontract.Conversation{Session: "s1", Directory: "/repo/wada"})
	return h, p, svc, host
}

func TestToggleOpensCreatesAndRendersSnapshot(t *testing.T) {
	h, p, svc, _ := startTerminalTest(t)

	p.toggle()
	if !p.open {
		t.Fatal("expected drawer open after toggle")
	}
	waitCond(t, h, "created and attached terminal", func() bool {
		return p.streamID == "term_1" && p.screen.PlainText() != ""
	})

	svc.mu.Lock()
	created := slices.Clone(svc.created)
	svc.mu.Unlock()
	if len(created) != 1 || created[0].Directory != "/repo/wada" {
		t.Fatalf("created = %+v, want dir /repo/wada", created)
	}

	tt := ui.NewTester(p.drawerView, 480, 600)
	tt.Frame()
	if !tt.HasText("$ ready") {
		t.Fatalf("drawer missing snapshot text: %q", tt.Texts())
	}

	// Closing hides the drawer and detaches without killing the PTY.
	p.toggle()
	waitCond(t, h, "stream closed on detach", func() bool {
		st := svc.latestStream("term_1")
		return st != nil && st.isClosed()
	})
	svc.mu.Lock()
	killed := len(svc.killed)
	svc.mu.Unlock()
	if killed != 0 {
		t.Fatalf("closing drawer killed %d terminals, want 0", killed)
	}
}

func TestLiveANSIStreamInputAndControlKeys(t *testing.T) {
	h, p, svc, host := startTerminalTest(t)
	p.toggle()
	waitCond(t, h, "attached term_1", func() bool { return svc.latestStream("term_1") != nil })
	st := svc.latestStream("term_1")

	st.out <- []byte("\x1b[32mok\x1b[0m build done\r\nnext> ")
	waitCond(t, h, "live output applied", func() bool {
		return strings.Contains(p.screen.PlainText(), "ok build done")
	})

	tt := ui.NewTester(p.drawerView, 480, 600)
	tt.Frame()
	if !tt.HasText("ok build done") {
		t.Fatalf("drawer missing live output: %q", tt.Texts())
	}

	p.input = "go test ./..."
	p.submitInput()
	waitCond(t, h, "command delivered", func() bool {
		return len(st.sentInputs()) == 1
	})
	p.sendControl(0x03)
	waitCond(t, h, "ctrl-c delivered", func() bool {
		return len(st.sentInputs()) == 2
	})
	got := st.sentInputs()
	if got[0] != "go test ./...\r" || got[1] != "\x03" {
		t.Fatalf("sentInputs = %q", got)
	}

	p.copyOutput()
	if !strings.Contains(host.Copied, "ok build done") {
		t.Fatalf("copied clipboard text = %q", host.Copied)
	}
}

func TestMultiTabSwitchAndKill(t *testing.T) {
	h, p, svc, _ := startTerminalTest(t)
	svc.list = []contractterm.Info{
		{ID: "term_1", Name: "server", Directory: "/repo/wada", Shell: "/bin/zsh", Running: true, PID: 101},
		{ID: "term_2", Name: "tests", Directory: "/repo/wada", Shell: "/bin/zsh", Running: true, PID: 102},
	}
	svc.snapshots["term_1"] = "server listening :8080\r\n"
	svc.snapshots["term_2"] = "PASS ok all tests\r\n"

	p.toggle()
	waitCond(t, h, "attached term_1", func() bool { return p.streamID == "term_1" })

	tt := ui.NewTester(p.drawerView, 480, 600)
	tt.Frame()
	if !tt.HasText("server listening :8080") {
		t.Fatalf("missing term_1 text: %q", tt.Texts())
	}

	p.selectTerminal("term_2")
	waitCond(t, h, "attached term_2", func() bool {
		return p.streamID == "term_2" && strings.Contains(p.screen.PlainText(), "PASS ok all tests")
	})
	tt.Frame()
	if !tt.HasText("PASS ok all tests") {
		t.Fatalf("missing term_2 text after switch: %q", tt.Texts())
	}

	p.killTerminal("term_2")
	waitCond(t, h, "fallback to term_1 after kill", func() bool {
		return p.activeID == "term_1" && p.streamID == "term_1"
	})
}

func TestReattachesAfterDaemonReconnect(t *testing.T) {
	h, p, svc, _ := startTerminalTest(t)
	svc.list = []contractterm.Info{
		{ID: "term_1", Name: "shell", Directory: "/repo/wada", Running: true, PID: 200},
	}
	svc.setSnapshot("term_1", "first\r\n")

	p.toggle()
	waitCond(t, h, "initial attach", func() bool { return svc.streamCount("term_1") == 1 })

	kernel.Emit(h, agent.StatusChanged, agent.Status{State: agent.Down, Attempt: 1})
	waitCond(t, h, "detached on socket drop", func() bool { return p.streamID == "" })

	svc.setSnapshot("term_1", "reconnected\r\n")
	kernel.Emit(h, agent.StatusChanged, agent.Status{State: agent.Ready, Attempt: 2})
	waitCond(t, h, "reattached after reconnect", func() bool {
		return p.streamID == "term_1" && strings.Contains(p.screen.PlainText(), "reconnected")
	})
}

func TestInputKeepsItsOrder(t *testing.T) {
	h, p, svc, _ := startTerminalTest(t)
	p.toggle()
	waitCond(t, h, "attached term_1", func() bool { return p.stream != nil })
	st := svc.latestStream("term_1")

	want := make([]string, 100)
	for i := range want {
		want[i] = fmt.Sprintf("cmd %d\r", i)
		p.sendBytes([]byte(want[i]))
	}
	p.sendControl(0x03)
	want = append(want, "\x03")
	waitCond(t, h, "every input delivered", func() bool { return len(st.sentInputs()) == len(want) })
	if got := st.sentInputs(); !slices.Equal(got, want) {
		t.Fatalf("inputs arrived out of order: %q", got)
	}
}

func TestDrawerFitsThePTY(t *testing.T) {
	h, p, svc, _ := startTerminalTest(t)
	p.toggle()
	waitCond(t, h, "attached term_1", func() bool { return p.stream != nil })
	st := svc.latestStream("term_1")

	tt := ui.NewTester(p.drawerView, 480, 600)
	tt.Frame()
	tt.Frame()
	waitCond(t, h, "PTY resized to the drawer", func() bool {
		st.mu.Lock()
		defer st.mu.Unlock()
		return st.info.Cols != 0 && st.info.Cols < defaultCols
	})
	if p.cols >= defaultCols || p.rows <= minRows || p.screen.cols != p.cols {
		t.Fatalf("fitted size = %dx%d (screen %d cols)", p.cols, p.rows, p.screen.cols)
	}
}
