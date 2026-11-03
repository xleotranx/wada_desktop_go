package video

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// videosDir is where the app puts a video's folder, inside the conversation's
// folder, like designs.
const videosDir = "videos"

// ref is a project the folder holds.
type ref struct {
	Dir      string // absolute
	Title    string
	Modified time.Time
}

// discover lists the HyperFrames projects of a folder: the folder itself
// when its index.html is a composition, and each folder under videos/.
func discover(folder string) []ref {
	if folder == "" {
		return nil
	}
	var found []ref
	if isProject(folder) {
		found = append(found, projectRef(folder))
	}
	entries, err := os.ReadDir(filepath.Join(folder, videosDir))
	if err != nil {
		return found
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(folder, videosDir, e.Name())
		if isProject(dir) {
			found = append(found, projectRef(dir))
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Modified.After(found[j].Modified) })
	return found
}

// isProject reports whether a folder's index.html is a composition. The read
// is bounded: the root element is at the top of a project's file.
func isProject(dir string) bool {
	f, err := os.Open(filepath.Join(dir, "index.html"))
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 8192)
	n, _ := f.Read(head)
	return strings.Contains(string(head[:n]), "data-composition-id")
}

// projectRef describes a project folder without parsing it.
func projectRef(dir string) ref {
	r := ref{Dir: dir, Title: filepath.Base(dir)}
	if info, err := os.Stat(filepath.Join(dir, "index.html")); err == nil {
		r.Modified = info.ModTime()
	}
	if src, err := os.ReadFile(filepath.Join(dir, "index.html")); err == nil {
		if t := titleOf(string(src)); t != "" {
			r.Title = t
		}
	}
	return r
}

// titleOf reads the document's title.
func titleOf(src string) string {
	doc, err := scanHTML(src)
	if err != nil {
		return ""
	}
	var title string
	doc.walk(func(n *element) bool {
		if title != "" {
			return false
		}
		if n.Tag == "title" && n.Inner.End <= len(src) {
			title = strings.TrimSpace(src[n.Inner.Start:n.Inner.End])
		}
		return true
	})
	return title
}

// openProject reads a project's root composition and the scenes it mounts.
// A scene file that is missing or cannot be read still opens: the check says
// so, and the timeline shows its window.
func openProject(dir string) (*Project, error) {
	src, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		return nil, err
	}
	root, err := parseComposition(dir, "index.html", string(src))
	if err != nil {
		return nil, err
	}
	p := &Project{Dir: dir, File: "index.html", Root: root, FPS: root.FPS}
	if root.root == nil {
		return nil, ErrNotAComposition
	}
	if p.FPS <= 0 {
		p.FPS = 30
	}
	p.Title = titleOf(string(src))
	if p.Title == "" {
		p.Title = filepath.Base(dir)
	}
	for _, c := range root.Clips {
		if c.Mount == "" {
			continue
		}
		rel := path.Clean(c.Mount)
		scene := &Scene{Host: c, Rate: 1}
		if v := parseNumber(hostAttr(root, c, "data-playback-start"), 0); v != 0 {
			scene.PlaybackStart = v
		}
		if v := parseNumber(hostAttr(root, c, "data-playback-rate"), 1); v > 0 {
			scene.Rate = v
		}
		if strings.HasPrefix(rel, "../") || path.IsAbs(rel) {
			scene.Comp = &Composition{File: rel, Width: 1920, Height: 1080, FPS: 30}
			p.Scenes = append(p.Scenes, scene)
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			scene.Comp = &Composition{File: rel, Width: 1920, Height: 1080, FPS: 30}
			p.Scenes = append(p.Scenes, scene)
			continue
		}
		comp, err := parseComposition(dir, rel, string(data))
		if err != nil {
			comp = &Composition{File: rel, Width: 1920, Height: 1080, FPS: 30}
		}
		scene.Comp = comp
		p.Scenes = append(p.Scenes, scene)
	}
	return p, nil
}

// hostAttr reads an attribute of the element a clip was read from.
func hostAttr(c *Composition, clip *Clip, name string) string {
	if c.root == nil {
		return ""
	}
	el := c.root.byID(clip.ID)
	if el == nil {
		return ""
	}
	return el.value(name)
}

