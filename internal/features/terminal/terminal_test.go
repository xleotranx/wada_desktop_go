package terminal

import (
	"context"
	term "github.com/egoist/mygo/plugins/terminal"
	"testing"
	"wada-desktop-go/internal/feature"
)

func TestClosePreventsQueuedShellStartup(t *testing.T) {
	application, cancel := context.WithCancel(context.Background())
	defer cancel()
	var worker func() func()
	host := feature.Host{Lifetime: application, Context: func() feature.Context { return feature.Context{Work: application} }, Reveal: func() {}, Background: func(work func() func()) { worker = work }}
	module := New(host).(*Module)
	starts := 0
	module.NewTerminal = func(term.Options) (*term.Terminal, error) { starts++; return nil, nil }
	module.Toggle()
	module.Close()
	worker()()
	if starts != 0 || len(module.Tabs) != 0 {
		t.Fatalf("closed terminal: starts=%d tabs=%d", starts, len(module.Tabs))
	}
	if application.Err() != nil {
		t.Fatal("closing the terminal closed the application")
	}
}
