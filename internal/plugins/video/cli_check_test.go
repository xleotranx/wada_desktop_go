package video

import (
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestParseCLIReport reads the official check's JSON into the editor's
// findings, relative to the project and with the clip a selector names.
func TestParseCLIReport(t *testing.T) {
	root, err := parseComposition("/proj", "index.html", `<div data-composition-id="main" data-width="1920" data-height="1080" data-duration="2">
  <video id="v" class="clip" src="assets/a.mp4" data-start="0" data-duration="2" muted></video>
</div>
<script>window.__timelines = { main: gsap.timeline({ paused: true }) };</script>`)
	if err != nil {
		t.Fatal(err)
	}
	project := &Project{Dir: "/proj", File: "index.html", Root: root}
	data := []byte(`{"ok":false,
	  "lint":{"ok":false,"errorCount":1,"warningCount":1,"findings":[
	    {"code":"video_missing_muted","severity":"error","message":"A","selector":"#v","sourceFile":"/proj/index.html","fixHint":"Add muted."},
	    {"code":"timed_element_missing_clip_class","severity":"warning","message":"B","sourceFile":"/proj/compositions/s.html","line":4}
	  ]},
	  "runtime":{"ok":false,"errorCount":1,"findings":[
	    {"code":"runtime_error","severity":"error","message":"C","sourceFile":"/proj/index.html"}
	  ]},
	  "layout":{"ok":true,"findings":[]}}`)
	c, err := parseCLIReport(data, "/proj", project)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Findings) != 3 || c.Errors != 2 || c.Warnings != 1 {
		t.Fatalf("findings = %+v", c.Findings)
	}
	first := c.Findings[0]
	if first.File != "index.html" || first.Clip != "v" || first.Source != "cli" || first.Fix != "Add muted." {
		t.Fatalf("first = %+v", first)
	}
	if second := c.Findings[1]; second.File != "compositions/s.html" || second.Line != 4 {
		t.Fatalf("second = %+v", second)
	}
}

// TestParseCLIReportIgnoresGarbage keeps a CLI that printed something else
// from looking like a clean check.
func TestParseCLIReportIgnoresGarbage(t *testing.T) {
	for _, data := range []string{"", "not json", "[1,2]"} {
		if _, err := parseCLIReport([]byte(data), "/proj", nil); err == nil {
			t.Fatalf("parseCLIReport(%q) did not fail", data)
		}
	}
}

// TestTheCheckPanelOffersTheFullCheck draws the panel's way to the CLI.
func TestTheCheckPanelOffersTheFullCheck(t *testing.T) {
	f := start(t)
	f.open(t, filepath.Join(f.dir, "videos", "video-1"))
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Fill().MinHeight(0).Children(func() {
			ui.Box(c).Grow(1).MinWidth(0)
			f.p.drawer(c)
		})
	}, 1400, 900)
	tt.Frame()
	f.h.Flush()
	tt.Frame()
	if err := tt.Click("Check"); err != nil {
		t.Fatalf("no Check tab: %v", err)
	}
	f.h.Flush()
	tt.Frame()
	if !tt.HasText("Full check") || !tt.HasText("The editor's check") {
		t.Fatalf("texts = %v", tt.Texts())
	}
}
