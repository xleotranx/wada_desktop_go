package main

import (
	"context"
	"errors"
	"testing"
)

func TestPinnedSessionsBeyondFirstPage(t *testing.T) {
	calls := 0
	rows, err := loadPinnedSessions(context.Background(), []string{"new", "old"}, []agentSession{{id: "new"}}, "page2",
		func(_ context.Context, cursor, search string) ([]agentSession, string, error) {
			calls++
			if cursor != "page2" || search != "" {
				t.Fatalf("lookup %q, %q", cursor, search)
			}
			return []agentSession{{id: "other"}, {id: "old", title: "Saved older chat"}}, "page3", nil
		})
	if err != nil || calls != 1 || len(rows) != 1 || rows[0].id != "old" {
		t.Fatalf("pins: %+v, %v; calls %d", rows, err, calls)
	}
}

func TestPinnedSessionLookupErrorsAndMissingPins(t *testing.T) {
	for _, scenario := range []string{"missing", "error", "repeated cursor"} {
		t.Run(scenario, func(t *testing.T) {
			_, err := loadPinnedSessions(context.Background(), []string{"gone"}, nil, "next",
				func(context.Context, string, string) ([]agentSession, string, error) {
					if scenario == "error" {
						return nil, "", errors.New("offline")
					}
					if scenario == "repeated cursor" {
						return nil, "next", nil
					}
					return nil, "", nil
				})
			if (err != nil) != (scenario != "missing") {
				t.Fatalf("unexpected lookup result: %v", err)
			}
		})
	}
}
