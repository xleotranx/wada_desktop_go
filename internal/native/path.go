package native

import (
	"github.com/egoist/mygo/ui"
	"net/url"
)

func OpenLocalPath(c *ui.Context, path string) {
	c.OpenURL((&url.URL{Scheme: "file", Path: path}).String())
}
