package pig

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"math/rand/v2"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"wada-desktop-go/internal/protocol"
)

func imageFile(t *testing.T, w, h int, noise bool) protocol.File {
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
	return protocol.File{Name: "image.png", MIME: "image/png", Data: data.Bytes()}
}

func TestImageFitAccountsForTheEncodedByteLimit(t *testing.T) {
	rt, _ := newTestRuntime(t)
	e, release, err := rt.acquire()
	defer release()
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
	ref := protocol.ModelRef{Provider: "small-images", ID: "vision"}
	file := imageFile(t, 600, 600, true)
	w, h, err := rt.FitImage(t.Context(), ref, file)
	if err != nil {
		t.Fatal(err)
	}
	if [2]int{w, h} != [2]int{189, 189} {
		t.Fatalf("byte-limited image sent at %dx%d, want 189x189", w, h)
	}
	if err := e.services.SettingsManager().SetImageAutoResize(false); err != nil {
		t.Fatal(err)
	}
	w, h, err = rt.FitImage(t.Context(), ref, file)
	if err != nil {
		t.Fatal(err)
	}
	if [2]int{w, h} != [2]int{600, 600} {
		t.Fatalf("resizing disabled: got %dx%d, want 600x600", w, h)
	}
}

func TestImagesFitWithinTheModelsLimitsKeepingTheirShape(t *testing.T) {
	rt, _ := newTestRuntime(t)
	for _, tc := range []struct{ w, h, mw, mh, ww, wh int }{
		{3024, 1964, 0, 0, 2000, 1299},
		{1964, 3024, 2000, 2000, 1299, 2000},
		{1920, 1080, 2000, 2000, 1920, 1080},
		{5120, 2880, 1568, 1000, 1568, 882},
		{4000, 4000, 3000, 1000, 1000, 1000},
	} {
		faux := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "image-limits", Models: []ai.FauxModelDefinition{{
			ID: "vision", Input: []string{"text", "image"},
			InputLimits: &ai.ModelInputLimits{Images: &ai.ModelImageInputLimits{Resize: &ai.ModelImageResizeOptions{MaxWidth: tc.mw, MaxHeight: tc.mh}}},
		}}})
		if err := rt.ModelRuntime().RegisterNativeProvider(faux.Provider()); err != nil {
			t.Fatal(err)
		}
		file := imageFile(t, tc.w, tc.h, false)
		w, h, err := rt.FitImage(t.Context(), protocol.ModelRef{Provider: "image-limits", ID: "vision"}, file)
		if err != nil {
			t.Fatal(err)
		}
		if [2]int{w, h} != [2]int{tc.ww, tc.wh} {
			t.Errorf("%dx%d in %dx%d: %dx%d, want %dx%d", tc.w, tc.h, tc.mw, tc.mh, w, h, tc.ww, tc.wh)
		}
	}
}

func TestImageFitRejectsInvalidAndCancelledImages(t *testing.T) {
	rt, _ := newTestRuntime(t)
	file := imageFile(t, 10, 10, false)
	if _, _, err := rt.FitImage(t.Context(), protocol.ModelRef{}, protocol.File{MIME: "image/png", Data: []byte("invalid")}); err == nil {
		t.Error("an invalid image reported a sent size")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := rt.FitImage(cancelled, protocol.ModelRef{}, file); err != context.Canceled {
		t.Errorf("cancelled fit: %v", err)
	}
}
