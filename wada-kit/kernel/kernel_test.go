package kernel_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
)

type fake struct {
	m     kernel.Manifest
	setup func(*kernel.Context) error
	ctx   *kernel.Context
}

func (f *fake) Manifest() kernel.Manifest { return f.m }
func (f *fake) Setup(c *kernel.Context) error {
	f.ctx = c
	if f.setup == nil {
		return nil
	}
	return f.setup(c)
}

func plug(id string, setup func(*kernel.Context) error) *fake {
	return &fake{m: kernel.Manifest{ID: id}, setup: setup}
}

type agent interface{ Name() string }
type named string

func (n named) Name() string { return string(n) }

var (
	agentKey = kernel.NewKey[agent]("agent.service")
	ping     = kernel.NewTopic[string]("test.ping")
	outgoing = kernel.NewPipeline[string]("test.outgoing")
	sidebar  = kernel.NewPoint[string]("test.sidebar")
)

func backend(id string) *fake {
	f := plug(id, func(c *kernel.Context) error {
		return kernel.Provide[agent](c, agentKey, named(id))
	})
	f.m.Provides = []kernel.Named{agentKey}
	return f
}

func consumer(id string, got *agent) *fake {
	f := plug(id, func(c *kernel.Context) error {
		*got = kernel.Require(c, agentKey)
		return nil
	})
	f.m.Requires = []kernel.Named{agentKey}
	return f
}

func state(t *testing.T, k *kerneltest.Harness, id string) kernel.Status {
	t.Helper()
	for _, s := range k.Plugins() {
		if s.Manifest.ID == id {
			return s
		}
	}
	t.Fatalf("no plugin %q", id)
	return kernel.Status{}
}

func closeClean(t *testing.T, h *kerneltest.Harness) {
	t.Helper()
	h.Close()
	if n := kernel.Leaks(h.Kernel); n != 0 {
		t.Fatalf("%d registrations left after Close", n)
	}
}

func TestScopeDisposeOrder(t *testing.T) {
	var log []string
	var child *kernel.Scope
	p := plug("p", func(c *kernel.Context) error {
		c.OnDispose(func() { log = append(log, "root first") })
		child = c.NewScope()
		child.OnDispose(func() {
			log = append(log, "child, ctx err="+errString(child.Lifetime().Err()))
		})
		c.NewScope().OnDispose(func() { log = append(log, "second child") })
		c.OnDispose(func() { log = append(log, "root second") })
		return nil
	})
	h := kerneltest.New(t, p)
	if err := h.Disable("p"); err != nil {
		t.Fatal(err)
	}
	want := []string{"second child", "child, ctx err=context canceled", "root second", "root first"}
	if !slices.Equal(log, want) {
		t.Fatalf("dispose order\n got %q\nwant %q", log, want)
	}
	child.Dispose() // idempotent
	if len(log) != len(want) || child.Alive() {
		t.Fatal("second dispose ran disposers again")
	}
	closeClean(t, h)
}

func errString(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}

func TestPostAfterDisposeIsDropped(t *testing.T) {
	var ran []string
	p := plug("p", nil)
	h := kerneltest.New(t, p)
	s := p.ctx.NewScope()
	s.Post(func() { ran = append(ran, "alive") })
	h.RunPosts()
	s.Post(func() { ran = append(ran, "disposed") })
	s.Dispose()
	h.RunPosts()
	if !slices.Equal(ran, []string{"alive"}) {
		t.Fatalf("ran %q", ran)
	}
	closeClean(t, h)
}

