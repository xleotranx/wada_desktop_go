package main

import (
	"context"
	"fmt"
)

// Find saved pins beyond page one without changing the Recents cursor.
func loadPinnedSessions(ctx context.Context, ids []string, first []agentSession, next string,
	list func(context.Context, string, string) ([]agentSession, string, error)) ([]agentSession, error) {
	missing := map[string]bool{}
	for _, id := range ids {
		missing[id] = true
	}
	for _, s := range first {
		delete(missing, s.id)
	}
	var found []agentSession
	seen := map[string]bool{}
	for len(missing) > 0 && next != "" {
		if seen[next] {
			return found, fmt.Errorf("server repeated the conversation cursor")
		}
		seen[next] = true
		rows, cursor, err := list(ctx, next, "")
		if err != nil {
			return found, err
		}
		for _, s := range rows {
			if missing[s.id] {
				found = append(found, s)
				delete(missing, s.id)
			}
		}
		next = cursor
	}
	return found, nil
}
