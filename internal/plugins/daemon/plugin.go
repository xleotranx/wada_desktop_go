// Package daemon connects Wada's desktop shell to the headless wadad daemon
// over WebSocket RPC, providing all internal/contract/agent capabilities.
package daemon

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
	"wada-desktop-go/internal/client"
	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/terminal"
	"wada-desktop-go/internal/protocol"
)

const version = "0.1.0-dev"

var attempts atomic.Uint64

// Plugin connects Wada's UI kernel to a local or remote wadad daemon.
type Plugin struct {
	ctx        *kernel.Context
	host       shell.Host
	run        *kernel.Scope
	status     agent.Status
	managed    bool
	mu         sync.Mutex
	cli        *client.Client
	providers  providerForm
	activeHost *kernel.Setting[string]
	remote     remoteForm

	// dial overrides client.ConnectOrStartLocal in tests.
	dial func(context.Context) (*client.Client, protocol.DaemonStatusResponse, error)
}

func New() kernel.Plugin { return &Plugin{} }

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID:          "daemon",
		Title:       "Daemon",
		Description: "Connects Wada to its headless wadad coding-agent daemon.",
		// Runtime recovery is explicit; a failure must not replay initialization.
		Restart:  &kernel.Restart{},
		Requires: []kernel.Named{shell.HostKey},
		Provides: []kernel.Named{
			agent.ServiceKey,
			agent.ModelsKey,
			agent.ModesKey,
			agent.ChildrenKey,
			agent.SnapshotsKey,
			agent.FilesKey,
			agent.SkillsKey,
			agent.FileTypesKey,
			agent.ImagesKey,
			agent.RewindKey,
			agent.ChangesKey,
			agent.InteractionsKey,
			agent.HarnessesKey,
			agent.WorkersKey,
			agent.WorktreesKey,
			terminal.ServiceKey,
		},
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.host = kernel.Require(c, shell.HostKey)
	p.activeHost = kernel.NewSetting(c, "active-host", "")
	kernel.Provide[agent.Service](c, agent.ServiceKey, p)
	kernel.Provide[agent.Models](c, agent.ModelsKey, p)
	kernel.Provide[agent.Modes](c, agent.ModesKey, p)
	kernel.Provide[agent.Snapshots](c, agent.SnapshotsKey, p)
	kernel.Provide[agent.Children](c, agent.ChildrenKey, p)
	kernel.Provide[agent.Files](c, agent.FilesKey, p)
	kernel.Provide[agent.Skills](c, agent.SkillsKey, p)
	kernel.Provide[agent.FileTypes](c, agent.FileTypesKey, fileTypes{})
	kernel.Provide[agent.Images](c, agent.ImagesKey, p)
	kernel.Provide[agent.Rewinder](c, agent.RewindKey, p)
	kernel.Provide[agent.Changes](c, agent.ChangesKey, p)
	kernel.Provide[agent.Interactions](c, agent.InteractionsKey, p)
	kernel.Provide[agent.Harnesses](c, agent.HarnessesKey, p)
	kernel.Provide[agent.Workers](c, agent.WorkersKey, p)
	kernel.Provide[agent.Worktrees](c, agent.WorktreesKey, p)
	kernel.Provide[terminal.Service](c, terminal.ServiceKey, terminalService{p: p})
	c.OnDispose(p.drop)
	kernel.Contribute(c, kernel.Commands, "daemon.restart", 0, kernel.Command{
		Title: "Restart Daemon",
		Menu:  "Agent",
		Run:   p.Restart,
	})
	p.contributeSettings(c)
	p.loadHosts()
	kernel.Contribute(c, chat.Requirements, "daemon.provider-setup", 0, chat.Requirement{
		Needed: p.providerNeeded,
		Render: p.providerSetup,
	})
	p.connect(false)
	return nil
}

func (p *Plugin) Status() agent.Status { return p.status }

func (p *Plugin) setStatus(st agent.Status) {
	p.status = st
	kernel.Emit(p.ctx, agent.StatusChanged, st)
}

func (p *Plugin) takeClient() *client.Client {
	p.mu.Lock()
	cli := p.cli
	p.cli = nil
	p.mu.Unlock()
	return cli
}

func (p *Plugin) drop() {
	if cli := p.takeClient(); cli != nil {
		_ = cli.Close()
	}
}

func (p *Plugin) current() (*client.Client, error) {
	p.mu.Lock()
	cli := p.cli
	p.mu.Unlock()
	if cli == nil {
		return nil, agent.ErrNotConnected
	}
	return cli, nil
}

// Restart shuts down an attached desktop-managed daemon (if any) and establishes
// a fresh daemon connection.
func (p *Plugin) Restart() { p.connect(true) }