func TestTaskDropsSupersededAndDisposedResults(t *testing.T) {
	p := plug("p", nil)
	h := kerneltest.New(t, p)
	var task kernel.Task
	var applied []string
	run := func(o kernel.Owner, v string) {
		kernel.Go(o, &task, func(ctx context.Context) (string, error) { return v, ctx.Err() },
			func(v string, err error) { applied = append(applied, v+":"+errString(err)) })
	}
	// A, then B, then A finishes first: only B applies.
	run(p.ctx, "a")
	run(p.ctx, "b")
	if !task.Running() || h.Work() != 2 {
		t.Fatalf("running=%v work=%d", task.Running(), h.Work())
	}
	h.RunWork(0)
	h.RunPosts()
	h.RunWork(0)
	h.RunPosts()
	if !slices.Equal(applied, []string{"b:nil"}) || task.Running() {
		t.Fatalf("applied %q running=%v", applied, task.Running())
	}
	// A result for a disposed scope is dropped.
	s := p.ctx.NewScope()
	run(s, "c")
	s.Dispose()
	h.Flush()
	// Cancel drops the run.
	run(p.ctx, "d")
	task.Cancel()
	h.Flush()
	if len(applied) != 1 {
		t.Fatalf("applied %q", applied)
	}
	closeClean(t, h)
}

