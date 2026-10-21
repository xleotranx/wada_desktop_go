package desktop

import (
	"testing"

	"wada-desktop-go/internal/agent"

	"github.com/egoist/mygo/ui"
)

// TestChatTextSettingAndKeys drives the Appearance choices and the
// Cmd+= / Cmd+- / Cmd+0 steps, including their limits.
func TestChatTextSettingAndKeys(t *testing.T) {
	a := newTestApp(t)
	a.openSettings(tabAppearance)
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if !tt.HasText("Conversation text") {
		t.Fatalf("the Appearance page has no conversation text: %v", tt.Texts())
	}
	if err := tt.Click("Conversation text: Large"); err != nil {
		t.Fatal(err)
	}
	if a.preferences.chatText != chatTextLarge {
		t.Fatalf("clicking Large saved %q", a.preferences.chatText)
	}

	tt.Key(ui.Cmd, ui.KeyEqual)
	if a.preferences.chatText != chatTextHuge {
		t.Fatalf("Cmd+= reached %q", a.preferences.chatText)
	}
	tt.Key(ui.Cmd, ui.KeyEqual)
	if a.preferences.chatText != chatTextHuge {
		t.Fatalf("Cmd+= passed the largest size: %q", a.preferences.chatText)
	}
	tt.Key(ui.Cmd, ui.KeyMinus)
	if a.preferences.chatText != chatTextLarge {
		t.Fatalf("Cmd+- reached %q", a.preferences.chatText)
	}
	tt.Key(ui.Cmd, ui.Key0)
	if a.preferences.chatText != chatTextDefault {
		t.Fatalf("Cmd+0 reached %q", a.preferences.chatText)
	}
}

// TestChatTextScalesTheTranscript applies the choice to the markdown theme
// the answers and notes use.
func TestChatTextScalesTheTranscript(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessions = []agent.Session{{ID: "ses_a", Title: "Alpha"}}
	a.conversation.target.selectSession("ses_a")
	a.conversation.selected = "Alpha"
	a.preferences.chatText = chatTextLarge
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	want := float32(14) * chatTextScale(chatTextLarge)
	if got := a.layout.mdTheme.FontSize; got != want {
		t.Fatalf("markdown font %v, want %v", got, want)
	}
	if a.preferences.chatText != chatTextLarge {
		t.Fatalf("the size changed to %q", a.preferences.chatText)
	}
}

// TestChatTextMetadata carries the choice through a save and load.
func TestChatTextMetadata(t *testing.T) {
	a := newTestApp(t)
	a.preferences.chatText = "nonsense"
	a.restoreState(appMetadata{chatText: "nonsense"})
	if a.preferences.chatText != chatTextDefault {
		t.Fatalf("unknown size restored as %q", a.preferences.chatText)
	}
}
