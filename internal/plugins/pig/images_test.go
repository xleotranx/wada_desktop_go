package pig

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"math/rand/v2"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"wada-desktop-go/internal/contract/agent"
)

func imageFile(t *testing.T, w, h int, noise bool) agent.File {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < len(img.Pix); i += 4 {
		if noise {
			img.Pix[i], img.Pix[i+1], img.Pix[i+2] = uint8(r.Uint32()), uint8(r.Uint32()), uint8(r.Uint32())
		}
		img.Pix[i+3] = 255
	}
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	return agent.File{Name: "image.png", MIME: "image/png", Data: data.Bytes()}
}

func TestImageFitAccountsForTheEncodedByteLimit(t *testing.T) {
	h, p, _ := startPig(t)
	e, err := p.current()
	if err != nil {
		t.Fatal(err)
	}
	faux := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "small-images", Models: []ai.FauxModelDefinition{{
		ID: "vision", Input: []string{"text", "image"},
		InputLimits: &ai.ModelInputLimits{Images: &ai.ModelImageInputLimits{Resize: &ai.ModelImageResizeOptions{
			MaxWidth: 2000, MaxHeight: 2000, MaxBytes: 10000,
		}}},
	}}})
	if err := e.models.RegisterNativeProvider(faux.Provider()); err != nil {
		t.Fatal(err)
	}
	ref := agent.ModelRef{Provider: "small-images", ID: "vision"}
	file := imageFile(t, 600, 600, true)
	fit := func() [2]int {
		return call(t, h, func(ctx context.Context) ([2]int, error) {
			w, h, err := p.Fit(ctx, ref, file)
			return [2]int{w, h}, err
		})
	}
	// The pinned PiG encoder needs four 3/4-size steps to fit this image.
	if got := fit(); got != [2]int{189, 189} {
		t.Fatalf("byte-limited image sent at %v, want 189×189", got)
	}
	if err := e.services.SettingsManager().SetImageAutoResize(false); err != nil {
		t.Fatal(err)
	}
	if got := fit(); got != [2]int{600, 600} {
		t.Fatalf("resizing disabled: got %v, want 600×600", got)
	}
}

func TestImagesFitWithinTheModelsLimitsKeepingTheirShape(t *testing.T) {
	h, p, _ := startPig(t)
	e, err := p.current()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ w, h, mw, mh, ww, wh int }{
		{3024, 1964, 0, 0, 2000, 1299}, // default limits
		{1964, 3024, 2000, 2000, 1299, 2000},
		{1920, 1080, 2000, 2000, 1920, 1080},
		{5120, 2880, 1568, 1000, 1568, 882},
		{4000, 4000, 3000, 1000, 1000, 1000},
	} {
		faux := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "image-limits", Models: []ai.FauxModelDefinition{{
			ID: "vision", Input: []string{"text", "image"},
			InputLimits: &ai.ModelInputLimits{Images: &ai.ModelImageInputLimits{Resize: &ai.ModelImageResizeOptions{MaxWidth: tc.mw, MaxHeight: tc.mh}}},
		}}})
		if err := e.models.RegisterNativeProvider(faux.Provider()); err != nil {
			t.Fatal(err)
		}
		file := imageFile(t, tc.w, tc.h, false)
		got := call(t, h, func(ctx context.Context) ([2]int, error) {
			w, h, err := p.Fit(ctx, agent.ModelRef{Provider: "image-limits", ID: "vision"}, file)
			return [2]int{w, h}, err
		})
		if got != [2]int{tc.ww, tc.wh} {
			t.Errorf("%d×%d in %d×%d: %v, want %d×%d", tc.w, tc.h, tc.mw, tc.mh, got, tc.ww, tc.wh)
		}
	}
}

func TestImageFitRejectsInvalidAndCancelledImages(t *testing.T) {
	h, p, _ := startPig(t)
	file := imageFile(t, 10, 10, false)
	call(t, h, func(ctx context.Context) (struct{}, error) {
		if _, _, err := p.Fit(ctx, agent.ModelRef{}, agent.File{MIME: "image/png", Data: []byte("invalid")}); err == nil {
			t.Error("an invalid image reported a sent size")
		}
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if _, _, err := p.Fit(cancelled, agent.ModelRef{}, file); err != context.Canceled {
			t.Errorf("cancelled fit: %v", err)
		}
		return struct{}{}, nil
	})
}
