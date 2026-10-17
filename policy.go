package main

import (
	"context"
	"time"
)

type policyChange struct {
	dir string
	ask bool
}

type policyResult struct {
	dir, level string
	err        error
}

// watchPolicy keeps filesystem work away from rendering. Polling also
// notices edits made outside the app, including a newly created config.
func watchPolicy(ctx context.Context, dirs <-chan string, writes <-chan policyChange, publish func(policyResult)) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var dir, lastDir, lastLevel string
	refresh := func() {
		if dir == "" {
			return
		}
		level, err := permissionLevel(dir)
		if err != nil {
			level = "ask"
		}
		if dir != lastDir || level != lastLevel {
			lastDir, lastLevel = dir, level
			publish(policyResult{dir: dir, level: level})
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case dir = <-dirs:
			lastDir = "" // a returning workspace may have reset its UI cache
			refresh()
		case change := <-writes:
			if err := setPermissionAsk(change.dir, change.ask); err != nil {
				publish(policyResult{dir: change.dir, err: err})
			}
			refresh()
		case <-tick.C:
			refresh()
		}
	}
}

func (a *app) watchWorkspacePolicy(dir string) {
	if a.policyDir == dir {
		return
	}
	a.policyDir, a.policyLevel = dir, "ask"
	select {
	case <-a.policyDirs:
	default:
	}
	a.policyDirs <- dir
}

func (a *app) acceptPolicy(result policyResult) {
	if result.err != nil {
		a.streamLine("The shell policy did not change: " + result.err.Error())
		return
	}
	if result.dir == a.policyDir {
		a.policyLevel = result.level
	}
}
