//go:build !wadareplay

package main

import "github.com/xleotranx/wada-kit/kernel"

// measured is empty in the app; builds tagged wadareplay add the replay plugin.
var measured []kernel.Factory
