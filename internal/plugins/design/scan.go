package design

import (
	"cmp"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Limits of the folder walk, so a large project costs little.
const (
	maxDepth  = 5
	maxBoards = 200
	maxSeen   = 20000
	headBytes = 4096 // read of each page for its size
)

// The size of an artboard without one of its own, and the bounds of one.
const (
	defaultW, defaultH = 1280, 800
	minSide, maxSide   = 120, 4000
	boardGap           = 120 // between artboards in a row, in page pixels
)

// skipped are folders that hold no designs, or too much to walk.
var skipped = map[string]bool{"node_modules": true, "vendor": true, "target": true, "__pycache__": true}

// systemNames are the files a design system is written in, by lowercase
// name; files ending in ".tokens.json" are too.
var systemNames = map[string]bool{
	"design.md": true, "design-system.md": true, "design_system.md": true, "designsystem.md": true,
	"brand.md": true, "brand-guidelines.md": true, "style-guide.md": true, "styleguide.md": true,
	"tokens.json": true, "design-tokens.json": true,
}

var (
	metaTag  = regexp.MustCompile(`(?is)<meta\b[^>]*\bname\s*=\s*["']artboard["'][^>]*>`)
	metaSize = regexp.MustCompile(`(?i)\bcontent\s*=\s*["']\s*(\d+)\s*[x×]\s*(\d+)\s*["']`)
)

// artboard is an HTML file on the canvas. X, Y, W and H place it on its
// page in page pixels, as it shows at 100%.
type artboard struct {
	Path       string
	Modified   time.Time
	X, Y, W, H float32
}

func (a artboard) name() string {
	return strings.TrimSuffix(filepath.Base(a.Path), filepath.Ext(a.Path))
}

// page is the artboards of one folder, side by side in the order of their
// names.
type page struct {
	Dir    string // relative to the design's folder, "." for itself
	Boards []artboard
	Newest time.Time
}

func (p page) board(path string) (artboard, bool) {
	i := slices.IndexFunc(p.Boards, func(a artboard) bool { return a.Path == path })
	if i < 0 {
		return artboard{}, false
	}
	return p.Boards[i], true
}

// layout positions artboards after their widths are known.
func (p *page) layout() {
	x := float32(0)
	for i := range p.Boards {
		p.Boards[i].X = x
		x += p.Boards[i].W + boardGap
	}
}

// bounds is the box around the page's artboards.
func (p page) bounds() (x0, y0, x1, y1 float32) {
	for i, a := range p.Boards {
		if i == 0 {
			x0, y0, x1, y1 = a.X, a.Y, a.X+a.W, a.Y+a.H
			continue
		}
		x0, y0, x1, y1 = min(x0, a.X), min(y0, a.Y), max(x1, a.X+a.W), max(y1, a.Y+a.H)
	}
	return
}

// listing is what a folder holds for the canvas.
type listing struct {
	Pages   []page   // the page with the newest artboard first
	Systems []string // design system files, relative, by name
}

func (l listing) page(dir string) (page, bool) {
	i := slices.IndexFunc(l.Pages, func(p page) bool { return p.Dir == dir })
	if i < 0 {
		return page{}, false
	}
	return l.Pages[i], true
}

// list walks the design's folder for its HTML files, and the
// conversation's folder for design systems. It leaves out hidden folders and
// dependencies and stops at limits of depth and count. A design's folder
// that is not there yet holds nothing.
func list(ctx context.Context, folder, project string) (listing, error) {
	byDir := map[string]*page{}
	var out listing
	systems, err := listSystems(ctx, project)
	if err != nil {
		return listing{}, err
	}
	seen, boards := 0, 0
	err = filepath.WalkDir(folder, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == folder && errors.Is(err, fs.ErrNotExist) {
				return fs.SkipAll
			}
			if path == folder {
				return err
			}
			return nil // an unreadable folder holds nothing to show
		}
		if seen++; seen > maxSeen || boards >= maxBoards {
			return fs.SkipAll
		}
		if seen%256 == 0 && ctx.Err() != nil {
			return ctx.Err()
		}
		r := rel(folder, path)
		if d.IsDir() {
			if path != folder && (strings.HasPrefix(d.Name(), ".") || skipped[d.Name()] || strings.Count(r, "/") >= maxDepth) {
				return fs.SkipDir
			}
			return nil
		}
		if ext := strings.ToLower(filepath.Ext(d.Name())); ext != ".html" && ext != ".htm" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		w, h := size(path)
		dir := filepath.ToSlash(filepath.Dir(r))
		pg := byDir[dir]
		if pg == nil {
			pg = &page{Dir: dir}
			byDir[dir] = pg
		}
		pg.Boards = append(pg.Boards, artboard{Path: path, Modified: info.ModTime(), W: w, H: h})
		if info.ModTime().After(pg.Newest) {
			pg.Newest = info.ModTime()
		}
		boards++
		return nil
	})
	if err != nil {
		return listing{}, err
	}
	for _, pg := range byDir {
		slices.SortFunc(pg.Boards, func(a, b artboard) int { return naturalCompare(a.name(), b.name()) })
		pg.layout()
		out.Pages = append(out.Pages, *pg)
	}
	slices.SortFunc(out.Pages, func(a, b page) int {
		return cmp.Or(b.Newest.Compare(a.Newest), strings.Compare(a.Dir, b.Dir))
	})
	out.Systems = systems
	return out, nil
}

