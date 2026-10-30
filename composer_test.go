package main

import (
	"github.com/xleotranx/wada-kit/kernel"
	"testing"
	"wada-desktop-go/internal/contract/agent"

	"github.com/egoist/mygo/ui"
)

func TestComposerFooterAcrossWindowWidths(t *testing.T) {
	a := startApp(t)
	for _, width := range []int{1200, 700, 460, 1200} {
		a.tt.SetSize(width, 700)
		a.tt.Frame()
		a.tt.Frame()
		input, ok := a.tt.Find("Compose")
		if !ok {
			t.Fatal("missing composer")
		}
		model, ok := a.tt.Find("Default model")
		if !ok || model.W < 100 || model.Y < input.Y+input.H {
			t.Fatalf("model clipped at %d: %+v in %q", width, model, a.tt.Texts())
		}
		captureMomentum(t, a.tt, "composer-"+map[int]string{1200: "wide", 700: "medium", 460: "narrow"}[width])
		if width < 700 {
			if err := a.tt.Click("More composer options"); err != nil {
				t.Fatal(err)
			}
			a.tt.Frame()
			if !a.tt.HasText("Build") {
				t.Fatalf("mode missing in overflow: %q", a.tt.Texts())
			}
			if err := a.tt.Click("Build"); err != nil {
				t.Fatal(err)
			}
			a.tt.Frame()
			if err := a.tt.Click("Plan"); err != nil {
				t.Fatal(err)
			}
			a.tt.Frame()
			a.tt.Key(0, ui.KeyEscape)
			a.tt.Frame()
		}
	}
}

func TestComposerKeepsModelAndReasoningTogetherWhenNarrow(t *testing.T) {
	a := startApp(t)
	ref := agent.ModelRef{Provider: "test", ID: "sonnet", Variant: "medium"}
	a.agent.Catalog = []agent.Model{{Ref: ref, Name: "Claude Sonnet 4.5", Variants: []string{"low", "medium", "high"}}}
	a.agent.Default = ref
	kernel.Emit(a.shell.Kernel, agent.ModelsChanged, struct{}{})
	a.until("model catalog", func() bool { return a.tt.HasText("Claude Sonnet 4.5") })
	for _, width := range []int{1200, 360, 320} {
		a.tt.SetSize(width, 700)
		a.tt.Frame()
		a.tt.Frame()
		model, modelOK := a.tt.Find("Claude Sonnet 4.5")
		effort, effortOK := a.tt.Find("medium")
		if !modelOK || !effortOK || model.Y != effort.Y || effort.X < model.X+model.W || effort.X+effort.W > float32(width) {
			t.Fatalf("model and effort separated at %d: %+v %+v texts=%q", width, model, effort, a.tt.Texts())
		}
		captureMomentum(t, a.tt, map[int]string{1200: "composer-group-wide", 360: "composer-group-narrow", 320: "composer-group-small"}[width])
	}
	if err := a.tt.Click("medium"); err != nil {
		t.Fatal(err)
	}
	a.tt.Frame()
	if err := a.tt.Click("high"); err != nil {
		t.Fatal(err)
	}
	a.tt.Frame()
	if !a.tt.HasText("high") {
		t.Fatal("reasoning selection did not change")
	}
}
