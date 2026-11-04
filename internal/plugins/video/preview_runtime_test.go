package video

import (
	"encoding/json"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// runtimeElement uses the production HTML scanner to give the JS runtime test
// real generated trees, without implementing another HTML parser in its DOM double.
type runtimeElement struct {
	Tag      string            `json:"tag"`
	Attrs    map[string]string `json:"attrs,omitempty"`
	Text     string            `json:"text,omitempty"`
	Children []runtimeElement  `json:"children,omitempty"`
}

func runtimeTree(t *testing.T, src string) runtimeElement {
	t.Helper()
	doc, err := scanHTML(src)
	if err != nil {
		t.Fatal(err)
	}
	var convert func(*element) runtimeElement
	convert = func(el *element) runtimeElement {
		node := runtimeElement{Tag: el.Tag, Attrs: map[string]string{}}
		for _, a := range el.Attrs {
			node.Attrs[a.Name] = a.Value
		}
		if len(el.Children) == 0 && el.Inner.Start <= el.Inner.End && el.Inner.End <= len(src) {
			node.Text = src[el.Inner.Start:el.Inner.End]
			if el.Tag != "script" && el.Tag != "style" {
				node.Text = html.UnescapeString(node.Text)
			}
		}
		for _, child := range el.Children {
			node.Children = append(node.Children, convert(child))
		}
		return node
	}
	return convert(doc)
}

func runPreviewRuntime(t *testing.T, dir string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("preview runtime tests need Node.js, also required by the renderer")
	}
	project, err := openProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	p := payload(project, nil)
	fixture := struct {
		Document runtimeElement            `json:"document"`
		Scenes   map[string]runtimeElement `json:"scenes"`
		Payload  previewPayload            `json:"payload"`
	}{runtimeTree(t, string(root)), map[string]runtimeElement{}, p}
	for _, s := range p.Scenes {
		fixture.Scenes[s.HTML] = runtimeTree(t, s.HTML)
	}
	data, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	fixturePath := filepath.Join(tmp, "fixture.json")
	runtimePath := filepath.Join(tmp, "preview.js")
	if err := os.WriteFile(fixturePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtimePath, []byte(previewJS), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, "testdata/preview-runtime.cjs", fixturePath, runtimePath).CombinedOutput()
	if err != nil {
		t.Fatalf("preview runtime: %v\n%s", err, out)
	}
	t.Log(string(out))
}

func TestPreviewRuntimeRemountAndEndFrames(t *testing.T) {
	_, dir, _ := structuredProject(t)
	runPreviewRuntime(t, dir)
}

func TestPreviewRuntimeFourTipSequence(t *testing.T) {
	_, dir, d := structuredProject(t)
	intro, demo, formula, outro := d.Scenes[0], d.Scenes[1], d.Scenes[2], d.Scenes[3]
	intro.Duration = 5
	demo.ID = "autosum"
	demo.Duration = 10
	formula.ID = "autosum-formula"
	formula.Duration = 6
	outro.Duration = 5
	d.Scenes = nil
	d.Scenes = append(d.Scenes, intro, demo, formula)
	for _, id := range []string{"flashfill", "filters", "duplicates"} {
		s := demo
		s.ID = id
		d.Scenes = append(d.Scenes, s)
	}
	d.Scenes = append(d.Scenes, outro)
	resequence(d)
	if err := saveDocument(dir, d); err != nil {
		t.Fatal(err)
	}
	runPreviewRuntime(t, dir)
}

func TestPreviewSeekKeepsTransportAtFocusedEnd(t *testing.T) {
	f, dir, _ := structuredProject(t)
	f.open(t, dir)
	f.p.focusOn(f.p.project.Scenes[1])
	f.p.seek(10)
	f.h.Flush()
	if f.ran("__video.seek(10, 10)") == 0 {
		t.Fatal("scene end was not passed to the preview")
	}
	if f.p.playhead != 10 || f.p.displayTime() != 7 {
		t.Fatalf("transport moved off its endpoint: %g", f.p.playhead)
	}
	tt := drawerTester(f, 1440, 820)
	settle(t, f, tt)
	f.p.unfocus()
	f.p.seek(20)
	f.h.Flush()
	tt.Frame()
	if f.ran("__video.seek(20, 20)") == 0 || f.p.playhead != 20 {
		t.Fatal("film end was not preserved")
	}
}
