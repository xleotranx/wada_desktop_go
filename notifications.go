package main

import (
	"errors"
	"log"
	"strings"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo"
)

// noticeChatPrefix marks a banner's ID as naming a conversation, so the
// click handler can tell its own notifications from another future kind's.
const noticeChatPrefix = "chat:"

// desktopNotice is one banner the app sends to the system's notification
// center: the chat it names, where that chat works, and what happened.
type desktopNotice struct {
	id, title, subtitle, body, group string
}

// showDesktopNotice hands one banner to the platform. Showing can wait for
// the user's permission on the first one, so it runs off the UI thread;
// a refused permission is the user's choice, not an error to log.
func showDesktopNotice(n desktopNotice) {
	go func() {
		note := mygo.NewNotification(mygo.NotificationOptions{
			ID:       n.id,
			Title:    n.title,
			Subtitle: n.subtitle,
			Body:     n.body,
			Group:    n.group,
		})
		if err := note.Show(); err != nil && !errors.Is(err, mygo.ErrNotificationsDenied) {
			log.Printf("notification: %v", err)
		}
	}()
}

// noticeSession names the freshest row a banner is about, so the banner
// uses the title a row carries even when the list has not sorted one.
func (a *app) noticeSession(id string) opencode.Session {
	for _, s := range a.scope().sessions {
		if s.ID == id {
			return s
		}
	}
	return opencode.Session{ID: id}
}

// noticeTitle is the name a banner gives a chat: an untitled one is still
// a new chat.
func noticeTitle(s opencode.Session) string {
	if strings.TrimSpace(s.Title) == "" {
		return "New chat"
	}
	return s.Title
}

// notifyTurnEnded banners a turn that finished where the user was not
// looking: another chat was on show, or the window was in the background.
func (a *app) notifyTurnEnded(id string, failed bool) {
	if !a.preferences.notifyFinished || id == "" || a.chatArchived(id) {
		return
	}
	s := a.noticeSession(id)
	body := "The turn finished."
	if failed {
		body = "The turn stopped with an error."
	}
	a.showNotice(desktopNotice{
		id:       noticeChatPrefix + id,
		title:    noticeTitle(s),
		subtitle: dirBase(s.Directory),
		body:     body,
		group:    noticeChatPrefix + id,
	})
}

// notifyApproval banners an approval a chat is waiting on.
func (a *app) notifyApproval(id string, perm *opencode.PermissionData) {
	if !a.preferences.notifyApprovals || id == "" || perm == nil || a.chatArchived(id) {
		return
	}
	s := a.noticeSession(id)
	a.showNotice(desktopNotice{
		id:       noticeChatPrefix + id,
		title:    noticeTitle(s),
		subtitle: dirBase(s.Directory),
		body:     "Approval needed — " + permAsk(perm.Action) + ".",
		group:    noticeChatPrefix + id,
	})
}

// showNotice gives one banner to the platform's notifier, when this build
// and machine have one.
func (a *app) showNotice(n desktopNotice) {
	if a.runtime.closed || a.runtime.notify == nil {
		return
	}
	a.runtime.notify(n)
}

// noticeUnseen reports that the window is not in front, so a chat finishing
// on show still deserves a banner.
func (a *app) noticeUnseen() bool {
	w := a.runtime.win
	if w == nil {
		return false
	}
	return !w.IsVisible() || w.IsMinimized() || !w.IsFocused()
}

// openNoticeChat brings the conversation a clicked banner names on show.
func (a *app) openNoticeChat(id string) {
	sid, ok := strings.CutPrefix(id, noticeChatPrefix)
	if !ok || sid == "" || a.runtime.closed {
		return
	}
	a.openConversation(opencode.Session{ID: sid})
}
