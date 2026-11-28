//go:build unix

package main

import (
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"
	"wada-desktop-go/internal/client"
)

// watchResize sends the local terminal's size to stream whenever the window
// changes, until the returned stop is called.
func watchResize(fd int, stream *client.TerminalStream) (stop func()) {
	if !term.IsTerminal(fd) {
		return func() {}
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGWINCH)
	quit := make(chan struct{})
	go func() {
		for {
			select {
			case <-quit:
				return
			case <-sig:
				if w, h, err := term.GetSize(fd); err == nil {
					_ = stream.Resize(w, h)
				}
			}
		}
	}()
	return func() {
		signal.Stop(sig)
		close(quit)
	}
}
