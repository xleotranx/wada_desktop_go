package markup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"math"
	"path/filepath"
	"strconv"
	"strings"

	"wada-desktop-go/internal/contract/agent"
	attachment "wada-desktop-go/internal/contract/attachments"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/native"
)

// paintMarks draws marks over a w×h pixel image shown at r, with the
// palette's inks and redaction, which are the same in every theme. The
// editor and the PNG export both paint through it, so the image sent is
// the one seen. Hidden areas go first, then highlights, lines and, last,
// badges, so a later mark's line never hides a number.
func paintMarks(p *ui.Painter, pal native.Palette, marks []mark, r ui.Rect, w, h int) {
	k := r.W / float32(w)
	stroke := strokeWidth(w, h) * k
	halo := max(1, stroke*.35)
	at := func(f point) (float32, float32) { return r.X + f.X*r.W, r.Y + f.Y*r.H }
	// area is a mark's rectangle on whole image pixels, so a hidden area's
	// edge in the export is opaque, not blended with what it hides.
	area := func(m mark) ui.Rect {
		x0 := float32(math.Floor(float64(min(m.A.X, m.B.X) * float32(w))))
		y0 := float32(math.Floor(float64(min(m.A.Y, m.B.Y) * float32(h))))
		x1 := float32(math.Ceil(float64(max(m.A.X, m.B.X) * float32(w))))
		y1 := float32(math.Ceil(float64(max(m.A.Y, m.B.Y) * float32(h))))
		return ui.Rect{X: r.X + x0*k, Y: r.Y + y0*k, W: (x1 - x0) * k, H: (y1 - y0) * k}
	}
	for _, m := range marks {
		if m.Kind == redact {
			p.Fill(area(m), pal.Redaction, 0)
		}
	}
	for _, m := range marks {
		if m.Kind == highlight {
			p.Fill(area(m), inkOf(pal.Inks, m).Wash, 0)
		}
	}
	var path ui.Path
	for _, m := range marks {
		ink := inkOf(pal.Inks, m)
		switch m.Kind {
		case box:
			x0, y0 := at(m.A)
			x1, y1 := at(m.B)
			path.Reset().MoveTo(x0, y0).LineTo(x1, y0).LineTo(x1, y1).LineTo(x0, y1).Close()
			p.StrokePath(&path, stroke+2*halo, ink.Halo)
			p.StrokePath(&path, stroke, ink.Mark)
		case pen:
			if len(m.Points) < 2 {
				continue
			}
			path.Reset().MoveTo(at(m.Points[0]))
			for _, q := range m.Points[1:] {
				path.LineTo(at(q))
			}
			p.StrokePath(&path, stroke+2*halo, ink.Halo)
			p.StrokePath(&path, stroke, ink.Mark)
		case arrow:
			ax, ay := at(m.A)
			bx, by := at(m.B)
			length := float32(math.Hypot(float64(bx-ax), float64(by-ay)))
			if length < 1 {
				continue
			}
			ux, uy := (bx-ax)/length, (by-ay)/length
			head := min(length*.6, max(stroke*4, 12*k))
			half := head * .6
			baseX, baseY := bx-ux*head, by-uy*head
			var tip ui.Path
			tip.MoveTo(bx, by).LineTo(baseX-uy*half, baseY+ux*half).LineTo(baseX+uy*half, baseY-ux*half).Close()
			// The shaft stops inside the head so its round cap stays hidden.
			path.Reset().MoveTo(ax, ay).LineTo(baseX+ux*head*.3, baseY+uy*head*.3)
			p.StrokePath(&path, stroke+2*halo, ink.Halo)
			p.StrokePath(&tip, 2*halo, ink.Halo)
			p.StrokePath(&path, stroke, ink.Mark)
			p.FillPath(&tip, ink.Mark)
		case pin, highlight, redact:
		}
	}
	for i, n := range numbers(marks) {
		if n == 0 {
			continue
		}
		m := marks[i]
		ink, radius := inkOf(pal.Inks, m), m.radius(w, h)*k
		bx, by := m.badgeAt(w, h)
		cx, cy := r.X+bx*k, r.Y+by*k
		outer := radius + halo
		p.Fill(ui.Rect{X: cx - outer, Y: cy - outer, W: 2 * outer, H: 2 * outer}, ink.Halo, outer)
		p.Fill(ui.Rect{X: cx - radius, Y: cy - radius, W: 2 * radius, H: 2 * radius}, ink.Mark, radius)
		label := ui.Span{Text: strconv.Itoa(n), Size: radius * 1.1, Weight: 700, Color: ink.On}
		tw, th := p.MeasureText(0, label)
		p.RichText(cx-tw/2, cy-th/2, 0, label)
	}
}

