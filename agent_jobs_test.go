package main

import "testing"

func TestServerSwitchReplacesFullQueue(t *testing.T) {
	a := newApp()
	a.servers = append(a.servers, serverConn{id: "box", url: "http://localhost:1234"})
	for range cap(a.jobs) {
		a.jobs <- agentJob{kind: jobRefresh}
	}
	a.useServer("box")
	job := <-a.jobs
	if job.kind != jobServer || job.server.id != "box" || len(a.jobs) != 0 {
		t.Fatalf("switch left obsolete work queued: %+v, %d more jobs", job, len(a.jobs))
	}
}

func TestServerSwitchPrecedesRetryWork(t *testing.T) {
	a := newApp()
	a.servers = append(a.servers, serverConn{id: "box"})
	a.agentSend("old prompt")
	stash := []agentJob{<-a.jobs}
	a.useServer("box")
	a.agentSend("new prompt")
	job, ready := pollAgentJob(a.jobs, &stash)
	if !ready || job.kind != jobServer || job.server.id != "box" {
		t.Fatalf("retry work ran before the switch: %+v", job)
	}
	if stash[0].route == job.route {
		t.Fatal("old retry work shares the new connection's generation")
	}
	// A completed switch discards the old attempt's retry stash.
	stash = nil
	next, ready := pollAgentJob(a.jobs, &stash)
	if !ready || next.kind != jobPrompt || next.text != "new prompt" || next.route != job.route {
		t.Fatalf("the new prompt did not follow its switch: %+v", next)
	}

	// Returning to the first server creates another generation, even
	// though the server ID matches requests from the original connection.
	a.useServer(localServerID)
	job = <-a.jobs
	if job.route == 0 || job.route == next.route {
		t.Fatal("returning to a server reused an earlier connection generation")
	}
}

func TestRetryJobsKeepTheirOrder(t *testing.T) {
	jobs := make(chan agentJob, 2)
	jobs <- agentJob{kind: jobPrompt, text: "second"}
	jobs <- agentJob{kind: jobPrompt, text: "third"}
	stash := []agentJob{{kind: jobPrompt, text: "first"}}
	for _, want := range []string{"first", "second", "third"} {
		job, ready := pollAgentJob(jobs, &stash)
		if !ready || job.text != want {
			t.Fatalf("got %+v, want %q", job, want)
		}
	}
	if _, ready := pollAgentJob(jobs, &stash); ready {
		t.Fatal("the drained queue reported another job")
	}
}

func TestDraftRejectsUnrelatedSessionEvents(t *testing.T) {
	a := newApp()
	a.startNewChat()
	a.draft = "my question"
	a.send()
	for _, ev := range []agentEvent{
		{Type: "session.text.started", Data: []byte(`{"sessionID":"other","assistantMessageID":"msg","ordinal":0}`)},
		{Type: "session.execution.started", Data: []byte(`{"sessionID":"other"}`)},
		{Type: "permission.asked", Data: []byte(`{"sessionID":"other","id":"ask","action":"shell"}`)},
	} {
		a.apply(ev)
	}
	if len(a.blocks) != 1 || a.blocks[0].kind != kindUser || len(a.pending) != 0 || a.busySession != "" {
		t.Fatalf("unrelated events entered the pending turn: %+v", a.blocks)
	}
	a.selectedSession = "mine"
	a.apply(agentEvent{Type: "session.text.started", Data: []byte(`{"assistantMessageID":"anonymous","ordinal":0}`)})
	if len(a.blocks) != 1 {
		t.Fatal("an event without a session ID entered the selected conversation")
	}
	a.apply(agentEvent{Type: "session.text.started", Data: []byte(`{"sessionID":"mine","assistantMessageID":"mine-msg","ordinal":0}`)})
	if len(a.blocks) != 2 || a.blocks[1].kind != kindStream {
		t.Fatal("the adopted conversation did not accept its own stream")
	}
}
