package desktop

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestDismissedDialogsCancelPostedRequestsOnRebuild(t *testing.T) {
	for _, kind := range []string{"attach", "browse", "pair"} {
		t.Run(kind, func(t *testing.T) {
			a := newTestApp(t)
			manual := new(manualBackground)
			a.runtime.background = manual.submit
			var request *requestState
			switch kind {
			case "attach":
				a.layout.attach = &attachState{open: true}
				request = &a.layout.attach.request
			case "browse":
				a.layout.browse = &browseState{open: true}
				request = &a.layout.browse.request
			case "pair":
				a.layout.serverEditor = &serverEditor{open: true}
				request = a.registryRequest("pair:editor")
			}
			var workerContext context.Context
			runRequest(a, request, func(ctx context.Context) (int, error) { workerContext = ctx; return 1, nil }, func(int, error) { t.Fatal("dismissed dialog accepted publication") })
			manual.finish(t, 0)
			switch kind {
			case "attach":
				a.layout.attach.open = false
			case "browse":
				a.layout.browse.open = false
			case "pair":
				a.layout.serverEditor.open = false
			}
			ui.NewTester(a.view, 1400, 920)
			if workerContext.Err() != context.Canceled {
				t.Fatal("dismissed dialog retained its request")
			}
			manual.publish(t, 0)
		})
	}
}
