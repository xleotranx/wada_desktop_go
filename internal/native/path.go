package native

import (
	"os"
	"strings"
	"sync"
)

var home = sync.OnceValue(func() string { h, _ := os.UserHomeDir(); return h })

// Tilde shortens a path under the user's home folder to start with "~/".
func Tilde(path string) string {
	if h := home(); h != "" {
		if rest, ok := strings.CutPrefix(path, h+"/"); ok {
			return "~/" + rest
		}
	}
	return path
}
