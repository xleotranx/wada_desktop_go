package worktree

import (
	"context"
	"errors"
	"strings"
	"testing"

	"wada-desktop-go/internal/protocol"
)

func TestParseRemoteHostVariants(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"git@github.com:org/repo.git", "github.com"},
		{"ssh://git@github.enterprise.local/org/repo.git", "github.enterprise.local"},
		{"https://github.com/org/repo.git", "github.com"},
		{"https://gitlab.example.org/team/proj", "gitlab.example.org"},
		{"", ""},
		{"/local/bare/repo.git", ""},
	}
	for _, tc := range cases {
		if got := parseRemoteHost(tc.url); got != tc.want {
			t.Fatalf("parseRemoteHost(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}
}

func TestForgeSkipsPRProbeWhenHostUnauthenticated(t *testing.T) {
	var calls []string
	f := &Forge{
		authCache: map[string]authEntry{},
		run: func(_ context.Context, _ string, name string, args ...string) (string, error) {
			calls = append(calls, name+" "+strings.Join(args, " "))
			if name == "git" {
				return "git@github.com:xleotranx/wada_desktop_go.git\n", nil
			}
			if name == "gh" && len(args) > 0 && args[0] == "auth" {
				return "", errors.New("not logged in")
			}
			t.Fatalf("unexpected probe on unauthenticated host: %s %v", name, args)
			return "", nil
		},
	}

	pr, err := f.PRStatus(context.Background(), "/tmp/wt")
	if err != nil || pr != nil {
		t.Fatalf("PRStatus = (%+v, %v), want (nil, nil)", pr, err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected only remote + auth check, got %v", calls)
	}
}

func TestForgePRStatusAndCreate(t *testing.T) {
	var createdArgs []string
	viewJSON := `{"number":42,"title":"Add E2EE relay","state":"OPEN","isDraft":false,"url":"https://github.com/xleotranx/wada_desktop_go/pull/42","reviewDecision":"APPROVED","statusCheckRollup":[{"status":"COMPLETED","conclusion":"SUCCESS"},{"status":"COMPLETED","conclusion":"SUCCESS"}]}`
	f := &Forge{
		authCache: map[string]authEntry{},
		run: func(_ context.Context, _ string, name string, args ...string) (string, error) {
			if name == "git" {
				return "https://github.com/xleotranx/wada_desktop_go.git\n", nil
			}
			if name == "gh" && len(args) >= 2 && args[0] == "auth" {
				return "Logged in", nil
			}
			if name == "gh" && len(args) >= 2 && args[0] == "pr" && args[1] == "view" {
				return viewJSON, nil
			}
			if name == "gh" && len(args) >= 2 && args[0] == "pr" && args[1] == "create" {
				createdArgs = append([]string(nil), args...)
				return "https://github.com/xleotranx/wada_desktop_go/pull/42\n", nil
			}
			return "", errors.New("unexpected command")
		},
	}

	pr, err := f.CreatePR(context.Background(), protocol.WorktreeCreatePRRequest{
		Directory: "/tmp/wt",
		Title:     "Add E2EE relay",
		Body:      "Summary",
		Base:      "main",
		Draft:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(createdArgs) == 0 || !strings.Contains(strings.Join(createdArgs, " "), "--draft") {
		t.Fatalf("createdArgs = %v", createdArgs)
	}
	if pr == nil || pr.Number != 42 || pr.Checks != "pass" || pr.ReviewDecision != "APPROVED" {
		t.Fatalf("pr = %+v", pr)
	}

	failing, err := parseGHPRJSON([]byte(`{"number":9,"state":"OPEN","statusCheckRollup":[{"status":"IN_PROGRESS"},{"conclusion":"FAILURE"}]}`))
	if err != nil || failing.Checks != "fail" {
		t.Fatalf("failing = %+v, err=%v", failing, err)
	}
	pending, err := parseGHPRJSON([]byte(`{"number":9,"state":"OPEN","statusCheckRollup":[{"status":"IN_PROGRESS"}]}`))
	if err != nil || pending.Checks != "pending" {
		t.Fatalf("pending = %+v, err=%v", pending, err)
	}
}