func TestRequirementsBlockAndUnblock(t *testing.T) {
	var got agent
	var log []string
	b := backend("opencode")
	b.setup = func(c *kernel.Context) error {
		c.OnDispose(func() { log = append(log, "backend") })
		return kernel.Provide[agent](c, agentKey, named("opencode"))
	}
	chat := consumer("chat", &got)
	inner := chat.setup
	chat.setup = func(c *kernel.Context) error {
		c.OnDispose(func() { log = append(log, "chat") })
		return inner(c)
	}
	// Installed consumer-first: setup still runs provider-first.
	h := kerneltest.New(t, chat, b)
	if got == nil || got.Name() != "opencode" || state(t, h, "chat").State != kernel.Active {
		t.Fatalf("chat did not get the backend: %v", got)
	}
	if err := h.Disable("opencode"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(log, []string{"chat", "backend"}) {
		t.Fatalf("dependents must dispose first: %q", log)
	}
	st := state(t, h, "chat")
	if st.State != kernel.Blocked || !slices.Equal(st.Missing, []string{"agent.service"}) {
		t.Fatalf("chat status %+v", st)
	}
	if _, ok := kernel.Use(h, agentKey); ok {
		t.Fatal("service still provided after its plugin was disabled")
	}
	if err := h.Enable("opencode"); err != nil {
		t.Fatal(err)
	}
	if state(t, h, "chat").State != kernel.Active {
		t.Fatal("chat did not unblock when its provider returned")
	}
	closeClean(t, h)
}

func TestSwitchingProviders(t *testing.T) {
	var got agent
	pi := backend("pi")
	pi.m.DefaultOff = true
	h := kerneltest.New(t, backend("opencode"), pi, consumer("chat", &got))
	if state(t, h, "pi").State != kernel.Disabled {
		t.Fatal("DefaultOff plugin started")
	}
	if err := h.Enable("pi"); err == nil || !strings.Contains(err.Error(), "already provided") {
		t.Fatalf("second provider enabled: %v", err)
	}
	if err := h.Disable("opencode"); err != nil {
		t.Fatal(err)
	}
	if err := h.Enable("pi"); err != nil {
		t.Fatal(err)
	}
	if got.Name() != "pi" || state(t, h, "chat").State != kernel.Active {
		t.Fatalf("chat uses %v", got)
	}
	closeClean(t, h)
}

func TestBothProvidersEnabledFailsTheLater(t *testing.T) {
	h := kerneltest.New(t, backend("opencode"), backend("pi"))
	if st := state(t, h, "pi"); st.State != kernel.Failed || !strings.Contains(st.Err.Error(), "opencode") {
		t.Fatalf("pi status %+v", st)
	}
	closeClean(t, h)
}

func TestProvideRules(t *testing.T) {
	undeclared := plug("undeclared", func(c *kernel.Context) error {
		return kernel.Provide[agent](c, agentKey, named("x"))
	})
	silent := plug("silent", nil)
	silent.m.Provides = []kernel.Named{agentKey}
	h := kerneltest.New(t, undeclared, silent)
	if st := state(t, h, "undeclared"); st.State != kernel.Failed || !strings.Contains(st.Err.Error(), "Provides") {
		t.Fatalf("undeclared %+v", st)
	}
	if st := state(t, h, "silent"); st.State != kernel.Failed || !strings.Contains(st.Err.Error(), "did not provide") {
		t.Fatalf("silent %+v", st)
	}
	if err := kernel.Provide[agent](undeclared.ctx, agentKey, named("late")); err == nil {
		t.Fatal("Provide outside Setup succeeded")
	}
	closeClean(t, h)
}

func TestInstallValidation(t *testing.T) {
	selfish := plug("selfish", nil)
	selfish.m.Provides = []kernel.Named{agentKey}
	selfish.m.Requires = []kernel.Named{agentKey}

	other := kernel.NewKey[agent]("other.service")
	a, b := plug("a", nil), plug("b", nil)
	a.m.Provides, a.m.Requires = []kernel.Named{agentKey}, []kernel.Named{other}
	b.m.Provides, b.m.Requires = []kernel.Named{other}, []kernel.Named{agentKey}

	for name, plugins := range map[string][]kernel.Plugin{
		"bad id":    {plug("Bad_ID", nil)},
		"reserved":  {plug("kernel", nil)},
		"host":      {plug("app", nil)},
		"duplicate": {plug("p", nil), plug("p", nil)},
		"self":      {selfish},
		"cycle":     {a, b},
	} {
		k := kernel.New(kernel.Options{Post: func(func()) {}, Store: kerneltest.NewStore()})
		if err := k.Install(plugins...); err == nil {
			t.Errorf("%s: Install accepted", name)
		}
	}
}

func TestFailedSetupUndoesRegistrations(t *testing.T) {
	for _, mode := range []string{"error", "panic"} {
		t.Run(mode, func(t *testing.T) {
			p := plug("p", func(c *kernel.Context) error {
				if err := kernel.Contribute(c, sidebar, "p.item", 0, "item"); err != nil {
					return err
				}
				kernel.On(c, ping, func(string) {})
				if mode == "panic" {
					panic("boom")
				}
				return errors.New("boom")
			})
			h := kerneltest.Start(t, kerneltest.Config{Lenient: true}, p)
			st := state(t, h, "p")
			if st.State != kernel.Failed || !strings.Contains(st.Err.Error(), "boom") {
				t.Fatalf("status %+v", st)
			}
			if len(kernel.Items(h, sidebar)) != 0 || kernel.Leaks(h.Kernel) != 0 {
				t.Fatal("failed setup left registrations")
			}
			closeClean(t, h)
		})
	}
}

func TestCallbackPanicsFailOnlyTheirPlugin(t *testing.T) {
	var task kernel.Task
	bad := plug("bad", nil)
	var heard []string
	good := plug("good", func(c *kernel.Context) error {
		kernel.On(c, ping, func(v string) { heard = append(heard, v) })
		return nil
	})
	cases := map[string]func(h *kerneltest.Harness){
		"listener": func(h *kerneltest.Harness) {
			kernel.On(bad.ctx, ping, func(string) { panic("listener") })
			kernel.Emit(h, ping, "hello")
		},
		"transform": func(h *kerneltest.Harness) {
			kernel.Transform(bad.ctx, outgoing, 0, func(string) (string, error) { panic("transform") })
			if v, err := kernel.Run(h, outgoing, "x"); v != "x" || err != nil {
				t.Errorf("run after panicking transform = %q, %v", v, err)
			}
		},
		"command": func(h *kerneltest.Harness) {
			kernel.Contribute(bad.ctx, kernel.Commands, "bad.cmd", 0, kernel.Command{Run: func() { panic("command") }})
			for _, e := range kernel.Items(h, kernel.Commands) {
				kernel.Invoke(h, e, func(c kernel.Command) { c.Run() })
			}
		},
		"disposer": func(h *kerneltest.Harness) {
			bad.ctx.OnDispose(func() { panic("disposer") })
			h.Disable("bad")
		},
		"post": func(h *kerneltest.Harness) {
			bad.ctx.Post(func() { panic("post") })
			h.RunPosts()
		},
		"task": func(h *kerneltest.Harness) {
			kernel.Go(bad.ctx, &task, func(context.Context) (int, error) { panic("task") }, func(int, error) {})
			h.Flush()
		},
		"spawn": func(h *kerneltest.Harness) {
			bad.ctx.Spawn(func(context.Context) { panic("spawn") })
			h.WaitPosts(1)
		},
		"superseded task": func(h *kerneltest.Harness) {
			kernel.Go(bad.ctx, &task, func(context.Context) (int, error) { panic("superseded task") }, func(int, error) {})
			kernel.Go(bad.ctx, &task, func(context.Context) (int, error) { return 0, nil }, func(int, error) {})
			h.Flush()
		},
		"worker's own goroutine": func(h *kerneltest.Harness) {
			bad.ctx.Spawn(func(context.Context) {
				bad.ctx.Spawn(func(context.Context) { panic("worker's own goroutine") })
			})
			h.WaitPosts(1)
		},
	}
	for name, trigger := range cases {
		t.Run(name, func(t *testing.T) {
			heard = nil
			h := kerneltest.Start(t, kerneltest.Config{Lenient: true}, bad, good)
			trigger(h)
			st := state(t, h, "bad")
			var pe *kernel.PanicError
			if st.State != kernel.Failed || !errors.As(st.Err, &pe) || pe.Value != name {
				t.Fatalf("bad status %+v", st)
			}
			if state(t, h, "good").State != kernel.Active {
				t.Fatal("an unrelated plugin was taken down")
			}
			kernel.Emit(h, ping, "after")
			if !slices.Contains(heard, "after") {
				t.Fatal("good plugin stopped hearing events")
			}
			if err := h.Enable("bad"); err != nil || state(t, h, "bad").State != kernel.Active {
				t.Fatalf("re-enable failed plugin: %v", err)
			}
			closeClean(t, h)
		})
	}
}

func TestStrictRepanics(t *testing.T) {
	p := plug("p", nil)
	h := kerneltest.New(t, p)
	kernel.On(p.ctx, ping, func(string) { panic("strict") })
	defer func() {
		if recover() == nil {
			t.Fatal("strict kernel swallowed a panic")
		}
	}()
	kernel.Emit(h, ping, "x")
}

func TestEvents(t *testing.T) {
	p := plug("p", nil)
	h := kerneltest.New(t, p)
	var log []string
	var off func()
	kernel.On(p.ctx, ping, func(v string) {
		log = append(log, "first "+v)
		kernel.On(p.ctx, ping, func(v string) { log = append(log, "late "+v) })
		off()
	})
	off = kernel.On(p.ctx, ping, func(v string) { log = append(log, "second "+v) })
	// The first listener unsubscribes the second, which then doesn't run, and
	// subscribes a late one, which waits for the next Emit.
	kernel.Emit(h, ping, "1")
	if !slices.Equal(log, []string{"first 1"}) {
		t.Fatalf("emit 1: %q", log)
	}
	log = nil
	kernel.Emit(h, ping, "2")
	if !slices.Contains(log, "late 2") || slices.Contains(log, "second 2") {
		t.Fatalf("emit 2: %q", log)
	}
	closeClean(t, h)
}

func TestPipelineOrderAndRejection(t *testing.T) {
	a := plug("a", func(c *kernel.Context) error {
		kernel.Transform(c, outgoing, 20, func(s string) (string, error) { return s + "-a20", nil })
		kernel.Transform(c, outgoing, 10, func(s string) (string, error) { return s + "-a10", nil })
		return nil
	})
	b := plug("b", func(c *kernel.Context) error {
		kernel.Transform(c, outgoing, 10, func(s string) (string, error) {
			if s == "reject-a10" {
				return "", errors.New("too long")
			}
			return s + "-b10", nil
		})
		return nil
	})
	h := kerneltest.New(t, a, b)
	if v, err := kernel.Run(h, outgoing, "x"); err != nil || v != "x-a10-b10-a20" {
		t.Fatalf("run = %q, %v", v, err)
	}
	_, err := kernel.Run(h, outgoing, "reject")
	var pe *kernel.PluginError
	if !errors.As(err, &pe) || pe.Plugin != "b" {
		t.Fatalf("rejection = %v", err)
	}
	h.Disable("b")
	if v, _ := kernel.Run(h, outgoing, "x"); v != "x-a10-a20" {
		t.Fatalf("after disable = %q", v)
	}
	closeClean(t, h)
}

func TestPoints(t *testing.T) {
	a := plug("a", func(c *kernel.Context) error {
		kernel.Contribute(c, sidebar, "a.late", 5, "a-late")
		return kernel.Contribute(c, sidebar, "a.first", 0, "a-first")
	})
	b := plug("b", func(c *kernel.Context) error {
		return kernel.Contribute(c, sidebar, "b.first", 0, "b-first")
	})
	h := kerneltest.New(t, a, b)
	if err := kernel.Contribute(h, sidebar, "app.host", 0, "host"); err != nil {
		t.Fatal(err)
	}
	ids := func() []string {
		var out []string
		for _, e := range kernel.Items(h, sidebar) {
			out = append(out, e.ID)
		}
		return out
	}
	if got := ids(); !slices.Equal(got, []string{"app.host", "a.first", "b.first", "a.late"}) {
		t.Fatalf("order %q", got)
	}
	first, second := kernel.Items(h, sidebar), kernel.Items(h, sidebar)
	if &first[0] != &second[0] {
		t.Fatal("Items rebuilt an unchanged point")
	}
	for _, bad := range []string{"b.stolen", "a.", "a.first"} {
		if err := kernel.Contribute(a.ctx, sidebar, bad, 0, "x"); err == nil {
			t.Errorf("Contribute(%q) accepted", bad)
		}
	}
	h.Disable("a")
	if got := ids(); !slices.Equal(got, []string{"app.host", "b.first"}) {
		t.Fatalf("after disable %q", got)
	}
	closeClean(t, h)
}

func TestSettingsPersist(t *testing.T) {
	store := kerneltest.NewStore()
	var size *kernel.Setting[int]
	newPlugins := func() []kernel.Plugin {
		p := plug("p", func(c *kernel.Context) error {
			size = kernel.NewSetting(c, "size", 12)
			return nil
		})
		opt := plug("opt", nil)
		opt.m.DefaultOff = true
		locked := plug("locked", nil)
		locked.m.Locked = true
		return []kernel.Plugin{p, opt, locked}
	}
	h := kerneltest.Start(t, kerneltest.Config{Store: store}, newPlugins()...)
	if size.Get() != 12 {
		t.Fatal("default not returned")
	}
	size.Set(14)
	size.Set(14) // unchanged values aren't written again
	if err := h.Disable("p"); err != nil {
		t.Fatal(err)
	}
	if err := h.Enable("opt"); err != nil {
		t.Fatal(err)
	}
	if err := h.Disable("locked"); err == nil {
		t.Fatal("locked plugin disabled")
	}
	h.Flush()
	closeClean(t, h)

	h = kerneltest.Start(t, kerneltest.Config{Store: store}, newPlugins()...)
	if state(t, h, "p").State != kernel.Disabled || state(t, h, "opt").State != kernel.Active {
		t.Fatal("enablement not restored")
	}
	if err := h.Enable("p"); err != nil {
		t.Fatal(err)
	}
	if size.Get() != 14 {
		t.Fatalf("size = %d after disable and restart", size.Get())
	}
	closeClean(t, h)
}

func TestSettingsSaveCoalescesAndRetries(t *testing.T) {
	store := kerneltest.NewStore()
	p := plug("p", nil)
	h := kerneltest.Start(t, kerneltest.Config{Store: store}, p)
	var failures []error
	kernel.On(h, kernel.SaveFailed, func(err error) { failures = append(failures, err) })
	s := p.ctx.Settings()
	s.Set("a", 1) // starts a save
	s.Set("b", 2) // waits for it
	s.Set("b", 3)
	if h.Work() != 1 {
		t.Fatalf("%d saves queued, want 1 in flight", h.Work())
	}
	h.Flush()
	if store.Saves() != 2 || string(store.Raw("p", "b")) != "3" {
		t.Fatalf("saves=%d b=%s", store.Saves(), store.Raw("p", "b"))
	}
	store.Fail = errors.New("disk full")
	s.Set("c", 4)
	h.Flush()
	if len(failures) != 1 {
		t.Fatalf("failures %v", failures)
	}
	store.Fail = nil
	s.Delete("a")
	h.Flush()
	if store.Raw("p", "c") == nil || store.Raw("p", "a") != nil {
		t.Fatal("failed change was not retried with the next save")
	}
	closeClean(t, h)
}

func TestCloseWritesUnsavedSettingsAndWaitsForWorkers(t *testing.T) {
	store := kerneltest.NewStore()
	stopped := make(chan struct{})
	p := plug("p", func(c *kernel.Context) error {
		c.Spawn(func(ctx context.Context) { <-ctx.Done(); close(stopped) })
		return nil
	})
	h := kerneltest.Start(t, kerneltest.Config{Store: store}, p)
	p.ctx.Settings().Set("k", "v")
	closeClean(t, h)
	select {
	case <-stopped:
	default:
		t.Fatal("Close returned before the worker stopped")
	}
	if string(store.Raw("p", "k")) != `"v"` {
		t.Fatalf("unsaved setting lost: %s", store.Raw("p", "k"))
	}
}

func TestPluginsChangedReportsTransitions(t *testing.T) {
	p := plug("p", nil)
	h := kerneltest.New(t, p)
	var states []kernel.State
	kernel.On(h, kernel.PluginsChanged, func(all []kernel.Status) { states = append(states, all[0].State) })
	h.Disable("p")
	h.Enable("p")
	if !slices.Equal(states, []kernel.State{kernel.Disabled, kernel.Active}) {
		t.Fatalf("states %v", states)
	}
	closeClean(t, h)
}

func TestDisposedSpawnPanicDoesNotFailCurrentPlugin(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(fmt.Sprint("restart=", restart), func(t *testing.T) {
			p := plug("p", nil)
			h := kerneltest.Start(t, kerneltest.Config{Lenient: true}, p)
			s := p.ctx.NewScope()
			release := make(chan struct{})
			s.Spawn(func(context.Context) { <-release; panic("obsolete worker") })
			if restart {
				if err := h.Disable("p"); err != nil {
					t.Fatal(err)
				}
				if err := h.Enable("p"); err != nil {
					t.Fatal(err)
				}
			} else {
				s.Dispose()
			}
			close(release)
			h.WaitPosts(1)
			if st := state(t, h, "p"); st.State != kernel.Active {
				t.Fatalf("disposed worker failed current plugin: %+v", st)
			}
		})
	}
}

