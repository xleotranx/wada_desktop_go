//go:build !unix

package main

import "wada-desktop-go/internal/client"

// watchResize does nothing where the console sends no resize signal; the
// terminal keeps the size it was attached with.
func watchResize(int, *client.TerminalStream) (stop func()) { return func() {} }
