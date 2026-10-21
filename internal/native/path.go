package native

import (
	"net/url"

	"github.com/egoist/mygo/ui"
)

func OpenLocalPath(c *ui.Context, path string) {
	c.OpenURL((&url.URL{Scheme: "file", Path: path}).String())
}
