package design

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/kernel"
)

// renderRaster waits for the full-size viewport to be committed before taking
// a reduced preview. Only content or viewport changes need another capture;
// panning and stepping between reduced zoom levels reuse the bitmap.
func (p *Plugin) renderRaster(path string, refresh bool) {
	b := p.boards[path]
	a, ok := p.current().board(path)
	if b == nil || !ok || p.vp.zoom() >= 1 || path == p.presenting {
		return
	}
	size := previewSize{a.W, a.H}
	if !refresh && b.rasterSize == size && (b.raster != nil || b.rastering.Running()) {
		return
	}
	if b.rasterSize != size {
		b.raster = nil
	}
	b.rasterSize = size
	web := b.web
	script := fmt.Sprintf(`
// Full-size canvas preview.
if (document.readyState !== "complete" || Math.abs(innerWidth-%g)>2 || Math.abs(innerHeight-%g)>2) return null;
await document.fonts.ready;
await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
return true;`, a.W, a.H)
	kernel.Go(p.navigation, &b.rastering, func(ctx context.Context) (*ui.Bitmap, error) {
		ctx, cancel := context.WithTimeout(ctx, loadWait)
		defer cancel()
		ticker := time.NewTicker(loadCheck)
		defer ticker.Stop()
		for {
			ready, err := web.Eval(ctx, script)
			if err != nil {
				return nil, err
			}
			if ready == true {
				break
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-ticker.C:
			}
		}
		data, err := web.Capture()
		if err != nil {
			return nil, err
		}
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		return ui.NewBitmap(img), nil
	}, func(bitmap *ui.Bitmap, _ error) {
		b.raster = bitmap
	})
}

// actualBoard opens a reduced preview as a live page at readable size.
func (p *Plugin) actualBoard(a artboard) {
	p.frameBoard(a)
	p.zoomTo(1)
}
