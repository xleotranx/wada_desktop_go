package desktop

import (
	"testing"
	"wada-desktop-go/internal/chat"
	"wada-desktop-go/internal/runner"

	"github.com/egoist/mygo/ui"
)

func TestFullQueuePreservesUnsentDraftAndTranscript(t *testing.T) {
	for _, session := range []string{"", "ses-existing"} {
		t.Run("session="+session, func(t *testing.T) {
			a := newTestApp(t)
			a.conversation.target.selectSession(chat.SessionID(session))
			seedTranscript(a, []chat.Block{{Kind: chat.KindText, Text: "Previous answer"}})
			a.conversation.draft = "  Keep this unsent question  "
			for range cap(commandsOf(a)) {
				commandsOf(a) <- commandRecord{kind: "refresh"}
			}
			if a.send() {
				t.Fatal("full queue reported the prompt accepted")
			}
			if a.conversation.commandError == "" {
				t.Fatal("full queue supplied no rejection feedback")
			}
			tt := ui.NewTester(a.view, 1400, 920)
			if !tt.HasText(a.conversation.commandError) {
				t.Fatal("the composer did not display queue rejection feedback")
			}
			if a.conversation.draft != "  Keep this unsent question  " {
				t.Fatalf("full queue consumed the unsent draft: %q", a.conversation.draft)
			}
			if a.conversation.transcript.Len() != 1 || a.conversation.transcript.Row(0).Kind != chat.KindText || a.conversation.transcript.Row(0).Text != "Previous answer" {
				t.Fatalf("full queue changed the transcript: %+v", a.conversation.transcript.Snapshot())
			}
			if len(commandsOf(a)) != cap(commandsOf(a)) {
				t.Fatalf("full queue changed admission count: %d", len(commandsOf(a)))
			}
		})
	}
}

func TestAcceptedSendAppendsOnceAndClearsDraft(t *testing.T) {
	a := newTestApp(t)
	a.conversation.target.selectSession("ses-existing")
	seedTranscript(a, []chat.Block{{Kind: chat.KindText, Text: "Previous answer"}})
	a.conversation.draft = "  Send this question  "
	if !a.send() {
		t.Fatal("available queue rejected the prompt")
	}
	if a.conversation.draft != "" || a.conversation.transcript.Len() != 2 || a.conversation.transcript.Row(1).Kind != chat.KindUser || a.conversation.transcript.Row(1).Text != "  Send this question  " {
		t.Fatalf("accepted prompt did not enter the transcript once: draft=%q, blocks=%+v", a.conversation.draft, a.conversation.transcript.Snapshot())
	}
	if len(commandsOf(a)) != 1 {
		t.Fatalf("accepted prompt queued %d jobs, want 1", len(commandsOf(a)))
	}
	job := <-commandsOf(a)
	if job.kind != "prompt" || job.sid != "ses-existing" || job.text != "Send this question" || job.route != a.connections.route {
		t.Fatalf("accepted prompt queued %+v", job)
	}
	if a.send() {
		t.Fatal("empty draft reported a prompt accepted")
	}
	if len(commandsOf(a)) != 0 || a.conversation.transcript.Len() != 2 {
		t.Fatal("empty draft submitted or appended the accepted prompt again")
	}
}

func TestClosedAppPreservesUnsentDraft(t *testing.T) {
	a := newTestApp(t)
	a.conversation.target.selectSession("ses-existing")
	seedTranscript(a, []chat.Block{{Kind: chat.KindText, Text: "Previous answer"}})
	a.conversation.draft = "Keep this after closing"
	a.closeWork()
	if a.send() {
		t.Fatal("closed app reported the prompt accepted")
	}
	if a.conversation.commandError == "" {
		t.Fatal("closed app supplied no rejection feedback")
	}
	if a.conversation.draft != "Keep this after closing" || a.conversation.transcript.Len() != 1 || a.conversation.transcript.Row(0).Text != "Previous answer" || len(commandsOf(a)) != 0 {
		t.Fatalf("closed app accepted a prompt: draft=%q blocks=%+v jobs=%d", a.conversation.draft, a.conversation.transcript.Snapshot(), len(commandsOf(a)))
	}
}

func TestQueueJobReportsAdmissionAndClearsRecoveredError(t *testing.T) {
	a := newTestApp(t)
	installCommandFixture(a, 1)
	a.connections.route = 17
	if got := a.queueCommand(runner.Refresh{}); got != runner.Accepted {
		t.Fatalf("empty queue admission=%v, want accepted", got)
	}
	if got := a.queueCommand(runner.Prompt{Text: "not admitted"}); got != runner.Busy {
		t.Fatalf("full queue admission=%v, want busy", got)
	}
	if a.conversation.commandError == "" {
		t.Fatal("busy admission supplied no rejection feedback")
	}
	if job := <-commandsOf(a); job.kind != "refresh" || job.route != 17 {
		t.Fatalf("queue pressure replaced the admitted job: %+v", job)
	}
	if got := a.queueCommand(runner.Prompt{Text: "retry"}); got != runner.Accepted {
		t.Fatalf("recovered queue admission=%v, want accepted", got)
	}
	if a.conversation.commandError != "" {
		t.Fatalf("accepted retry retained rejection feedback: %q", a.conversation.commandError)
	}
	a.closeWork()
	if got := a.queueCommand(runner.Refresh{}); got != runner.Closed {
		t.Fatalf("closed app admission=%v, want closed", got)
	}
	if len(commandsOf(a)) != 1 {
		t.Fatal("closed admission changed the queued work")
	}
}

func TestAgentSendReportsBusyAndClosedRejection(t *testing.T) {
	a := newTestApp(t)
	installCommandFixture(a, 1)
	if !a.agentSend("accepted") {
		t.Fatal("available queue rejected the agent prompt")
	}
	if a.agentSend("busy") {
		t.Fatal("full queue reported the agent prompt accepted")
	}
	a.closeWork()
	if a.agentSend("closed") {
		t.Fatal("closed app reported the agent prompt accepted")
	}
	if job := <-commandsOf(a); job.kind != "prompt" || job.text != "accepted" || len(commandsOf(a)) != 0 {
		t.Fatalf("rejected commands changed accepted work: %+v", job)
	}
}
