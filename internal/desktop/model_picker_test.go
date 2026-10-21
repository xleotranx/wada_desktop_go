package desktop

import (
	"testing"

	"wada-desktop-go/internal/agent"

	"github.com/egoist/mygo/ui"
)

// modelPickerApp seeds a session with a small catalog and opens the model
// picker, ready for the keyboard.
func modelPickerApp(t *testing.T) (*app, *ui.Tester) {
	t.Helper()
	a := newTestApp(t)
	a.scope().models = []agent.Model{
		{ProviderID: "p", ID: "alpha", Name: "Alpha"},
		{ProviderID: "p", ID: "beta", Name: "Beta"},
		{ProviderID: "p", ID: "gamma", Name: "Gamma"},
	}
	a.scope().modelsLoaded = true
	a.scope().sessions = []agent.Session{{ID: "ses_z", Title: "Turn"}}
	a.conversation.target.selectSession("ses_z")
	a.conversation.selected = "Turn"
	a.openModelPicker()
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	return a, tt
}

// TestModelPickerKeyboardChooses walks the catalog with the arrows and
// picks with Enter, with the filter holding the keyboard on open.
func TestModelPickerKeyboardChooses(t *testing.T) {
	a, tt := modelPickerApp(t)
	if !a.layout.pickerOpen || !tt.HasText("Alpha") {
		t.Fatalf("the picker did not open: %v", tt.Texts())
	}
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	job := <-commandsOf(a)
	if job.kind != "model" || job.provider != "p" || job.model != "gamma" {
		t.Fatalf("the keyboard picked %+v", job)
	}
	if a.layout.pickerOpen {
		t.Fatal("choosing left the picker open")
	}
}

// TestModelPickerFilterKeyboard types a filter, sees the list narrow, and
// picks the only match with Enter.
func TestModelPickerFilterKeyboard(t *testing.T) {
	a, tt := modelPickerApp(t)
	tt.Type("gam")
	tt.Frame()
	if tt.HasText("Pick Alpha") || tt.HasText("Pick Beta") || !tt.HasText("Pick Gamma") {
		t.Fatalf("the filter did not narrow the rows: %v", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	job := <-commandsOf(a)
	if job.kind != "model" || job.model != "gamma" {
		t.Fatalf("the filtered pick is %+v", job)
	}
}

// TestModelPickerStartsOnTheCurrentModel opens the list on the row the
// chat runs on, so Enter keeps it.
func TestModelPickerStartsOnTheCurrentModel(t *testing.T) {
	a := newTestApp(t)
	a.scope().models = []agent.Model{
		{ProviderID: "p", ID: "alpha", Name: "Alpha"},
		{ProviderID: "p", ID: "beta", Name: "Beta"},
		{ProviderID: "p", ID: "gamma", Name: "Gamma"},
	}
	a.scope().modelsLoaded = true
	a.scope().sessions = []agent.Session{{ID: "ses_z", Title: "Turn",
		Model: agent.ModelRef{ProviderID: "p", ID: "beta"}}}
	a.conversation.target.selectSession("ses_z")
	a.conversation.selected = "Turn"
	a.openModelPicker()
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if got := a.filteredModels()[a.layout.modelSelected].ID; got != "beta" {
		t.Fatalf("the picker opened on %q", got)
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	job := <-commandsOf(a)
	if job.kind != "model" || job.model != "beta" {
		t.Fatalf("Enter moved the chat to %+v", job)
	}
}