type pngBuffer struct{ bytes.Buffer }

func (b *pngBuffer) Write(data []byte) (int, error) {
	if b.Len()+len(data) > attachment.MaxImageBytes {
		return 0, errors.New("the marked-up PNG exceeds the 10 MB attachment limit")
	}
	return b.Buffer.Write(data)
}

type exported struct {
	File    agent.File
	Preview *ui.Bitmap
	Details []attachment.Detail
}

// exportPNG lays the marks, drawn with MyGo's software renderer on a clear
// layer, over the source pixels, which stay exactly as they were outside
// the marks, and cuts each mark's detail out of the result, so a detail
// shows its mark and hides what the image hides. It runs on a worker.
func exportPNG(ctx context.Context, source image.Image, marks []mark, pal native.Palette, name string) (exported, error) {
	if err := ctx.Err(); err != nil {
		return exported{}, err
	}
	bounds := source.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if !validDimensions(w, h) || len(marks) > maxMarks {
		return exported{}, errors.New("the screenshot or number of marks exceeds the editor's limits")
	}
	layer := ui.Render(func(c *ui.Context) {
		c.Root().Background(ui.Transparent)
		ui.Box(c).Size(float32(w), float32(h)).Draw(func(p *ui.Painter, r ui.Rect) {
			paintMarks(p, pal, marks, r, w, h)
		})
	}, w, h, 1)
	if err := ctx.Err(); err != nil {
		return exported{}, err
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), source, bounds.Min, draw.Src)
	draw.Draw(img, img.Bounds(), layer, image.Point{}, draw.Over)
	var data pngBuffer
	if err := png.Encode(&data, img); err != nil {
		return exported{}, err
	}
	out := exported{File: agent.File{Name: name, MIME: "image/png", Data: data.Bytes()}, Preview: ui.NewBitmap(img)}
	base := strings.TrimSuffix(name, filepath.Ext(name))
	for i, n := range numbers(marks) {
		if err := ctx.Err(); err != nil {
			return exported{}, err
		}
		m := marks[i]
		if !m.Detail || !m.detailable() {
			continue
		}
		r := m.detailRect(w, h)
		if r.Empty() {
			continue
		}
		var part pngBuffer
		if err := png.Encode(&part, img.SubImage(r)); err != nil {
			return exported{}, err
		}
		out.Details = append(out.Details, attachment.Detail{
			Name: fmt.Sprintf("mark %d at full resolution", n),
			File: agent.File{Name: fmt.Sprintf("%s mark %d.png", base, n), MIME: "image/png", Data: part.Bytes()},
		})
	}
	return out, nil
}

// downscale averages img down to w×h pixels, as a backend sending it
// smaller shows it, for the editor's view of what the model sees.
func downscale(img image.Image, w, h int) *image.RGBA {
	src := image.NewRGBA(image.Rect(0, 0, img.Bounds().Dx(), img.Bounds().Dy()))
	draw.Draw(src, src.Bounds(), img, img.Bounds().Min, draw.Src)
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		y0, y1 := y*sh/h, max(y*sh/h+1, (y+1)*sh/h)
		for x := range w {
			x0, x1 := x*sw/w, max(x*sw/w+1, (x+1)*sw/w)
			var sum [4]int
			for sy := y0; sy < y1; sy++ {
				row := src.Pix[sy*src.Stride+x0*4 : sy*src.Stride+x1*4]
				for i := 0; i < len(row); i += 4 {
					sum[0] += int(row[i])
					sum[1] += int(row[i+1])
					sum[2] += int(row[i+2])
					sum[3] += int(row[i+3])
				}
			}
			n := (x1 - x0) * (y1 - y0)
			o := y*dst.Stride + x*4
			for c := range 4 {
				dst.Pix[o+c] = uint8((sum[c] + n/2) / n)
			}
		}
	}
	return dst
}
