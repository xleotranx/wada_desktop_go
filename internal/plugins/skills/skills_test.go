package skills

import (
	"errors"
	"slices"
	"testing"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/plugintest"

	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
	"github.com/xleotranx/wada-kit/kittest"
)

func start(t *testing.T) (*kerneltest.Harness, *Plugin, *plugintest.Skills, *plugintest.Chat, *kittest.Host) {
	t.Helper()
	skills := plugintest.NewSkills(
		agent.Skill{ID: "git-release", Name: "git-release", Description: "Cut a release"},
		agent.Skill{ID: "code-review", Name: "code-review", Description: "Review changes"},
	)
	ctl, host := &plugintest.Chat{}, &kittest.Host{}
	ctl.Conv.Directory = "/w/app"
	var p *Plugin
	h := kerneltest.New(t, skills.New, ctl.New, host.New, kerneltest.Keep(&p, func() *Plugin { return &Plugin{} }))
	h.Flush()
	return h, p, skills, ctl, host
}

// completion is what the composer sees of the plugin.
func completion(h *kerneltest.Harness) chat.Completion {
	return kernel.Items(h, chat.Completions)[0].Value
}

func ids(cs []chat.Candidate) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.ID)
	}
	return out
}

func TestTypingListsSkillsForTheFolder(t *testing.T) {
	h, _, skills, _, _ := start(t)
	c := completion(h)
	c.Query("", true)
	if len(c.Candidates()) != 0 {
		t.Fatal("candidates before the lookup answered")
	}
	h.Flush()
	if got := ids(c.Candidates()); !slices.Equal(got, []string{"code-review", "git-release"}) {
		t.Fatalf("candidates %q", got)
	}
	first := c.Candidates()[0]
	if first.Label != "code-review" || first.Detail != "Review changes" || first.Icon == nil {
		t.Fatalf("candidate %+v", first)
	}
	if got := skills.Calls(); !slices.Equal(got, []string{"skills /w/app"}) {
		t.Fatalf("calls %q", got)
	}
	// Typing filters the list already fetched; another keystroke is not a
	// new lookup.
	c.Query("git", true)
	h.Flush()
	if got := ids(c.Candidates()); !slices.Equal(got, []string{"git-release"}) {
		t.Fatalf("filtered %q", got)
	}
	if got := skills.Calls(); len(got) != 1 {
		t.Fatalf("lookups %q", got)
	}
}

func TestChoosingASkillNamesItInTheMessage(t *testing.T) {
	h, _, _, _, _ := start(t)
	c := completion(h)
	c.Query("", true)
	h.Flush()
	if got := c.Accept(chat.Candidate{ID: "git-release"}); got != "/git-release " {
		t.Fatalf("accepted %q", got)
	}
	if got := c.Accept(chat.Candidate{ID: "gone"}); got != "" {
		t.Fatalf("accepted an unknown skill: %q", got)
	}
}

func TestEverySkillTheTextNamesGoesWithTheMessage(t *testing.T) {
	h, _, _, _, _ := start(t)
	c := completion(h)
	c.Query("", true)
	h.Flush()
	d, err := send(h, chat.Draft{Text: "Cut a release with /git-release, then let /code-review look."})
	if err != nil {
		t.Fatal(err)
	}
	want := []agent.SkillRef{{ID: "git-release"}, {ID: "code-review"}}
	if !slices.Equal(d.Skills, want) {
		t.Fatalf("skills %+v", d.Skills)
	}
	if d.Text != "Cut a release with /git-release, then let /code-review look." {
		t.Fatalf("text changed: %q", d.Text)
	}
	// A skill removed from the text is not attached.
	d, err = send(h, chat.Draft{Text: "Cut a release"})
	if err != nil || len(d.Skills) != 0 {
		t.Fatalf("removed skill: %+v, %v", d.Skills, err)
	}
}

func TestOnlyASlashWordAttachesASkill(t *testing.T) {
	h, _, _, _, _ := start(t)
	c := completion(h)
	c.Query("", true)
	h.Flush()
	for _, tc := range []struct {
		text   string
		attach bool
	}{
		{"Use /git-release.", true},
		{"Use (see /git-release)", true},
		{"Use /git-release-notes", false},
		{"See https://example.com/git-release", false},
		{"Use git-release", false},
	} {
		d, err := send(h, chat.Draft{Text: tc.text})
		if err != nil {
			t.Fatal(err)
		}
		if got := len(d.Skills) == 1; got != tc.attach {
			t.Fatalf("%q: attached=%v, want %v", tc.text, got, tc.attach)
		}
	}
}

func TestTheListFollowsTheConversationsFolder(t *testing.T) {
	h, _, skills, ctl, _ := start(t)
	c := completion(h)
	c.Query("", true)
	h.Flush()
	ctl.SetDirectory("/w/other")
	c.Query("", true)
	h.Flush()
	if got := skills.Calls(); !slices.Equal(got, []string{"skills /w/app", "skills /w/other"}) {
		t.Fatalf("calls %q", got)
	}
}

func TestAFailedLookupIsReportedOncePerWord(t *testing.T) {
	h, _, skills, _, host := start(t)
	skills.Fail = errors.New("offline")
	c := completion(h)
	c.Query("", true)
	h.Flush()
	if !slices.Equal(host.Toasts, []string{"Skills could not be listed"}) {
		t.Fatalf("toasts %q", host.Toasts)
	}
	if len(c.Candidates()) != 0 {
		t.Fatal("a failed lookup offered skills")
	}
	c.Query("g", true)
	h.Flush()
	if len(host.Toasts) != 1 || len(skills.Calls()) != 1 {
		t.Fatalf("a keystroke repeated the failure: toasts=%q calls=%q", host.Toasts, skills.Calls())
	}
	c.Query("", false)
	c.Query("", true)
	h.Flush()
	if len(host.Toasts) != 2 {
		t.Fatalf("a new word did not retry: %q", host.Toasts)
	}
}

// send runs the outgoing pipeline as the chat does, committing what it accepts.
func send(h *kerneltest.Harness, d chat.Draft) (chat.Draft, error) {
	out, commit, err := kernel.Run(h, chat.Outgoing, d)
	if err == nil {
		commit()
	}
	return out, err
}