func TestCloseRetriesFailedSaveBeforeItsCompletionIsApplied(t *testing.T) {
	for _, newer := range []string{"", "update", "delete"} {
		t.Run(newer, func(t *testing.T) {
			store := kerneltest.NewStore()
			p := plug("p", nil)
			h := kerneltest.Start(t, kerneltest.Config{Store: store}, p)
			store.Fail = errors.New("temporary failure")
			p.ctx.Settings().Set("value", 42)
			h.RunWork(0) // failure is queued, but the UI has not applied it
			switch newer {
			case "update":
				p.ctx.Settings().Set("value", 43)
			case "delete":
				p.ctx.Settings().Delete("value")
			}
			store.Fail = nil
			if err := h.Kernel.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			want := "42"
			if newer == "update" {
				want = "43"
			}
			if newer == "delete" {
				want = ""
			}
			if got := string(store.Raw("p", "value")); got != want {
				t.Fatalf("saved %q, want %q", got, want)
			}
			h.RunPosts() // late completion cannot overwrite the shutdown save
			if got := string(store.Raw("p", "value")); got != want {
				t.Fatalf("late completion saved %q", got)
			}
		})
	}
}

func TestCloseReportsPersistentSaveFailure(t *testing.T) {
	store := kerneltest.NewStore()
	p := plug("p", nil)
	h := kerneltest.Start(t, kerneltest.Config{Store: store}, p)
	failure := errors.New("disk full")
	store.Fail = failure
	p.ctx.Settings().Set("value", 42)
	h.RunWork(0)
	if err := h.Kernel.Close(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("Close returned %v, want %v", err, failure)
	}
}

