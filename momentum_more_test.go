package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"

	"github.com/xleotranx/wada-kit/kernel"
)

func TestScreenshotMarkupTravelsWithTheMessageAndLeavesTheSourceFileUntouched(t *testing.T) {
	a := startApp(t)
	a.tt.SetSize(900, 600)
	a.openConversation(agent.Session{ID: "s1", Title: "Fix the parser", Directory: "/work/app"})
	img := image.NewNRGBA(image.Rect(0, 0, 200, 100))
	for y := range 100 {
		for x := range 200 {
			img.SetNRGBA(x, y, color.NRGBA{R: 245, G: 245, B: 245, A: 255})
		}
	}
	var original bytes.Buffer
	if err := png.Encode(&original, img); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "screen.png")
	if err := os.WriteFile(path, original.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, e := range kernel.Items(a.shell.Kernel, chat.FileDrop) {
		kernel.Invoke(a.shell.Kernel, e, func(drop func([]string)) { drop([]string{path}) })
	}
	a.until("image action", func() bool { _, ok := a.tt.Find("Mark up screen.png"); return ok })
	if err := a.tt.Click("Mark up screen.png"); err != nil {
		t.Fatal(err)
	}
	a.until("canvas", func() bool { _, ok := a.tt.Find("Screenshot canvas"); return ok })
	a.tt.Advance(time.Second)
	if err := a.tt.Click("Box"); err != nil {
		t.Fatal(err)
	}
	// The 200×100 screenshot shows at its own size, centered.
	r, _ := a.tt.Find("Screenshot canvas")
	w := min(r.W, r.H*2, 200)
	h := w / 2
	x, y := r.X+(r.W-w)/2, r.Y+(r.H-h)/2
	a.tt.Press(x+w*.25, y+h*.25)
	a.tt.Move(x+w*.75, y+h*.75)
	a.tt.Release(x+w*.75, y+h*.75)
	a.settle()
	if !a.tt.Focused("Note for mark 1") {
		t.Fatal("the new mark's note is not focused")
	}
	a.tt.Type("Move this panel to the right")
	a.settle()
	captureMomentum(t, a.tt, "screenshot-markup")
	if apply, ok := a.tt.Find("Use marked-up image"); !ok || apply.Y+apply.H > 600 || apply.H < 24 {
		t.Fatalf("the editor does not fit the minimum window: %+v", apply)
	}
	if err := a.tt.Click("Use marked-up image"); err != nil {
		t.Fatal(err)
	}
	a.until("marked-up chip", func() bool {
		return slices.ContainsFunc(a.tt.Texts(), func(s string) bool { return strings.HasSuffix(s, "· with notes") })
	})
	ctl, _ := kernel.Use(a.shell.Kernel, chat.ControllerKey)
	ctl.SetDraft("Fix the layout")
	a.settle()
	sends := make(chan agent.Send, 1)
	a.agent.OnSend = func(req agent.Send) { sends <- req }
	if err := a.tt.Click("Send"); err != nil {
		t.Fatal(err)
	}
	a.settle()
	select {
	case req := <-sends:
		want := "Fix the layout\n\n[Image 1: screen.png]\nI drew the numbered marks; they are not part of the image. Positions are x, y from 0 to 1000 across and down.\n" +
			"1. Red box from (250, 250) to (750, 750): Move this panel to the right"
		if len(req.Files) != 1 || req.Files[0].MIME != "image/png" || bytes.Equal(req.Files[0].Data, original.Bytes()) || req.Text != want {
			t.Fatalf("wrong marked-up message: text=%q, files=%d", req.Text, len(req.Files))
		}
		marked, err := png.Decode(bytes.NewReader(req.Files[0].Data))
		if err != nil || marked.Bounds() != img.Bounds() {
			t.Fatalf("invalid annotated PNG: %v", err)
		}
		if marked.At(50, 25) == img.At(50, 25) {
			t.Fatal("the rectangle is missing from the sent pixels")
		}
	default:
		t.Fatal("the annotated message was not sent")
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, original.Bytes()) {
		t.Fatal("markup changed the source file")
	}
}