// Reconnect drops and re-establishes the WebSocket session without stopping the daemon.
func (p *Plugin) Reconnect() { p.connect(false) }

func (p *Plugin) connect(restartManaged bool) {
	p.resetLoginState()
	p.providers.load.Cancel()
	p.providers.list = nil
	p.providers.loaded = false
	p.providers.err = ""

	wasManaged := p.managed
	p.managed = false
	p.drop()
	if p.run != nil {
		p.run.Dispose()
	}

	p.run = p.ctx.NewScope()
	run := p.run
	dataDir := p.host.DataDir()
	dialFn := p.dial
	hostName := ""
	if p.activeHost != nil {
		hostName = strings.TrimSpace(p.activeHost.Get())
	}
	remotePW := p.remote.connectPassword

	run.Spawn(func(ctx context.Context) {
		if restartManaged && wasManaged && dialFn == nil && hostName == "" {
			p.stopManagedDaemon(ctx, dataDir)
		}

		backoffs := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 5 * time.Second}
		failStreak := 0

		endpointLabel := "Local wadad"
		if hostName != "" {
			endpointLabel = hostName
		}

		for {
			if ctx.Err() != nil {
				return
			}
			attempt := attempts.Add(1)
			connectingSt := agent.Status{
				State:    agent.Connecting,
				Attempt:  attempt,
				Endpoint: endpointLabel,
			}
			run.Post(func() { p.setStatus(connectingSt) })

			dialCtx, dialCancel := context.WithTimeout(ctx, 15*time.Second)
			var (
				cli        *client.Client
				daemonStat protocol.DaemonStatusResponse
				err        error
			)
			switch {
			case dialFn != nil:
				cli, daemonStat, err = dialFn(dialCtx)
			case hostName != "":
				cli, err = client.DialHost(dialCtx, dataDir, hostName, client.Options{
					ClientType: "desktop",
					AppVersion: version,
					Password:   remotePW,
				})
				if err == nil {
					daemonStat, err = cli.Status(dialCtx)
					if err != nil {
						_ = cli.Close()
						cli = nil
					}
				}
			default:
				cli, daemonStat, err = client.ConnectOrStartLocal(dialCtx, dataDir, version, client.Options{
					ClientType: "desktop",
					AppVersion: version,
				})
			}
			dialCancel()

			if err != nil {
				if ctx.Err() != nil {
					return
				}
				downSt := connectingSt
				downSt.State = agent.Down
				downSt.Err = err
				run.Post(func() { p.setStatus(downSt) })
				wait := backoffs[min(failStreak, len(backoffs)-1)]
				failStreak++
				if !sleepContext(ctx, wait) {
					return
				}
				continue
			}

			subID, subCh, err := cli.Subscribe(ctx)
			if err != nil {
				_ = cli.Close()
				if ctx.Err() != nil {
					return
				}
				downSt := connectingSt
				downSt.State = agent.Down
				downSt.Err = err
				run.Post(func() { p.setStatus(downSt) })
				wait := backoffs[min(failStreak, len(backoffs)-1)]
				failStreak++
				if !sleepContext(ctx, wait) {
					return
				}
				continue
			}

			failStreak = 0
			p.mu.Lock()
			if ctx.Err() != nil {
				p.mu.Unlock()
				_ = cli.Close()
				return
			}
			p.cli = cli
			p.mu.Unlock()

			info := cli.ServerInfo()
			endpoint := strings.TrimSpace(info.Hostname)
			if endpoint == "" {
				endpoint = "Local wadad"
			}
			readySt := connectingSt
			if daemonStat.Origin != "" {
				readySt.Origin = daemonStat.Origin
			} else {
				readySt.Origin = info.Origin
			}
			if daemonStat.Version != "" {
				readySt.Version = daemonStat.Version
			} else {
				readySt.Version = info.Version
			}
			readySt.Endpoint = endpoint
			readySt.State = agent.Ready
			managed := daemonStat.DesktopManaged

			run.Post(func() {
				p.managed = managed
				p.setStatus(readySt)
				p.loadProviders()
			})

			p.pump(ctx, run, subCh)

			p.mu.Lock()
			if p.cli == cli {
				p.cli = nil
			}
			p.mu.Unlock()
			if subID != "" {
				uc, ucancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
				_ = cli.Unsubscribe(uc, subID)
				ucancel()
			}
			_ = cli.Close()

			if ctx.Err() != nil {
				return
			}

			lostSt := readySt
			lostSt.State = agent.Down
			lostSt.Err = errors.New("connection to wadad lost")
			run.Post(func() {
				p.managed = false
				p.setStatus(lostSt)
			})
			if !sleepContext(ctx, backoffs[0]) {
				return
			}
		}
	})
}

