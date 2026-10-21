package opencode

import (
	"context"
	"os"
	"path/filepath"
	"time"
	"wada-desktop-go/internal/workspace"
)

func (policy *Client) MonitorPolicy(ctx context.Context, dirs <-chan workspace.PolicyTarget, writes <-chan workspace.PolicyChange, publish func(workspace.PolicyResult)) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var target workspace.PolicyTarget
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
				info.ModTime().Equal(lastInfo.ModTime()) && info.Mode() == lastInfo.Mode()
		}
		if !force && dir == lastDir && unchanged {
			return
		}
		level, err := policy.ReadPolicy(ctx, dir)
		if err != nil {
			level = "ask"
		}
		if force || dir != lastDir || level != lastLevel {
			publish(workspace.PolicyResult{Dir: dir, Level: level, Revision: target.Revision})
		}
		lastDir, lastLevel = dir, level
		lastInfo, lastMissing = info, os.IsNotExist(statErr)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case target = <-dirs:
			dir = target.Dir
			refresh(true) // a returning workspace has reset its UI cache
		case change := <-writes:
			if err := policyWrite(ctx, policy, change.Dir, change.Ask); err != nil {
				publish(workspace.PolicyResult{Dir: change.Dir, Err: err, Revision: change.Revision})
			}
			refresh(true)
		case <-tick.C:
			refresh(false)
		}
	}
}

func policyWrite(ctx context.Context, p *Client, dir string, ask bool) error {
	_, err := p.WritePolicy(ctx, dir, ask)
	return err
}
