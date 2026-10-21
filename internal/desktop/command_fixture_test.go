package desktop

import (
	"sync"

	"wada-desktop-go/internal/runner"
)

// commandRecord is a UI test observation, not the runner's private queue.
type commandRecord struct {
	kind                                                                     string
	route                                                                    uint64
	sid, dir, text, cursor, provider, model, variant, msg, request, decision string
	agent                                                                    string
	draft                                                                    uint64
	server                                                                   serverConn
	pins                                                                     []string
}

var testCommandQueues sync.Map

func commandsOf(a *app) chan commandRecord {
	queue, _ := testCommandQueues.Load(a)
	return queue.(chan commandRecord)
}
func installCommandFixture(a *app, capacity int) {
	queue := make(chan commandRecord, capacity)
	testCommandQueues.Store(a, queue)
	a.runtime.submitCommand = func(g runner.Generation, command runner.Command) runner.Admission {
		record := commandRecord{route: uint64(g)}
		switch c := command.(type) {
		case runner.Prompt:
			record.kind = "prompt"
			record.sid = string(c.Session)
			record.text = c.Text
			record.dir = c.Directory
			record.agent = c.Agent
			record.draft = c.Draft
			record.provider = c.Model.ProviderID
			record.model = c.Model.ID
			record.variant = c.Model.Variant
		case runner.Open:
			record.kind = "open"
			record.sid = string(c.Session)
		case runner.Stop:
			record.kind = "stop"
			record.sid = string(c.Session)
		case runner.Answer:
			record.kind = "answer"
			record.sid = string(c.Session)
			record.request = string(c.Request)
			record.decision = string(c.Decision)
		case runner.Rename:
			record.kind = "rename"
			record.sid = string(c.Session)
			record.text = c.Title
		case runner.Delete:
			record.kind = "delete"
			record.sid = string(c.Session)
		case runner.SelectModel:
			record.kind = "model"
			record.sid = string(c.Session)
			record.provider = c.Model.ProviderID
			record.model = c.Model.ID
			record.variant = c.Model.Variant
		case runner.More:
			record.kind = "more"
			record.cursor = c.Cursor
		case runner.Refresh:
			record.kind = "refresh"
		case runner.Usage:
			record.kind = "usage"
			record.sid = string(c.Session)
			record.msg = string(c.Message)
		default:
			panic("unobserved command")
		}
		select {
		case queue <- record:
			return runner.Accepted
		default:
			return runner.Busy
		}
	}
	a.runtime.connectTo = func(g runner.Generation, connection runner.Connection) {
		for {
			select {
			case <-queue:
			default:
				goto drained
			}
		}
	drained:
		server, _ := a.serverByID(connection.ID)
		queue <- commandRecord{kind: "server", route: uint64(g), server: server, pins: connection.Pins}
	}
}
