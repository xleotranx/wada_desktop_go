package workflow

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/sqlite"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
	"github.com/xleotranx/wada-kit/kittest"
	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	wf "wada-desktop-go/internal/contract/workflow"
	"wada-desktop-go/internal/plugintest"
)

func persistedFixture(t *testing.T, dir string) (*kerneltest.Harness, *Plugin, *plugintest.Agent, chan agent.Send) {
	t.Helper()
	a := plugintest.NewAgent()
	sent := make(chan agent.Send, 10)
	a.OnSend = func(req agent.Send) { sent <- req }
	var p *Plugin
	h := kerneltest.New(t, a.New, (&kittest.Host{Data: dir}).New, kerneltest.Keep(&p, func() *Plugin { return &Plugin{} }))
	if err := kernel.Contribute(h, wf.Definitions, kernel.HostPrefix+"test", 0, wf.Flow{ID: kernel.HostPrefix + "test", Plan: "plan", Code: "code", Route: "route", Review: "review", Final: "final", MaxReviews: 3}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !p.loaded && time.Now().Before(deadline) {
		h.Flush()
		time.Sleep(time.Millisecond)
	}
	if !p.loaded || p.loadError != "" {
		t.Fatalf("storage loaded=%v err=%s", p.loaded, p.loadError)
	}
	return h, p, a, sent
}
func TestRestartKeepsQueuedPayloadAndRequiresExplicitResume(t *testing.T) {
	dir := t.TempDir()
	h, p, _, sent := persistedFixture(t, dir)
	d := chat.Draft{Session: "main", Directory: t.TempDir(), Workflow: kernel.HostPrefix + "test", Mode: "full", Text: "implement", Files: []agent.File{{Name: "image.png", MIME: "image/png", Data: []byte{1, 2, 3}}}, Skills: []agent.SkillRef{{ID: "skill"}}}
	if err := p.Admit(d); err != nil {
		t.Fatal(err)
	}
	first := next(t, h, sent)
	if err := p.Admit(chat.Draft{Session: "main", Text: "why?", Files: d.Files}); err != nil {
		t.Fatal(err)
	}
	h.Close()
	h2, p2, _, sent2 := persistedFixture(t, dir)
	l := p2.lanes["main"]
	if l == nil || !l.Halted || l.task().State != "interrupted" || len(l.Queue) != 1 || len(l.Queue[0].Files[0].Data) != 3 {
		t.Fatalf("restored %+v", l)
	}
	h2.Flush()
	select {
	case req := <-sent2:
		t.Fatalf("restart sent %+v", req)
	default:
	}
	p2.resume(l, l.task())
	retry := next(t, h2, sent2)
	if retry.Session != first.Session || retry.ID == first.ID || len(retry.Files) != 1 || len(retry.Skills) != 1 {
		t.Fatalf("resume lost prepared input: %+v", retry)
	}
}
func TestWorkflowDatabaseRefusesUnknownSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workflow.db")
	db, err := sqlite.Open(t.Context(), path, sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Transaction(t.Context(), []sqlite.Statement{{SQL: "PRAGMA user_version=99"}}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if db, _, err := openStore(context.Background(), path); err == nil {
		db.Close()
		t.Fatal("unknown schema accepted")
	}
}
