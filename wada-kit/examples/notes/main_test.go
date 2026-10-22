package main

import (
	"context"
	"testing"
	"time"

	"github.com/xleotranx/wada-kit/kernel/kerneltest"
	kitsettings "github.com/xleotranx/wada-kit/plugins/settings"
	kitshell "github.com/xleotranx/wada-kit/shell"

	"github.com/egoist/mygo/ui"
)

func TestANoteIsAddedAndKept(t *testing.T) {
	store := kerneltest.NewStore()
	s, err := kitshell.New(kitshell.Options{Name: "Notes", Post: func(fn func()) { fn() }, Store: store}, &notes{}, &kitsettings.Plugin{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		s.Close(ctx)
	}()
	tt := ui.NewTester(s.View, 900, 640)
	tt.Frame()
	if !tt.HasText("No notes yet.") {
		t.Fatalf("texts %q", tt.Texts())
	}
	if err := tt.Click("New note"); err != nil {
		t.Fatal(err)
	}
	tt.Type("Buy milk")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !tt.HasText("Buy milk") || tt.HasText("No notes yet.") {
		t.Fatalf("texts %q", tt.Texts())
	}
}
