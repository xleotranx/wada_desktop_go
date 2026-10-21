package runner

import (
	"context"
	"fmt"
	"wada-desktop-go/internal/agent"
)

// Find saved pins beyond page one without changing the Recents cursor.
func LoadPinnedSessions(ctx context.Context, ids []string, first []agent.Session, next string,
	list func(context.Context, string, string) ([]agent.Session, string, error)) ([]agent.Session, error) {
	missing := map[string]bool{}
	for _, id := range ids {
		missing[id] = true
	}
	for _, s := range first {
		delete(missing, s.ID)
	}
	var found []agent.Session
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
			if missing[s.ID] {
				found = append(found, s)
				delete(missing, s.ID)
			}
		}
		next = cursor
	}
	return found, nil
}
