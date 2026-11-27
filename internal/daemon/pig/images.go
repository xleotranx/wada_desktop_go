package pig

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"wada-desktop-go/internal/protocol"
)

// FitImage returns the pixel size file reaches model at after PiG's prompt resizer.
func (r *Runtime) FitImage(ctx context.Context, ref protocol.ModelRef, file protocol.File) (int, int, error) {
	e, release, err := r.acquire()
	defer release()
	if err != nil {
		return 0, 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	if !e.services.Settings().GetImageAutoResize() {
		config, _, err := image.DecodeConfig(bytes.NewReader(file.Data))
		return config.Width, config.Height, err
	}
	args := subprocess.ResizeImageArgs{Data: base64.StdEncoding.EncodeToString(file.Data), MimeType: file.MIME}
	if m := e.resolveModel(ref); m != nil && m.InputLimits != nil && m.InputLimits.Images != nil && m.InputLimits.Images.Resize != nil {
		lim := m.InputLimits.Images.Resize
		args.Options = &struct {
			MaxWidth    int `json:"maxWidth,omitempty"`
			MaxHeight   int `json:"maxHeight,omitempty"`
			MaxBytes    int `json:"maxBytes,omitempty"`
			JPEGQuality int `json:"jpegQuality,omitempty"`
		}{lim.MaxWidth, lim.MaxHeight, lim.MaxBytes, lim.JPEGQuality}
	}
	result, err := subprocess.ResizeImage(args)
	if err != nil {
		return 0, 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	if result == nil || result.Image == nil || result.Image.Width == nil || result.Image.Height == nil {
		return 0, 0, errors.New("the image cannot be resized within the model's limits")
	}
	return *result.Image.Width, *result.Image.Height, nil
}