func (p *Plugin) stopManagedDaemon(ctx context.Context, dataDir string) {
	rec, _, err := client.ReadLocalEndpoint(dataDir)
	if err != nil || !rec.DesktopManaged {
		return
	}
	sctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	cli, derr := client.DialLocal(sctx, dataDir, client.Options{
		ClientType: "desktop",
		AppVersion: version,
	})
	if derr == nil {
		_ = cli.Shutdown(sctx)
		_ = cli.Close()
	}
	cancel()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, rerr := client.ReadLocalEndpoint(dataDir); rerr != nil {
			return
		}
		if !sleepContext(ctx, 50*time.Millisecond) {
			return
		}
	}
}

func sleepContext(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (p *Plugin) pump(ctx context.Context, run *kernel.Scope, queue <-chan protocol.Event) {
	flushBatch := func(batch []agent.Event) bool {
		if len(batch) == 0 {
			return true
		}
		applied := make(chan struct{})
		run.PostQuiet(func() { defer close(applied); kernel.Emit(p.ctx, agent.Events, batch) })
		select {
		case <-applied:
			return true
		case <-ctx.Done():
			return false
		}
	}

	dispatchControl := func(ev protocol.Event) {
		switch ev.Kind {
		case protocol.EventModelsChanged:
			run.Post(func() {
				kernel.Emit(p.ctx, agent.ModelsChanged, struct{}{})
				p.loadProviders()
			})
		case protocol.EventSessionDeleted:
			sess := agent.SessionID(ev.Session)
			run.Post(func() { kernel.Emit(p.ctx, agent.Deleted, sess) })
		case protocol.EventProviderAuth:
			authEv := ev.ProviderAuth
			run.Post(func() { p.handleProviderAuth(authEv) })
		case protocol.EventWorkerUpdated:
			if ev.Worker != nil {
				w := fromProtoWorker(*ev.Worker)
				run.Post(func() { kernel.Emit(p.ctx, agent.WorkerUpdated, w) })
			}
		case protocol.EventTerminalUpdated:
			if ev.Terminal != nil {
				info := fromProtoTerminal(*ev.Terminal)
				run.Post(func() { kernel.Emit(p.ctx, terminal.Updated, info) })
			}
		case protocol.EventTextStarted, protocol.EventTextDelta, protocol.EventTextEnded,
			protocol.EventToolStarted, protocol.EventToolCalled, protocol.EventToolFinished,
			protocol.EventApprovalRequested, protocol.EventInteractionRequested,
			protocol.EventTurnStarted, protocol.EventTurnSucceeded,
			protocol.EventTurnFailed, protocol.EventTurnInterrupted,
			protocol.EventModelChanged, protocol.EventSessionRenamed:
		}
	}

	isControl := func(kind protocol.EventKind) bool {
		switch kind {
		case protocol.EventModelsChanged, protocol.EventSessionDeleted, protocol.EventProviderAuth,
			protocol.EventWorkerUpdated, protocol.EventTerminalUpdated:
			return true
		case protocol.EventTextStarted, protocol.EventTextDelta, protocol.EventTextEnded,
			protocol.EventToolStarted, protocol.EventToolCalled, protocol.EventToolFinished,
			protocol.EventApprovalRequested, protocol.EventInteractionRequested,
			protocol.EventTurnStarted, protocol.EventTurnSucceeded,
			protocol.EventTurnFailed, protocol.EventTurnInterrupted,
			protocol.EventModelChanged, protocol.EventSessionRenamed:
			return false
		}
		return false
	}

	for {
		var (
			batch   []agent.Event
			pending *protocol.Event
		)
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-queue:
			if !ok {
				return
			}
			if isControl(ev.Kind) {
				dispatchControl(ev)
				continue
			}
			aev := fromProtoEvent(ev)
			batch = append(batch, aev)
			size := aev.Bytes

			timer := time.NewTimer(time.Second / 60)
		collect:
			for len(batch) < 64 && size < 256<<10 {
				select {
				case next, ok := <-queue:
					if !ok {
						timer.Stop()
						_ = flushBatch(batch)
						return
					}
					if isControl(next.Kind) {
						cp := next
						pending = &cp
						break collect
					}
					na := fromProtoEvent(next)
					batch = append(batch, na)
					size += na.Bytes
				case <-timer.C:
					break collect
				case <-ctx.Done():
					timer.Stop()
					return
				}
			}
			timer.Stop()
		}

		if !flushBatch(batch) {
			return
		}
		if pending != nil {
			dispatchControl(*pending)
		}
	}
}