func TestPointChangesAreAnnouncedOncePerRound(t *testing.T) {
	var got []string
	watcher := plug("watch", func(c *kernel.Context) error {
		kernel.On(c, kernel.PointChanged, func(name string) { got = append(got, name) })
		return nil
	})
	p := plug("p", nil)
	h := kerneltest.New(t, watcher, p)
	h.Flush()
	got = nil
	scope := p.ctx.NewScope()
	for _, id := range []string{"p.a", "p.b"} {
		if err := kernel.Contribute(scope, sidebar, id, 0, id); err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 0 {
		t.Fatal("the change was announced before the round ended")
	}
	h.Flush()
	if !slices.Equal(got, []string{sidebar.Name()}) {
		t.Fatalf("announced %q", got)
	}
	scope.Dispose()
	h.Flush()
	if !slices.Equal(got, []string{sidebar.Name(), sidebar.Name()}) || len(kernel.Items(h, sidebar)) != 0 {
		t.Fatalf("after removal: announced %q", got)
	}
}

// A provider that fails while a dependent's Setup runs, as when the dependent
// emits to a listener of the provider that panics, leaves the dependent
// Blocked, not active without what it requires.
func TestProviderFailingDuringSetupBlocksTheDependent(t *testing.T) {
	provider := backend("provider")
	panicked := false
	provider.setup = func(c *kernel.Context) error {
		kernel.On(c, ping, func(string) {
			if !panicked {
				panicked = true
				panic("provider's listener")
			}
		})
		return kernel.Provide[agent](c, agentKey, named("provider"))
	}
	var got agent
	dependent := consumer("dependent", &got)
	inner := dependent.setup
	dependent.setup = func(c *kernel.Context) error {
		if err := inner(c); err != nil {
			return err
		}
		kernel.Emit(c, ping, "hello")
		return kernel.Contribute(c, sidebar, "dependent.item", 0, "item")
	}
	h := kerneltest.Start(t, kerneltest.Config{Lenient: true}, provider, dependent)
	if st := state(t, h, "provider"); st.State != kernel.Failed {
		t.Fatalf("provider %+v", st)
	}
	if st := state(t, h, "dependent"); st.State != kernel.Blocked || !slices.Equal(st.Missing, []string{agentKey.Name()}) {
		t.Fatalf("dependent %+v", st)
	}
	if items := kernel.Items(h, sidebar); len(items) != 0 {
		t.Fatalf("the blocked dependent kept its contributions: %v", items)
	}
	if err := h.Enable("provider"); err != nil {
		t.Fatal(err)
	}
	if st := state(t, h, "dependent"); st.State != kernel.Active {
		t.Fatalf("dependent after the provider came back %+v", st)
	}
	closeClean(t, h)
}

func TestNameDeclaredWithTwoTypesPanics(t *testing.T) {
	kernel.NewPoint[string]("test.twice")
	kernel.NewPoint[string]("test.twice") // the same type again is fine
	kernel.NewTopic[int]("test.twice")    // so is another kind
	defer func() {
		if v := recover(); v == nil || !strings.Contains(fmt.Sprint(v), `point "test.twice"`) {
			t.Fatalf("recovered %v", v)
		}
	}()
	kernel.NewPoint[int]("test.twice")
}
