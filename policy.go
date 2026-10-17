package main

import (
	"context"
	"os"
	"path/filepath"
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
	var lastInfo os.FileInfo
	var lastMissing bool
	refresh := func(force bool) {
		if dir == "" {
			return
		}
		info, statErr := os.Stat(filepath.Join(dir, "opencode.json"))
		unchanged := os.IsNotExist(statErr) && lastMissing
		if statErr == nil && lastInfo != nil {
			unchanged = os.SameFile(info, lastInfo) && info.Size() == lastInfo.Size() &&
				info.ModTime() == lastInfo.ModTime() && info.Mode() == lastInfo.Mode()
		}
		if !force && dir == lastDir && unchanged {
			return
		}
		level, err := permissionLevel(dir)
		if err != nil {
			level = "ask"
		}
		if force || dir != lastDir || level != lastLevel {
			publish(policyResult{dir: dir, level: level})
		}
		lastDir, lastLevel = dir, level
		lastInfo, lastMissing = info, os.IsNotExist(statErr)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case dir = <-dirs:
			refresh(true) // a returning workspace has reset its UI cache
		case change := <-writes:
			if err := setPermissionAsk(change.dir, change.ask); err != nil {
				publish(policyResult{dir: change.dir, err: err})
			}
			refresh(true)
		case <-tick.C:
			refresh(false)
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
