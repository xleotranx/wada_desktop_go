//go:build !windows

package video

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the plugin's actual command, log/progress and output handling
// without requiring a browser or network connection from the test process.
func TestRenderCommandSuccessFailureAndMissingOutput(t *testing.T) {
	for _, outcome := range []string{"success", "failure", "missing"} {
		t.Run(outcome, func(t *testing.T) {
			f := start(t)
			dir := filepath.Join(f.dir, "videos", "video-1")
			f.open(t, dir)
			bin := t.TempDir()
			script := `#!/bin/sh
out=""
while [ "$#" -gt 0 ]; do
 if [ "$1" = "--output" ]; then
  shift
  out="$1"
 fi
 shift
done
printf '%s\n' '@hf-progress {"pct":55}'
`
			switch outcome {
			case "success":
				script += "printf '%s' 'test video' > \"$out\"\n"
			case "failure":
				script += "printf '%s\\n' 'browser failed to start' >&2\nexit 3\n"
			case "missing":
				script += "exit 0\n"
			}
			if err := os.WriteFile(filepath.Join(bin, "npx"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin)
			f.p.render()
			f.h.Flush()
			if f.p.rendering.Running() || f.p.renderPct != 0 {
				t.Fatal("render did not settle")
			}
			if outcome == "success" {
				if f.p.renderErr != "" || f.p.renderOut == "" || len(f.host.Opened) != 1 {
					t.Fatalf("render not published: %q / %q", f.p.renderOut, f.p.renderErr)
				}
				if _, err := os.Stat(f.p.renderOut); err != nil {
					t.Fatal(err)
				}
			} else {
				if f.p.renderErr == "" || f.p.renderOut != "" || len(f.host.Opened) != 0 {
					t.Fatal("failed render published an output")
				}
				if outcome == "missing" && !strings.Contains(f.p.renderErr, "wrote no file") {
					t.Fatalf("missing output error: %q", f.p.renderErr)
				}
				if outcome == "failure" && !strings.Contains(strings.Join(f.p.renderLog, "\n"), "browser failed") {
					t.Fatal("render discarded the CLI failure log")
				}
			}
		})
	}
}