// ErrNotAComposition is what a project without a composition root opens as.
var ErrNotAComposition = errors.New("index.html has no data-composition-id")

// newProject makes a blank project in dir: a root that mounts one scene, and
// that scene's own file. It returns the project, so the editor can show it
// without reading it again.
func newProject(dir, title string) (*Project, error) {
	if err := os.MkdirAll(filepath.Join(dir, "compositions"), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		return nil, err
	}
	if title == "" {
		title = filepath.Base(dir)
	}
	if err := writeFile(filepath.Join(dir, "compositions", "scene-01.html"), sceneTemplate("scene-01", title, 8)); err != nil {
		return nil, err
	}
	if err := writeFile(filepath.Join(dir, "index.html"), rootTemplate(title)); err != nil {
		return nil, err
	}
	return openProject(dir)
}

// writeFile writes a file whole.
func writeFile(path, data string) error {
	return os.WriteFile(path, []byte(data), 0o644)
}

// rootTemplate is a project's index.html: one scene, and the paused root
// timeline the runtime seeks.
func rootTemplate(title string) string {
	return `<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <title>` + title + `</title>
    <script src="https://cdn.jsdelivr.net/npm/gsap@3.15.0/dist/gsap.min.js"></script>
    <style>
      html,
      body {
        width: 1920px;
        height: 1080px;
        margin: 0;
        overflow: hidden;
        background: #0b0b0c;
      }
      #main {
        position: relative;
        width: 1920px;
        height: 1080px;
      }
    </style>
  </head>
  <body>
    <div
      id="main"
      data-composition-id="main"
      data-width="1920"
      data-height="1080"
      data-fps="30"
      data-start="0"
      data-duration="8"
    >
      <div
        id="scene-01"
        data-composition-id="scene-01"
        data-composition-src="compositions/scene-01.html"
        data-start="0"
        data-duration="8"
        data-track-index="0"
      ></div>
    </div>
    <script>
      window.__timelines = window.__timelines || {};
      const tl = gsap.timeline({ paused: true });
      window.__timelines["main"] = tl;
      tl.seek(0);
    </script>
  </body>
</html>
`
}

// sceneTemplate is a scene file: its markup in a template, and its own
// paused timeline, local to the scene.
func sceneTemplate(id, title string, length float64) string {
	return `<template id="` + id + `-template">
  <section
    id="` + id + `"
    data-composition-id="` + id + `"
    data-width="1920"
    data-height="1080"
    data-duration="` + formatSeconds(length) + `"
  >
    <style>
      #` + id + ` {
        position: absolute;
        inset: 0;
        display: flex;
        align-items: center;
        justify-content: center;
        background: #0b0b0c;
        color: #fff;
        font-family: Inter, Arial, sans-serif;
      }
      #` + id + ` .title {
        font-size: 120px;
        font-weight: 600;
        letter-spacing: -0.03em;
      }
    </style>
    <h1 id="` + id + `-title" class="clip title" data-start="0" data-duration="` + formatSeconds(length) + `" data-track-index="0">` + title + `</h1>
    <script>
      // The title starts visible, so a new project's first frame is not blank.
      const tl = gsap.timeline({ paused: true });
      tl.fromTo("#` + id + `-title", { y: 40 }, { y: 0, duration: 0.6, ease: "power3.out" }, 0);
      window.__timelines["` + id + `"] = tl;
    </script>
  </section>
</template>
`
}

// projectFile is a project-relative path as a path on disk.
func projectFile(dir, rel string) string { return filepath.Join(dir, filepath.FromSlash(rel)) }

// fileTime is a file's modification time, or the zero time when it is not
// there.
func fileTime(path string) time.Time {
	if info, err := os.Stat(path); err == nil {
		return info.ModTime()
	}
	return time.Time{}
}

// projectTime is when the project last changed: its root, or a scene it
// mounts.
func projectTime(p *Project) time.Time {
	if p == nil {
		return time.Time{}
	}
	latest := fileTime(projectFile(p.Dir, p.File))
	for _, s := range p.Scenes {
		if s.Comp == nil {
			continue
		}
		if at := fileTime(projectFile(p.Dir, s.Comp.File)); at.After(latest) {
			latest = at
		}
	}
	return latest
}
