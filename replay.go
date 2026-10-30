//go:build wadareplay

package main

import (
	"wada-desktop-go/internal/plugins/replay"

	"github.com/xleotranx/wada-kit/kernel"
)

// measured adds the replay plugin, which only measurement builds install
// (scripts/measure.sh).
var measured = []kernel.Factory{replay.New}
