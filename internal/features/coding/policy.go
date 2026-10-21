package coding

import (
	"context"
	"fmt"
	"time"

	"wada-desktop-go/internal/feature"
	"wada-desktop-go/internal/native"
	"wada-desktop-go/internal/workspace"

	"github.com/egoist/mygo/ui"
	onechat "github.com/oneo-me/onevra/source/components/chat"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

func (a *Module) WatchWorkspacePolicy(dir string) {
	if a.Policy.Dir == dir && a.Policy.Route == a.host.Context().Connection && a.Policy.Attempt == a.host.Context().Attempt && a.Policy.Remote == !a.host.Context().Local {
		return
	}
	a.Policy.Request.Stop()
	a.Policy.Version++
	a.Policy.Route = a.host.Context().Connection
	a.Policy.Attempt = a.host.Context().Attempt
	a.Policy.Remote = !a.host.Context().Local
	a.Policy.Dir, a.Policy.Level = dir, "ask"
	select {
	case <-a.Policy.Dirs:
	default:
	}
	a.Policy.Dirs <- workspace.PolicyTarget{Dir: dir, Revision: a.Policy.Version}
}

func (a *Module) AcceptPolicy(result workspace.PolicyResult) {
	if a.closed || result.Revision != a.Policy.Version || result.Dir != a.Policy.Dir {
		return
	}
	if result.Err != nil {
		a.host.Note("The shell policy did not change: " + result.Err.Error())
		return
	}
	if result.Dir == a.Policy.Dir {
		a.Policy.Level = result.Level
	}
}

// watchServerPolicy reads the shell level of one server workspace once,
// over its API: nothing of this machine to watch, so the pill leans ask
// until the server answers, and a fresh ask lands when the chat moves.
func (a *Module) WatchServerPolicy(dir string) {
	if a.Policy.Dir == dir && a.Policy.Route == a.host.Context().Connection && a.Policy.Attempt == a.host.Context().Attempt && a.Policy.Remote == !a.host.Context().Local {
		return
	}
	a.Policy.Request.Stop()
	a.Policy.Version++
	a.Policy.Route = a.host.Context().Connection
	a.Policy.Attempt = a.host.Context().Attempt
	a.Policy.Remote = !a.host.Context().Local
	a.Policy.Dir, a.Policy.Level = dir, "ask"
	select {
	case <-a.Policy.Dirs:
	default:
	}
	a.Policy.Dirs <- workspace.PolicyTarget{Revision: a.Policy.Version}
	if dir == "" {
		return
	}
	policy := a.host.Context().Policy
	feature.Run(a.host, &a.Policy.Request, func(ctx context.Context) (string, error) {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if policy == nil {
			return "", fmt.Errorf("this backend does not support workspace policy")
		}
		return policy.ReadPolicy(ctx, dir)
	}, func(level string, err error) {
		if err == nil && a.Policy.Dir == dir {
			a.Policy.Level = level
		}
	})
}

// permissionLevel reads the workspace's json: how bash actions
// run in it, ask-first or open. A config the decoder cannot read shows
// itself as ask-first, the safe reading.
func (a *Module) SetAsk(ask bool) {
	dir := a.host.Context().Directory
	if !a.host.Context().Local {
		if dir == "" {
			// No folder of the server's to settle a rule in yet, and
			// naming one of this machine's would file the server a
			// phantom project.
			a.host.Note("The chat has no folder on the server yet, so the shell rule has nowhere to go.")
			return
		}
		a.Policy.Request.Stop()
		policy, version := a.host.Context().Policy, a.Policy.Version
		previous := a.Policy.Tail
		finished := make(chan struct{})
		a.Policy.Tail = finished
		a.Policy.WriteRevision++
		revision := a.Policy.WriteRevision
		feature.Run(a.host, new(feature.Request), func(ctx context.Context) (string, error) {
			defer close(finished)
			if previous != nil {
				select {
				case <-previous:
				case <-ctx.Done():
					return "", ctx.Err()
				}
			}
			ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			if policy == nil {
				return "", fmt.Errorf("this backend does not support workspace policy")
			}
			return policy.WritePolicy(ctx, dir, ask)
		}, func(level string, err error) {
			if a.Policy.WriteRevision == revision {
				a.AcceptPolicy(workspace.PolicyResult{Dir: dir, Level: level, Err: err, Revision: version})
			}
		})
		return
	}
	if dir == "" {
		dir = a.host.Context().Home
	}
	select {
	case a.Policy.Writes <- workspace.PolicyChange{Dir: dir, Ask: ask, Revision: a.Policy.Version}:
	default:
		a.host.Note("The shell policy is still being updated. Try again shortly.")
	}
}

func (a *Module) FullAccessIcon(c *ui.Context, p native.Palette) {
	dir := a.host.Context().Directory
	label := "Full access"
	if a.Policy.Level != "full" {
		label = "Asks first"
	}
	b := onechat.IconAction(c, label, lucide.ShieldCheck)
	b.Tooltip(label + " in " + dirBase(dir))
	b.Menu(a.AccessMenu())
}

// accessMenu is the ask-first/full-access choice the chip and the compact
// icon both open.
func (a *Module) AccessMenu() func(*ui.Menu) {
	return func(m *ui.Menu) {
		if m.Item("Asks first").Chosen() {
			a.SetAsk(true)
		}
		m.Separator()
		if m.Item("Full access").Chosen() {
			a.SetAsk(false)
		}
	}
}

// fullAccessButton shows how shell actions run in the chat's folder and
// moves the policy between ask-first and open through the workspace
// config the server watches.
func (a *Module) FullAccessButton(c *ui.Context, p native.Palette) {
	dir := a.host.Context().Directory
	label := "Full access"
	if a.Policy.Level != "full" {
		// A policy not yet read, or unreadable, leans to the safe name.
		label = "Asks first"
	}
	chip := onechat.Chip(c, label, onechat.ChipOptions{Icon: lucide.ShieldCheck, Caret: true})
	chip.Tooltip("How shell actions run in " + dirBase(dir))
	chip.Menu(a.AccessMenu())
}
