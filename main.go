package main

import (
	"log"
	"os"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// block is one paragraph, bubble or activity line of the transcript.
type block struct {
	kind   string // "text", "user", "stream" or "activity"
	text   string
	icon   *ui.SVG
	key    string                         // key into app.expanded for collapsible lines
	detail func(c *ui.Context, p palette) // drawn below the line once expanded
}

// sideFolder is a section entry of the sidebar with its chats.
type sideFolder struct {
	name  string
	tasks []string
}

// app is the state the window shows.
type app struct {
	selected string
	sidebarW float32

	// expanded holds collapsible lines ("activity:…"), folder rows
	// ("folder:…") and section titles ("pinned", "projects", "recents").
	expanded map[string]bool

	draft string

	pinned  string
	folders []sideFolder

	feed   ui.ScrollState
	blocks []block
}

func newApp() *app {
	wadaPi := sideFolder{"wada_pi_webapp", []string{
		"Implement PD-09 outcomes and retries",
		"Start PD-08",
		"Build trusted parallel agent flows",
		"Brainstorm Wada app improvements",
		"Review code changes",
	}}
	a := &app{
		sidebarW: 340,
		expanded: map[string]bool{
			"folder:wada_pi_webapp": true,
			"activity:viewed-image": true,
		},
		selected: "Implement PD-09 outcomes and retries",
		pinned:   "Compare OpenCode dev bot options",
		folders: []sideFolder{
			wadaPi,
			{"wada_automation", []string{"Nightly digest job", "Watch releases of mygo"}},
			{"wada-starter-hono", []string{"Scaffold the starter", "Wire the OAuth flow"}},
			{"wada_social", []string{"Draft the first post"}},
		},
		blocks: memoTranscript(),
	}
	return a
}

// view lays the window out: a sidebar, a hairline beside it and the
// transcript of the chosen chat, with the composer at its foot.
func (a *app) view(c *ui.Context) {
	p := paletteFor(c)
	bar := c.TitleBar()
	// The window controls are centered in a strip of bar.Height, so the
	// top rows take the same height: traffic lights, nav buttons and
	// the chat title share one center line.
	headerH := bar.Height
	if headerH < 48 {
		headerH = 48
	}

	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		a.sidebarPane(c, p, bar, headerH)
		ui.Box(c).Width(1).FillHeight().Background(p.hairline)
		a.contentPane(c, p, headerH)
	})
}

func main() {
	a := newApp()
	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:     "Wada",
			Width:     1400,
			Height:    920,
			MinWidth:  980,
			MinHeight: 640,
			// The window draws its own title bar under the traffic lights.
			TitleBarStyle: mygo.TitleBarHidden,
			// Center the traffic lights in the header: the close button
			// is 14 DIP tall, so Y = header/2 - 7 puts the lights on the
			// same center line the top rows use (TitleBar().Height is
			// button height + 2*Y).
			TrafficLightPosition: &mygo.Point{X: 14, Y: 25},
			StateKey:             "main",
			Content:              ui.View(a.view),
		})

		// WADA_CAPTURE=/path/out.png writes a PNG of the window and quits:
		// a way to see the interface without clicking through it.
		if out := os.Getenv("WADA_CAPTURE"); out != "" {
			win.OnReadyToShow(func() {
				win.Update(func() {
					if png, err := win.CapturePage(); err == nil {
						os.WriteFile(out, png, 0o644)
					}
					mygo.App.Exit(0)
				})
			})
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// send appends the draft to the transcript as the user's own words and
// brings the latest block in view.
func (a *app) send() {
	if strings.TrimSpace(a.draft) == "" {
		return
	}
	a.blocks = append(a.blocks, block{kind: "user", text: a.draft})
	a.draft = ""
	// math.MaxFloat32 scrolls the tracked container to its end.
	a.feed.Y = 1e9
}
