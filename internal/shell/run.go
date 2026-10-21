package shell

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"time"

	"wada-desktop-go/internal/contract/shell"
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
// startup (WADA_CAPTURE_WAIT, default 4s) and quits; WADA_APPEARANCE=light|dark
// fixes the palette without saving it.
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
			SetTitle: win.SetTitle, SetMenu: mygo.App.SetMenu, SetTheme: mygo.Theme.SetSource,
			Notify: notifier(), Foreground: func() bool { return win.IsVisible() && !win.IsMinimized() && win.IsFocused() },
		}, plugins...)
		if err != nil {
			log.Fatal(err)
		}
		mygo.App.OnNotificationClick(func(id string) {
			win.Update(func() {
				if win.IsMinimized() {
					win.Restore()
				}
				win.Show()
				win.Focus()
				s.NoticeClicked(id)
			})
		})
		// What the user sees in the app doesn't wait in Notification Center.
		mygo.App.OnDidBecomeActive(mygo.ClearNotifications)
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
		if mode := os.Getenv("WADA_APPEARANCE"); mode != "" {
			s.host.applyTheme(mode) // for captures; not saved
		}
		if out := os.Getenv("WADA_CAPTURE"); out != "" {
			wait := 4 * time.Second
			if d, err := time.ParseDuration(os.Getenv("WADA_CAPTURE_WAIT")); err == nil {
				wait = d
			}
			win.OnReadyToShow(func() {
				time.AfterFunc(wait, func() {
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

// notifier shows notices through the system, or is nil where it can't (on
// macOS, an app run with `go run` instead of from a bundle).
func notifier() func(shell.Notice) {
	if !mygo.NotificationsSupported() {
		return nil
	}
	return func(n shell.Notice) {
		// The first one may wait for the user to allow notifications.
		go func() {
			err := mygo.NewNotification(mygo.NotificationOptions{
				ID: n.ID, Title: n.Title, Subtitle: n.Subtitle, Body: n.Body, Group: n.ID,
			}).Show()
			if err != nil && !errors.Is(err, mygo.ErrNotificationsDenied) {
				log.Printf("notification: %v", err)
			}
		}()
	}
}
