// Package voice dictates into the composer: the web engine's speech
// recognition, in a page that never shows, turns what the user says into
// the draft's text as they speak.
package voice

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/native"
	"wada-desktop-go/internal/contract/chat"
)

// Plugin owns the dictation page and what is being dictated.
type Plugin struct {
	ctx  *kernel.Context
	chat chat.Controller
	host shell.Host

	page      shell.WebView // made the first time the user dictates
	loaded    bool
	listening bool
	stopping  bool
	draftID   uint64 // the conversation dictated into
	session   string
	base      string // the draft before dictation began
	heard     string // what the recognizer has so far
	run       kernel.Task
}

// New makes the plugin; the kernel makes one for each activation.
func New() kernel.Plugin { return &Plugin{} }

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID: "voice", Title: "Dictation",
		Description: "Dictate a message into the composer.",
		Requires:    []kernel.Named{chat.ControllerKey, shell.HostKey},
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.chat = kernel.Require(c, chat.ControllerKey)
	p.host = kernel.Require(c, shell.HostKey)
	c.OnDispose(func() {
		if p.page != nil {
			p.page.Close()
		}
	})
	// Dictation belongs to the conversation it began in.
	kernel.On(c, chat.Changed, func(conv chat.Conversation) {
		if p.listening && (string(conv.Session) != p.session || conv.DraftID != p.draftID) {
			p.stop()
		}
	})
	kernel.Contribute(c, chat.ComposerAccessory, "voice.dictate", 20, shell.View{Render: p.button})
	return kernel.Contribute(c, kernel.Commands, "voice.dictate", 0, kernel.Command{
		Title: "Dictate", Menu: "Edit", Keys: []string{"shift+cmd+m"}, Run: p.toggle})
}

func (p *Plugin) toggle() {
	if p.listening {
		p.stop()
		return
	}
	p.start()
}

// start begins dictating into the open conversation's draft.
func (p *Plugin) start() {
	if p.page == nil {
		v, err := p.host.NewWebView()
		if err != nil {
			p.fail(err)
			return
		}
		p.page = v
	}
	conv := p.chat.Current()
	p.listening, p.stopping = true, false
	p.draftID, p.session = conv.DraftID, string(conv.Session)
	p.base, p.heard = p.chat.DraftText(), ""
	page, loaded, dir := p.page, p.loaded, p.host.DataDir()
	kernel.Go(p.ctx, &p.run, func(ctx context.Context) (struct{}, error) {
		if !loaded {
			path, err := writePage(dir)
			if err != nil {
				return struct{}{}, err
			}
			if err := page.LoadFile(path); err != nil {
				return struct{}{}, err
			}
		}
		for range 50 {
			v, err := page.Eval(ctx, "if (!window.voice) return 'loading'; return voice.start()")
			if s, _ := v.(string); err == nil && s != "loading" {
				if s != "" {
					return struct{}{}, errors.New(s)
				}
				return struct{}{}, nil
			}
			if err := sleep(ctx, 100*time.Millisecond); err != nil {
				return struct{}{}, err
			}
		}
		return struct{}{}, errors.New("the dictation page didn't load")
	}, func(_ struct{}, err error) {
		if err != nil {
			p.listening = false
			p.fail(err)
			return
		}
		p.loaded = true
		p.follow()
	})
}

// state is the recognizer's, read from the page.
type state struct {
	text      string
	listening bool
	problem   string
}

// follow reads what the recognizer hears into the draft until it ends.
func (p *Plugin) follow() {
	page := p.page
	kernel.Go(p.ctx, &p.run, func(ctx context.Context) (state, error) {
		if err := sleep(ctx, 200*time.Millisecond); err != nil {
			return state{}, err
		}
		v, err := page.Eval(ctx, "return voice.state()")
		if err != nil {
			return state{}, err
		}
		m, _ := v.(map[string]any)
		var st state
		st.text, _ = m["text"].(string)
		st.listening, _ = m["listening"].(bool)
		st.problem, _ = m["error"].(string)
		return st, nil
	}, func(st state, err error) {
		if err != nil {
			p.listening = false
			p.fail(err)
			return
		}
		if st.text != p.heard {
			p.heard = st.text
			p.chat.SetDraft(join(p.base, st.text))
		}
		if !st.listening {
			p.listening, p.stopping = false, false
			if st.problem != "" && st.problem != "aborted" && st.problem != "no-speech" {
				p.fail(errors.New(problemText(st.problem)))
			}
			p.ctx.Invalidate()
			return
		}
		p.follow()
	})
}

