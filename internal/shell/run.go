package shell

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"time"

	"wada-desktop-go/internal/kernel"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
)

// Run opens the app window with the shell and plugins, and returns when the
// app quits.
//
// Environment: WADA_DATA_DIR overrides where settings live; WADA_STRICT=1
// re-panics plugin failures; WADA_CAPTURE=/path.png writes a screenshot after
// startup and quits; WADA_APPEARANCE=light|dark fixes the palette.
func Run(plugins ...kernel.Plugin) {
	var store kernel.Store = &memoryStore{}
	var storeErr error
	var db *sqliteStore
	if dir, err := dataDir(); err != nil {
		storeErr = err
	} else if db, err = openStore(filepath.Join(dir, "settings.db")); err != nil {
		storeErr = err
	} else {
		store = db
	}

	mygo.App.WhenReady(func() {
		var s *Shell
		win := mygo.NewWindow(mygo.WindowOptions{
			Title: "Wada", Width: 1400, Height: 900, MinWidth: 900, MinHeight: 600,
			TitleBarStyle:        mygo.TitleBarHidden,
			TrafficLightPosition: &mygo.Point{X: 14, Y: trafficLightY},
			StateKey:             "main",
			Content:              ui.View(func(c *ui.Context) { s.View(c) }),
		})
		var err error
		s, err = New(Options{Post: win.Update, Store: store, Strict: os.Getenv("WADA_STRICT") == "1",
			SetTitle: win.SetTitle, SetMenu: mygo.App.SetMenu}, plugins...)
		if err != nil {
			log.Fatal(err)
		}
		if err := s.Start(); err != nil {
			log.Printf("settings: %v", err)
			storeErr = err
		}
		if storeErr != nil {
			s.host.Toast(ui.Toast{Title: "Settings won't be saved", Description: storeErr.Error(), Type: components.ToastError})
		}
		closed := false
		shutdown := func() {
			if closed {
				return
			}
			closed = true
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := s.Close(ctx); err != nil {
				log.Printf("shutdown: %v", err)
			}
			if db != nil {
				db.Close()
			}
		}
		win.OnClosed(shutdown)
		mygo.App.OnQuit(shutdown)
		switch os.Getenv("WADA_APPEARANCE") {
		case "light":
			mygo.Theme.SetSource(mygo.ThemeLight)
		case "dark":
			mygo.Theme.SetSource(mygo.ThemeDark)
		}
		if out := os.Getenv("WADA_CAPTURE"); out != "" {
			win.OnReadyToShow(func() {
				time.AfterFunc(4*time.Second, func() {
					win.Update(func() {
						if png, err := win.CapturePage(); err == nil {
							os.WriteFile(out, png, 0o644)
						} else {
							log.Printf("capture: %v", err)
						}
						mygo.App.Exit(0)
					})
				})
			})
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
