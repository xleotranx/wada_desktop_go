package main

import (
	"testing"
	"wada-desktop-go/internal/chat"

	"github.com/egoist/mygo/ui"
)

func TestFullQueuePreservesUnsentDraftAndTranscript(t *testing.T) {
	for _, session := range []string{"", "ses-existing"} {
		t.Run("session="+session, func(t *testing.T) {
			a := newTestApp(t)
			a.selectedSession = session
			a.transcript.Replace([]chat.Block{{Kind: chat.KindText, Text: "Previous answer"}})
			a.draft = "  Keep this unsent question  "
			for range cap(a.jobs) {
				a.jobs <- agentJob{kind: jobRefresh}
			}
			if a.send() {
				t.Fatal("full queue reported the prompt accepted")
			}
			if a.commandError == "" {
				t.Fatal("full queue supplied no rejection feedback")
			}
			tt := ui.NewTester(a.view, 1400, 920)
			if !tt.HasText(a.commandError) {
				t.Fatal("the composer did not display queue rejection feedback")
			}
			if a.draft != "  Keep this unsent question  " {
				t.Fatalf("full queue consumed the unsent draft: %q", a.draft)
			}
			if len(a.transcript.Rows()) != 1 || a.transcript.Rows()[0].Kind != chat.KindText || a.transcript.Rows()[0].Text != "Previous answer" {
				t.Fatalf("full queue changed the transcript: %+v", a.transcript.Rows())
			}
			if len(a.jobs) != cap(a.jobs) {
				t.Fatalf("full queue changed admission count: %d", len(a.jobs))
			}
		})
	}
}

func TestAcceptedSendAppendsOnceAndClearsDraft(t *testing.T) {
	a := newTestApp(t)
	a.selectedSession = "ses-existing"
	a.transcript.Replace([]chat.Block{{Kind: chat.KindText, Text: "Previous answer"}})
	a.draft = "  Send this question  "
	if !a.send() {
		t.Fatal("available queue rejected the prompt")
	}
	if a.draft != "" || len(a.transcript.Rows()) != 2 || a.transcript.Rows()[1].Kind != chat.KindUser || a.transcript.Rows()[1].Text != "  Send this question  " {
		t.Fatalf("accepted prompt did not enter the transcript once: draft=%q, blocks=%+v", a.draft, a.transcript.Rows())
	}
	if len(a.jobs) != 1 {
		t.Fatalf("accepted prompt queued %d jobs, want 1", len(a.jobs))
	}
	job := <-a.jobs
	if job.kind != jobPrompt || job.sid != "ses-existing" || job.text != "Send this question" || job.route != a.route {
		t.Fatalf("accepted prompt queued %+v", job)
	}
	if a.send() {
		t.Fatal("empty draft reported a prompt accepted")
	}
	if len(a.jobs) != 0 || len(a.transcript.Rows()) != 2 {
		t.Fatal("empty draft submitted or appended the accepted prompt again")
	}
}

func TestClosedAppPreservesUnsentDraft(t *testing.T) {
	a := newTestApp(t)
	a.selectedSession = "ses-existing"
	a.transcript.Replace([]chat.Block{{Kind: chat.KindText, Text: "Previous answer"}})
	a.draft = "Keep this after closing"
	a.closeWork()
	if a.send() {
		t.Fatal("closed app reported the prompt accepted")
	}
	if a.commandError == "" {
		t.Fatal("closed app supplied no rejection feedback")
	}
	if a.draft != "Keep this after closing" || len(a.transcript.Rows()) != 1 || a.transcript.Rows()[0].Text != "Previous answer" || len(a.jobs) != 0 {
		t.Fatalf("closed app accepted a prompt: draft=%q blocks=%+v jobs=%d", a.draft, a.transcript.Rows(), len(a.jobs))
	}
}

func TestQueueJobReportsAdmissionAndClearsRecoveredError(t *testing.T) {
	a := newTestApp(t)
	a.jobs = make(chan agentJob, 1)
	a.route = 17
	if got := a.queueJob(agentJob{kind: jobRefresh}); got != admissionAccepted {
		t.Fatalf("empty queue admission=%v, want accepted", got)
	}
	if got := a.queueJob(agentJob{kind: jobPrompt, text: "not admitted"}); got != admissionBusy {
		t.Fatalf("full queue admission=%v, want busy", got)
	}
	if a.commandError == "" {
		t.Fatal("busy admission supplied no rejection feedback")
	}
	if job := <-a.jobs; job.kind != jobRefresh || job.route != 17 {
		t.Fatalf("queue pressure replaced the admitted job: %+v", job)
	}
	if got := a.queueJob(agentJob{kind: jobPrompt, text: "retry"}); got != admissionAccepted {
		t.Fatalf("recovered queue admission=%v, want accepted", got)
	}
	if a.commandError != "" {
		t.Fatalf("accepted retry retained rejection feedback: %q", a.commandError)
	}
	a.closeWork()
	if got := a.queueJob(agentJob{kind: jobRefresh}); got != admissionClosed {
		t.Fatalf("closed app admission=%v, want closed", got)
	}
	if len(a.jobs) != 1 {
		t.Fatal("closed admission changed the queued work")
	}
}

func TestAgentSendReportsBusyAndClosedRejection(t *testing.T) {
	a := newTestApp(t)
	a.jobs = make(chan agentJob, 1)
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
	if job := <-a.jobs; job.kind != jobPrompt || job.text != "accepted" || len(a.jobs) != 0 {
		t.Fatalf("rejected commands changed accepted work: %+v", job)
	}
}