// stop asks the recognizer to finish; what it heard last still arrives.
func (p *Plugin) stop() {
	if !p.listening || p.page == nil || p.stopping {
		return
	}
	p.stopping = true
	page := p.page
	var t kernel.Task
	kernel.Go(p.ctx, &t, func(ctx context.Context) (struct{}, error) {
		_, err := page.Eval(ctx, "voice.stop()")
		return struct{}{}, err
	}, func(struct{}, error) {})
}

func (p *Plugin) fail(err error) {
	p.host.Toast(ui.Toast{Title: "Dictation stopped", Description: err.Error(), Type: components.ToastError})
	p.ctx.Invalidate()
}

func (p *Plugin) button(c *ui.Context) {
	pal := native.PaletteFor(c)
	label, icon, col := "Dictate (⇧⌘M)", lucide.Mic, pal.Muted
	if p.listening {
		label, icon, col = "Stop dictating", lucide.MicOff, pal.Danger
	}
	c.Key("dictate")
	b := ui.ButtonBase(c).Label(label).Tooltip(label).Size(28, 28).Radius(7).Center().Cursor(ui.CursorPointer).OnClick(p.toggle)
	switch {
	case p.listening:
		b.Background(pal.Danger.Alpha(.12))
	case b.Hovered():
		b.Background(pal.Hover)
	}
	b.Children(func() { ui.Icon(c, icon()).FontSize(14).TextColor(col) })
}

// join puts dictated text after the draft, with a space between.
func join(base, heard string) string {
	if heard == "" {
		return base
	}
	if base == "" || strings.HasSuffix(base, " ") || strings.HasSuffix(base, "\n") {
		return base + heard
	}
	return base + " " + heard
}

// problemText words a recognizer's error for the user.
func problemText(code string) string {
	switch code {
	case "not-allowed", "service-not-allowed":
		return "Wada may not use the microphone or speech recognition. Allow it in System Settings → Privacy & Security."
	case "audio-capture":
		return "No microphone could be used."
	case "network":
		return "Speech recognition needs the network."
	case "unsupported":
		return "Speech recognition isn't available here."
	}
	return "Speech recognition failed: " + code
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// writePage writes the dictation page and returns its path.
func writePage(dataDir string) (string, error) {
	if dataDir == "" {
		dataDir = os.TempDir()
	}
	dir := filepath.Join(dataDir, "voice")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "dictation.html")
	return path, os.WriteFile(path, []byte(dictationPage), 0o600)
}

// dictationPage runs the recognizer: voice.start() returns "" or why it
// can't, voice.stop() ends it, and voice.state() is what it heard so far.
const dictationPage = `<!doctype html><meta charset=utf-8><title>Dictation</title>
<script>
window.voice = (() => {
  const R = window.SpeechRecognition || window.webkitSpeechRecognition;
  let rec = null, done = "", now = "", listening = false, error = "";
  return {
    start() {
      if (!R) return "unsupported";
      if (rec) rec.abort();
      done = ""; now = ""; error = "";
      rec = new R();
      rec.continuous = true;
      rec.interimResults = true;
      rec.onresult = (e) => {
        now = "";
        for (let i = e.resultIndex; i < e.results.length; i++) {
          const r = e.results[i];
          if (r.isFinal) done += r[0].transcript; else now += r[0].transcript;
        }
      };
      rec.onerror = (e) => { error = e.error || "error"; };
      rec.onend = () => { listening = false; };
      try { rec.start(); } catch (e) { return String(e && e.message || e); }
      listening = true;
      return "";
    },
    stop() { if (rec) rec.stop(); },
    state() { return { text: (done + now).trim(), listening, error }; },
  };
})();
</script>`
