package coding

import (
	"context"
	"testing"
	"time"
	"wada-desktop-go/internal/feature"
	"wada-desktop-go/internal/workspace"
)

type listClient struct {
	workspace.Client
	hits int
}

func (c *listClient) ListDir(context.Context, string, string) ([]workspace.FileEntry, error) {
	c.hits++
	return []workspace.FileEntry{{Path: "example.txt", Name: "example.txt"}}, nil
}
func TestCloseCancelsModuleWorkWithoutClosingTheApplication(t *testing.T) {
	application, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &listClient{}
	var worker func() func()
	host := feature.Host{Lifetime: application, Context: func() feature.Context {
		return feature.Context{Directory: "/workspace", Workspace: client, Work: application}
	}, Background: func(work func() func()) { worker = work }}
	module := New(host).(*Module)
	module.OpenFiles()
	if worker == nil {
		t.Fatal("file read was not scheduled")
	}
	module.Close()
	worker()()
	if client.hits != 0 {
		t.Fatal("closed module performed a file read")
	}
	if application.Err() != nil {
		t.Fatal("closing a module closed the application")
	}
}
func TestLateFileReadCannotRestoreAClosedModule(t *testing.T) {
	application, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &listClient{}
	var worker func() func()
	host := feature.Host{Lifetime: application, Context: func() feature.Context {
		return feature.Context{Directory: "/workspace", Workspace: client, Work: application}
	}, Background: func(work func() func()) { worker = work }}
	module := New(host).(*Module)
	module.OpenFiles()
	publish := worker()
	module.Close()
	publish()
	if module.Files != nil {
		t.Fatal("late result restored the closed file state")
	}
}

type policyMonitor struct {
	workspace.Policy
	started, stopped chan struct{}
}

func (p *policyMonitor) MonitorPolicy(ctx context.Context, _ <-chan workspace.PolicyTarget, _ <-chan workspace.PolicyChange, _ func(workspace.PolicyResult)) {
	close(p.started)
	<-ctx.Done()
	close(p.stopped)
}
func TestPolicyMonitoringStartsOnlyThroughExplicitLifecycle(t *testing.T) {
	application, cancel := context.WithCancel(context.Background())
	defer cancel()
	monitor := &policyMonitor{started: make(chan struct{}), stopped: make(chan struct{})}
	host := feature.Host{Lifetime: application, Context: func() feature.Context {
		return feature.Context{Work: application, Local: true, IsDraft: true, Home: "/workspace", Policy: monitor}
	}, Post: func(fn func()) { fn() }}
	module := New(host).(*Module)
	module.Connected()
	if module.stopPolicy != nil {
		t.Fatal("connection notification started background monitoring")
	}
	module.Start()
	select {
	case <-monitor.started:
	case <-time.After(3 * time.Second):
		t.Fatal("explicit startup did not start monitoring")
	}
	module.Close()
	select {
	case <-monitor.stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("closing module did not stop monitoring")
	}
}