// listSystems finds the design systems in the conversation's folder, at most
// three folders deep, by name.
func listSystems(ctx context.Context, project string) ([]string, error) {
	var out []string
	seen := 0
	err := filepath.WalkDir(project, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == project {
				return err
			}
			return nil
		}
		if seen++; seen > maxSeen {
			return fs.SkipAll
		}
		if seen%256 == 0 && ctx.Err() != nil {
			return ctx.Err()
		}
		r := rel(project, path)
		if d.IsDir() {
			if path != project && (strings.HasPrefix(d.Name(), ".") || skipped[d.Name()] || strings.Count(r, "/") >= 2) {
				return fs.SkipDir
			}
			return nil
		}
		if name := strings.ToLower(d.Name()); systemNames[name] || strings.HasSuffix(name, ".tokens.json") {
			out = append(out, r)
		}
		return nil
	})
	slices.Sort(out)
	return out, err
}

// size reads the artboard size a page declares in its head, as
// <meta name="artboard" content="1440x900">, or else the default.
func size(path string) (w, h float32) {
	w, h = defaultW, defaultH
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	head, _ := io.ReadAll(io.LimitReader(f, headBytes))
	tag := metaTag.Find(head)
	m := metaSize.FindSubmatch(tag)
	if m == nil {
		return
	}
	pw, _ := strconv.Atoi(string(m[1]))
	ph, _ := strconv.Atoi(string(m[2]))
	return float32(max(minSide, min(maxSide, pw))), float32(max(minSide, min(maxSide, ph)))
}

// naturalCompare orders names as people count: "Screen 2" before "Screen 10".
func naturalCompare(a, b string) int {
	for a != "" && b != "" {
		da, db := digits(a), digits(b)
		if da > 0 && db > 0 {
			na, _ := strconv.Atoi(a[:da])
			nb, _ := strconv.Atoi(b[:db])
			if c := cmp.Compare(na, nb); c != 0 {
				return c
			}
			a, b = a[da:], b[db:]
			continue
		}
		if c := cmp.Compare(strings.ToLower(a[:1]), strings.ToLower(b[:1])); c != 0 {
			return c
		}
		a, b = a[1:], b[1:]
	}
	return cmp.Compare(len(a), len(b))
}

func digits(s string) int {
	n := 0
	for n < len(s) && n < 9 && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	return n
}

func rel(folder, path string) string {
	if r, err := filepath.Rel(folder, path); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return path
}
