package shell

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
)

// App describes the app Run opens.
type App struct {
	// Name ends the window title, as "Page — Name".
	Name string
	// Folder names the app's data folder in the user's config directory; a
	// development run (go run, mygo dev) keeps its data in Folder + "-dev".
	Folder string
	// Env, when set, prefixes the environment variables Run reads; with
	// "APP": APP_DATA_DIR overrides where settings live; APP_STRICT=1
	// re-panics plugin failures; APP_CAPTURE=/path.png writes a screenshot
	// after startup (APP_CAPTURE_WAIT, default 4s) and quits;
	// APP_APPEARANCE=light|dark fixes the palette without saving it.
	Env string
	// Width and Height size the window the first time it opens; MinWidth
	// and MinHeight bound it.
	Width, Height, MinWidth, MinHeight int
	// Menus are the menus of the menu bar between the app menu and Window,
	// in order (Options.Menus).
	Menus []string
}

// Run opens the app window with the shell and plugins, and returns when the
// app quits.
func Run(app App, plugins ...kernel.Plugin) {
	if app.Name == "" || app.Folder == "" {
		log.Fatal("shell: the App needs a Name and a Folder")
	}
	env := func(name string) string {
		if app.Env == "" {
			return ""
		}
		return os.Getenv(app.Env + "_" + name)
	}
	var store kernel.Store = &memoryStore{}
	var storeErr error
	var db *sqliteStore
	dir, err := dataDir(app.Folder, env("DATA_DIR"))
	if err != nil {
		storeErr, dir = err, ""
	} else if db, err = openStore(filepath.Join(dir, "settings.db")); err != nil {
		storeErr = err
	} else {
		store = db
	}

	mygo.App.WhenReady(func() {
		var s *Shell
		win := mygo.NewWindow(mygo.WindowOptions{
			Title: app.Name, Width: app.Width, Height: app.Height, MinWidth: app.MinWidth, MinHeight: app.MinHeight,
			TitleBarStyle:        mygo.TitleBarHidden,
			TrafficLightPosition: &mygo.Point{X: 14, Y: trafficLightY},
			// The page paints the strip and panel translucent for a clear or
			// frosted backdrop (see Host.SetBackdrop), and opaque otherwise.
			Transparent: true,
			StateKey:    "main",
			Content:     ui.View(func(c *ui.Context) { s.View(c) }),
		})
		var err error
		s, err = New(Options{Name: app.Name, Menus: app.Menus, Post: win.Update, Store: store, Strict: env("STRICT") == "1",
			SetTitle: win.SetTitle, SetMenu: mygo.App.SetMenu, SetTheme: mygo.Theme.SetSource,
			SetVibrancy: func(on bool) {
				if on {
					win.SetVibrancy(mygo.VibrancyUnderWindow)
				} else {
					win.SetVibrancy(mygo.VibrancyNone)
				}
			},
			Notify: notifier(), Clipboard: readClipboard, DataDir: dir, Foreground: func() bool { return win.IsVisible() && !win.IsMinimized() && win.IsFocused() },
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
		if mode := env("APPEARANCE"); mode != "" {
			s.host.applyTheme(mode) // for captures; not saved
		}
		if out := env("CAPTURE"); out != "" {
			wait := 4 * time.Second
			if d, err := time.ParseDuration(env("CAPTURE_WAIT")); err == nil {
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

// readClipboard reads local files, or else an image, off the clipboard.
func readClipboard() shell.Clipboard {
	if files, err := mygo.Clipboard.ReadFiles(); err == nil && len(files) > 0 {
		return shell.Clipboard{Files: files}
	}
	return shell.Clipboard{PNG: mygo.Clipboard.ReadImage()}
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
