package main

import (
	"context"
	"time"
	"wada-desktop-go/internal/opencode"
)

type searchRequest struct {
	text    string
	version uint64
}

// searchChats debounces typing independently of the agent's control jobs.
// A new query cancels the old lookup; revisions also reject replies that
// were already posted when the query changed, including an A-B-A edit.
func searchChats(ctx context.Context, queries <-chan searchRequest,
	lookup func(context.Context, string) ([]opencode.Session, error),
	post func(func()), accept func(searchRequest, []opencode.Session, error)) {
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
				rows, err := lookup(requestCtx, query.text)
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

func (a *app) acceptSearch(query searchRequest, rows []opencode.Session, err error) {
	if a.searchVersion != query.version {
		return
	}
	a.searchLoading = false
	a.searchResults = rows
	if err != nil {
		a.searchResults = nil
		a.streamLine("The search did not run: " + err.Error())
	}
}
