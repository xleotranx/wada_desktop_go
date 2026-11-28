package main

import (
	"bufio"
	"flag"
	"fmt"
	"strings"
	"testing"

	"wada-desktop-go/internal/protocol"
)

func TestParseInterspersed(t *testing.T) {
	fs := flag.NewFlagSet("host add", flag.ContinueOnError)
	cred := fs.String("credential", "", "")
	pos, err := parseInterspersed(fs, []string{"mac", "--credential", "secret", "my-mac.local", "--", "-x"})
	if err != nil {
		t.Fatal(err)
	}
	if *cred != "secret" || fmt.Sprint(pos) != "[mac my-mac.local -x]" {
		t.Fatalf("credential = %q, positional = %v", *cred, pos)
	}
}

func TestAskInteraction(t *testing.T) {
	ask := func(in protocol.Interaction, typed string) protocol.InteractionAnswer {
		in.ID, in.Session = "q1", "s1"
		return askInteraction(bufio.NewReader(strings.NewReader(typed)), in)
	}
	if a := ask(protocol.Interaction{Kind: protocol.InteractionConfirm}, "yes\n"); !a.Confirmed || a.Cancelled || a.ID != "q1" || a.Session != "s1" {
		t.Fatalf("confirm = %+v", a)
	}
	if a := ask(protocol.Interaction{Kind: protocol.InteractionSelect, Options: []string{"a", "b"}}, "2\n"); a.Value != "b" || a.Cancelled {
		t.Fatalf("select = %+v", a)
	}
	if a := ask(protocol.Interaction{Kind: protocol.InteractionSelect, Options: []string{"a"}}, "\n"); !a.Cancelled {
		t.Fatalf("empty select = %+v, want cancelled", a)
	}
	if a := ask(protocol.Interaction{Kind: protocol.InteractionInput, Prefill: "main"}, "\n"); a.Value != "main" || a.Cancelled {
		t.Fatalf("input with prefill = %+v", a)
	}
	if a := ask(protocol.Interaction{Kind: protocol.InteractionInput}, ""); !a.Cancelled {
		t.Fatalf("input at end of input = %+v, want cancelled", a)
	}
}

func TestWorktreeFlags(t *testing.T) {
	for _, tc := range []struct {
		args []string
		mode protocol.StartMode
		iso  protocol.Isolation
		from string
		pr   int
		path string
		bad  bool
	}{
		{args: nil, mode: protocol.StartBranchOff, iso: protocol.IsolationWorktree},
		{args: []string{"--base", "origin/dev"}, mode: protocol.StartBranchOff, iso: protocol.IsolationWorktree, from: "origin/dev"},
		{args: []string{"--checkout", "fix"}, mode: protocol.StartCheckoutBranch, iso: protocol.IsolationWorktree, from: "fix"},
		{args: []string{"--pr", "12"}, mode: protocol.StartCheckoutPR, iso: protocol.IsolationWorktree, pr: 12},
		{args: []string{"--existing", "/w/x"}, iso: protocol.IsolationExisting, path: "/w/x"},
		{args: []string{"--pr", "1", "--checkout", "x"}, bad: true},
	} {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		f := addWorktreeFlags(fs)
		if err := fs.Parse(tc.args); err != nil {
			t.Fatal(err)
		}
		req, err := f.request("prj_1")
		if tc.bad {
			if err == nil {
				t.Fatalf("%v: accepted", tc.args)
			}
			continue
		}
		if err != nil || req.Project != "prj_1" || req.Mode != tc.mode || req.Isolation != tc.iso || req.From != tc.from ||
			req.PR != tc.pr || req.Path != tc.path || req.IdempotencyKey == "" {
			t.Fatalf("%v: %+v %v", tc.args, req, err)
		}
	}
}
