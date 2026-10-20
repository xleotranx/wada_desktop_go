package main

import (
	"strings"
	"testing"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
)

// noticeRecorder stands in for the system's banner center: every notice
// the app would show lands in the returned slice.
func noticeRecorder(a *app) *[]desktopNotice {
	got := []desktopNotice{}
	a.runtime.notify = func(n desktopNotice) { got = append(got, n) }
	return &got
}

// TestBackgroundTurnBannersItsChat banners a turn that ends in a chat the
// window is not showing, naming the chat and where it works, and leaves
// the unread mark to say so in the sidebar.
func TestBackgroundTurnBannersItsChat(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	a.scope().sessions = []opencode.Session{{ID: "ses_bg", Title: "Fix the tests", Directory: dir}}
	got := noticeRecorder(a)
	a.applyUpdate(opencode.Update{Kind: opencode.ExecutionSucceeded, SessionID: "ses_bg"})
	if len(*got) != 1 {
		t.Fatalf("a background turn sent %d banners", len(*got))
	}
	note := (*got)[0]
	if note.id != "chat:ses_bg" || note.group != "chat:ses_bg" {
		t.Fatalf("banner identity %+v", note)
	}
	if note.title != "Fix the tests" || note.subtitle != dirBase(dir) {
		t.Fatalf("banner named %+v", note)
	}
	if !strings.Contains(note.body, "finished") {
		t.Fatalf("banner body %q", note.body)
	}
	if !a.scope().unread["ses_bg"] {
		t.Fatal("the banner replaced the unread mark")
	}
	if a.preferences.notifyFinished == false {
		t.Fatal("banners should be on by default")
	}
}

// TestNotificationPreferencesGateBanners turns each kind off and on, keeps
// a stopped turn quiet, and words a failure as one.
func TestNotificationPreferencesGateBanners(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessions = []opencode.Session{{ID: "ses_bg", Title: "Ship it"}}
	got := noticeRecorder(a)
	a.preferences.notifyFinished = false
	a.applyUpdate(opencode.Update{Kind: opencode.ExecutionSucceeded, SessionID: "ses_bg"})
	if len(*got) != 0 {
		t.Fatalf("finished banners off sent %d", len(*got))
	}

	a.preferences.notifyApprovals = false
	a.applyUpdate(opencode.Update{Kind: opencode.PermissionAsked, SessionID: "ses_bg",
		Permission: &opencode.PermissionData{ID: "perm_1", SessionID: "ses_bg", Action: "bash"}})
	if len(*got) != 0 {
		t.Fatalf("approval banners off sent %d", len(*got))
	}

	a.preferences.notifyApprovals = true
	a.applyUpdate(opencode.Update{Kind: opencode.PermissionAsked, SessionID: "ses_bg",
		Permission: &opencode.PermissionData{ID: "perm_2", SessionID: "ses_bg", Action: "bash"}})
	if len(*got) != 1 || !strings.Contains((*got)[0].body, "run a shell command") {
		t.Fatalf("approval banner %+v", *got)
	}

	a.preferences.notifyFinished = true
	a.applyUpdate(opencode.Update{Kind: opencode.ExecutionInterrupted, SessionID: "ses_bg"})
	if len(*got) != 1 {
		t.Fatalf("a stopped turn sent %d banners", len(*got)-1)
	}
	a.applyUpdate(opencode.Update{Kind: opencode.ExecutionFailed, SessionID: "ses_bg"})
	if len(*got) != 2 || !strings.Contains((*got)[1].body, "error") {
		t.Fatalf("failure banner %+v", *got)
	}
}

// TestNotificationClickOpensItsChat brings a clicked banner's conversation
// on show, and ignores an identity it did not write.
func TestNotificationClickOpensItsChat(t *testing.T) {
	a := newTestApp(t)
	a.openNoticeChat("something-else")
	if len(commandsOf(a)) != 0 {
		t.Fatal("a foreign notification opened a chat")
	}
	a.openNoticeChat("chat:ses_clicked")
	job := <-commandsOf(a)
	if job.kind != "open" || job.sid != "ses_clicked" {
		t.Fatalf("the click queued %+v", job)
	}
	if a.conversation.target.session != "ses_clicked" {
		t.Fatalf("the view shows %q", a.conversation.target.session)
	}
}

// TestSettingsGeneralTogglesNotifications shows the General page and saves
// both switches through the preference they edit.
func TestSettingsGeneralTogglesNotifications(t *testing.T) {
	a := newTestApp(t)
	a.openSettings(tabGeneral)
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if !tt.HasText("Notifications") || !tt.HasText("Finished turns") || !tt.HasText("Approvals") {
		t.Fatalf("the General page lacks its switches: %v", tt.Texts())
	}
	if err := tt.Click("Notify when a turn finishes"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.preferences.notifyFinished {
		t.Fatal("the finished-turns switch did not turn off")
	}
	if snap := a.stateSnapshot(); snap.notifyFinished {
		t.Fatal("the snapshot kept the old switch")
	}
	if err := tt.Click("Notify when a turn finishes"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.preferences.notifyFinished {
		t.Fatal("the finished-turns switch did not turn back on")
	}
	if err := tt.Click("Notify when a chat needs approval"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.preferences.notifyApprovals {
		t.Fatal("the approvals switch did not turn off")
	}
}
