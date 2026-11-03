package video

import (
	"slices"
	"strings"
	"testing"
)

// TestScanHTMLKeepsOffsets checks that every attribute value can be replaced
// in place: what an edit does to a composition the agent wrote.
func TestScanHTMLKeepsOffsets(t *testing.T) {
	src := `<div id="root" data-start='2' class=clip hidden>
  <img src="a.png" />
  <video src="b.mp4"></video>
</div>`
	root, err := scanHTML(src)
	if err != nil {
		t.Fatal(err)
	}
	div := root.Children[0]
	if div.Tag != "div" || div.Opening.Start != 0 || div.Opening.End != strings.Index(src, ">")+1 {
		t.Fatalf("div = %+v", div.Opening)
	}
	if got := div.value("data-start"); got != "2" {
		t.Fatalf("data-start = %q", got)
	}
	if a, _ := div.attr("class"); a.Quoted || a.ValueAt != (span{strings.Index(src, "clip"), strings.Index(src, "clip") + 4}) {
		t.Fatalf("class = %+v", a)
	}
	if _, ok := div.attr("hidden"); !ok {
		t.Fatal("hidden is not an attribute")
	}
	img := div.Children[0]
	if img.Tag != "img" || len(img.Children) != 0 || img.Closing != (span{}) {
		t.Fatalf("img = %+v", img)
	}
	video := div.Children[1]
	if video.Tag != "video" || video.Inner.Start != video.Inner.End || src[video.Inner.Start-1] != '>' {
		t.Fatalf("video inner = %+v", video.Inner)
	}
	if src[video.Closing.Start:video.Closing.End] != "</video>" {
		t.Fatalf("video close = %q", src[video.Closing.Start:video.Closing.End])
	}
}

// TestScanHTMLSkipsRawText keeps the ids of markup written inside scripts and
// styles from becoming elements.
func TestScanHTMLSkipsRawText(t *testing.T) {
	src := `<style>#root { color: red }</style><script>const x = "<div id=no>";</script><p id="yes">hi</p>`
	root, err := scanHTML(src)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(root.Children); got != 3 {
		t.Fatalf("children = %d", got)
	}
	if root.byID("no") != nil {
		t.Fatal("script text became an element")
	}
	if root.byID("yes") == nil {
		t.Fatal("the paragraph is missing")
	}
	if got := root.Children[1].Inner; src[got.Start:got.End] != `const x = "<div id=no>";` {
		t.Fatalf("script inner = %q", src[got.Start:got.End])
	}
}

// TestScanHTMLComments keeps comments out of the tree.
func TestScanHTMLComments(t *testing.T) {
	root, err := scanHTML(`<!-- <div id="a"></div> --><section id="b"></section>`)
	if err != nil {
		t.Fatal(err)
	}
	if len(root.Children) != 1 || root.Children[0].value("id") != "b" {
		t.Fatalf("children = %+v", root.Children)
	}
}

// TestScanHTMLTemplate finds the composition inside the template a scene
// wraps itself in.
func TestScanHTMLTemplate(t *testing.T) {
	src := `<template id="scene"><section id="s" data-composition-id="scene" data-duration="5"><h1>Hi</h1></section></template>`
	root, err := scanHTML(src)
	if err != nil {
		t.Fatal(err)
	}
	section := root.byID("s")
	if section == nil {
		t.Fatal("the template's section is missing")
	}
	if got := section.text(src); got != "Hi" {
		t.Fatalf("text = %q", got)
	}
}

// TestScanHTMLLowercasesTagsButKeepsValues reads what the browser reads.
func TestScanHTMLLowercasesTagsButKeepsValues(t *testing.T) {
	root, _ := scanHTML(`<DIV DATA-Start="1">x</DIV>`)
	div := root.Children[0]
	if div.Tag != "div" {
		t.Fatalf("tag = %q", div.Tag)
	}
	if got := div.value("data-start"); got != "1" {
		t.Fatalf("value = %q", got)
	}
}

// TestScanHTMLUnclosed does not hang or lose elements on markup that never
// closes, as a composition being written does.
func TestScanHTMLUnclosed(t *testing.T) {
	src := `<div id="root"><div class="clip" data-start="0" data-duration="2">`
	root, err := scanHTML(src)
	if err != nil {
		t.Fatal(err)
	}
	inner := root.Children[0].Children[0]
	if inner.value("data-duration") != "2" || inner.Inner.End != len(src) {
		t.Fatalf("inner = %+v", inner)
	}
}

// TestScanHTMLClasses reads the class list the way CSS does.
func TestScanHTMLClasses(t *testing.T) {
	root, _ := scanHTML(`<div class="  a   b  clip "></div>`)
	if got := root.Children[0].classes(); !slices.Equal(got, []string{"a", "b", "clip"}) {
		t.Fatalf("classes = %q", got)
	}
}
