package desktop

import (
	"context"
	"time"

	"wada-desktop-go/internal/agent"
)

type searchRequest struct {
	config agent.Config
	route  uint64

	text    string
	version uint64
}

// searchChats debounces typing independently of the agent's control jobs.
// A new query cancels the old lookup; revisions also reject replies that
// were already posted when the query changed, including an A-B-A edit.
func searchChats(ctx context.Context, queries <-chan searchRequest,
	lookup func(context.Context, searchRequest) ([]agent.Session, error),
	post func(func()), accept func(searchRequest, []agent.Session, error)) {
	var pending searchRequest
	var cancel context.CancelFunc
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	defer func() {
		if cancel != nil {
			cancel()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case query := <-queries:
			if cancel != nil {
				cancel()
			}
			pending = query
			timer.Stop()
			if query.text != "" {
				timer.Reset(120 * time.Millisecond)
			}
		case <-timer.C:
			requestCtx, stop := context.WithTimeout(ctx, 15*time.Second)
			cancel = stop
			query := pending
			go func() {
				defer stop()
				rows, err := lookup(requestCtx, query)
				if requestCtx.Err() == context.Canceled {
					return
				}
				post(func() {
					if ctx.Err() == nil {
						accept(query, rows, err)
					}
				})
			}()
		}
	}
}

func (a *app) acceptSearch(query searchRequest, rows []agent.Session, err error) {
	if a.runtime.closed || a.connections.route != query.route || a.search.searchVersion != query.version {
		return
	}
	a.search.searchLoading = false
	a.search.searchResults = rows
	if err != nil {
		a.search.searchResults = nil
		a.streamLine("The search did not run: " + err.Error())
	}
}
